package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func llmReadJSON(r *http.Request, out any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("bounded JSON required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("one typed JSON object required")
	}
	return nil
}

var llmDNSLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func llmResourceName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) > 63 || !llmDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func (h *LLMHandler) validateProvider(ctx context.Context, p models.ProviderConfig) error {
	if !llmResourceName(p.Provider) || p.SecretRef != "" && !llmResourceName(p.SecretRef) || p.Enabled && p.SecretRef == "" {
		return fmt.Errorf("provider and enabled provider Kubernetes credential reference must be valid resource names")
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && u.Scheme != "http" || strings.ContainsAny(p.BaseURL, "\r\n") {
			return fmt.Errorf("provider base URL must be an HTTP(S) endpoint without credentials, query or fragment")
		}
	}
	if len(p.DisplayName) > 512 || len(p.Region) > 128 || len(p.ProjectID) > 256 || strings.ContainsAny(p.DisplayName+p.Region+p.ProjectID, "\x00\r\n") {
		return fmt.Errorf("invalid provider metadata")
	}
	if h.secrets != nil && p.SecretRef != "" {
		// Deployments let the API read only Secret Manager secrets whose ID
		// starts with secrets.IDPrefix (an IAM condition), so any other name
		// would fail as a bare permission error. Refuse it up front instead.
		if !strings.HasPrefix(p.SecretRef, secrets.IDPrefix) {
			return fmt.Errorf("credential reference %q must start with %q: the API may only read Secret Manager secrets with that prefix (Terraform variable api_secret_prefix)", p.SecretRef, secrets.IDPrefix)
		}
		exists, err := h.secrets.Exists(ctx, p.SecretRef)
		if status.Code(err) == codes.PermissionDenied {
			return fmt.Errorf("credential reference %q cannot be checked: the API is not permitted to read it; secrets it reads must start with %q", p.SecretRef, secrets.IDPrefix)
		}
		if err != nil {
			return fmt.Errorf("credential reference validation is unavailable")
		}
		if !exists {
			return fmt.Errorf("credential reference does not exist in configured Secret Manager")
		}
	}
	return nil
}

// Explicit provider registrations constrain new routes. Unregistered providers
// retain legacy operator-managed Kubernetes secret defaults for compatibility.
func (h *LLMHandler) resolveProviders(ctx context.Context, c *models.LLMRouteConfig, providers map[string]models.ProviderConfig) error {
	for i, b := range c.Backends {
		if !b.Enabled {
			continue
		}
		if p, registered := providers[b.Provider]; registered {
			if !p.Enabled {
				return fmt.Errorf("enabled backend uses a disabled registered provider")
			}
			if b.SecretRef == "" {
				c.Backends[i].SecretRef = p.SecretRef
			}
		}
	}
	return nil
}

func (h *LLMHandler) routeManifests(ctx context.Context, c *models.LLMRouteConfig) ([]deploy.Manifest, error) {
	if len(c.Backends) > 128 || len(c.Fallbacks) > 32 {
		return nil, fmt.Errorf("route backend or fallback inventory exceeds supported limits")
	}
	configured, err := h.store.ListProviderConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider configurations unavailable")
	}
	providers := map[string]models.ProviderConfig{}
	for _, p := range configured {
		providers[p.Provider] = p
	}
	if err := h.resolveProviders(ctx, c, providers); err != nil {
		return nil, err
	}
	fallbacks := map[string]*models.LLMRouteConfig{}
	for _, id := range c.Fallbacks {
		if id == c.ModelID || fallbacks[id] != nil || !llmResourceName("llm-"+id) || id == "" {
			return nil, fmt.Errorf("fallbacks must be distinct valid route model identifiers")
		}
		f, err := h.store.GetRouteConfig(ctx, id)
		if err != nil || f == nil || f.ActivationPending || f.DeletionPending {
			return nil, fmt.Errorf("fallback model requires a saved route configuration")
		}
		copy := *f
		copy.Backends = append([]models.LLMBackend(nil), f.Backends...)
		if err := h.resolveProviders(ctx, &copy, providers); err != nil {
			return nil, err
		}
		fallbacks[id] = &copy
	}
	return deploy.GenerateRoutesWithFallbacksChecked(c, h.gatewayName, fallbacks)
}

func backendNames(c *models.LLMRouteConfig) []string {
	if c == nil {
		return nil
	}
	var names []string
	for _, b := range c.Backends {
		if b.Enabled {
			name, err := deploy.LLMBackendName(c.ModelID, b.Provider, c.BackendNamingVersion)
			if err == nil {
				names = append(names, name)
			}
		}
	}
	return names
}

func sameBackendNames(a, b *models.LLMRouteConfig) bool {
	one, two := backendNames(a), backendNames(b)
	slices.Sort(one)
	slices.Sort(two)
	return slices.Equal(one, two)
}

func ownedRetiredBackend(model, name string) bool {
	return deploy.LLMBackendOwnedByModel(model, name)
}

// Legacy concatenated names are not injective across arbitrary model/provider
// pairs. Preserve deployed names, but refuse collisions with every other saved
// active or retired inventory before writing or deleting a backend.
func (h *LLMHandler) checkBackendOwnership(ctx context.Context, model string, names []string) error {
	routes, err := h.store.ListRouteConfigs(ctx)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.ModelID == model {
			continue
		}
		claimed := append(backendNames(&route), route.RetiredBackends...)
		for _, name := range names {
			if slices.Contains(claimed, name) {
				return fmt.Errorf("backend name is claimed by another route")
			}
		}
	}
	return nil
}

func (h *LLMHandler) setRetiredBackends(previous, next *models.LLMRouteConfig) error {
	next.RetiredBackends = nil // caller metadata never supplies deletion authority
	if previous == nil {
		return nil
	}
	active := backendNames(next)
	retired := append(append([]string{}, previous.RetiredBackends...), backendNames(previous)...)
	for _, name := range retired {
		if !ownedRetiredBackend(next.ModelID, name) {
			return fmt.Errorf("saved backend cleanup inventory does not match route ownership")
		}
		if !slices.Contains(active, name) && !slices.Contains(next.RetiredBackends, name) {
			next.RetiredBackends = append(next.RetiredBackends, name)
		}
	}
	return nil
}

func (h *LLMHandler) cleanupBackends(ctx context.Context, previous, next *models.LLMRouteConfig) error {
	var names []string
	if next == nil {
		names = append(append([]string{}, previous.RetiredBackends...), backendNames(previous)...)
	} else {
		names = next.RetiredBackends
	}
	model := previous.ModelID
	if next != nil {
		model = next.ModelID
	}
	if err := h.checkBackendOwnership(ctx, model, names); err != nil {
		return err
	}
	for _, name := range names {
		if !ownedRetiredBackend(model, name) {
			return fmt.Errorf("backend cleanup ownership mismatch")
		}
		if err := h.k8s.Delete(ctx, "aigateway.envoyproxy.io/v1alpha1", "AIServiceBackend", name, "aiplex-system"); err != nil {
			return err
		}
	}
	return nil
}

func (h *LLMHandler) routeNotReferenced(ctx context.Context, id string) error {
	routes, err := h.store.ListRouteConfigs(ctx)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if route.ModelID != id && slices.Contains(route.Fallbacks, id) {
			return fmt.Errorf("route is used as a fallback")
		}
	}
	return nil
}

func (h *LLMHandler) providerNotReferenced(ctx context.Context, id string) error {
	routes, err := h.store.ListRouteConfigs(ctx)
	if err != nil {
		return err
	}
	for _, route := range routes {
		for _, backend := range route.Backends {
			if backend.Provider == id && backend.Enabled {
				return fmt.Errorf("provider is used by enabled route")
			}
		}
		for _, version := range []int{0, 2} {
			name, err := deploy.LLMBackendName(route.ModelID, id, version)
			if err == nil && slices.Contains(route.RetiredBackends, name) {
				return fmt.Errorf("provider is still represented by pending backend cleanup")
			}
		}
	}
	return nil
}

func validLLMPrice(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }

func validateLLMUsage(r models.UsageRecord) error {
	if r.ModelID == "" || r.Provider == "" || len(r.ModelID) > 512 || len(r.Provider) > 128 || len(r.ID) > 512 || len(r.AgentID) > 512 || len(r.UserID) > 1024 || strings.ContainsAny(r.ID, "/\x00\r\n") || strings.ContainsAny(r.ModelID+r.Provider+r.AgentID+r.UserID, "\x00\r\n") {
		return fmt.Errorf("bounded model, provider and report identifiers required")
	}
	if r.InputTokens < 0 || r.OutputTokens < 0 || r.InputTokens > math.MaxInt-r.OutputTokens || r.LatencyMs < 0 || !validLLMPrice(r.CostUSD) {
		return fmt.Errorf("usage counters and reporting cost must be nonnegative and within range")
	}
	return nil
}

func (h *LLMHandler) usageWarnings(w http.ResponseWriter, r *http.Request, record models.UsageRecord) {
	rc, err := h.store.GetRouteConfig(r.Context(), record.ModelID)
	if errors.Is(err, registry.ErrNotFound) {
		return
	}
	if err != nil || rc == nil {
		w.Header().Set("X-LLM-Reporting-Warning", "configuration_unavailable")
		return
	}
	if b := rc.Budget; b != nil {
		for _, period := range []string{"day", "month"} {
			limit := b.MaxDailyCostUSD
			label := "daily"
			if period == "month" {
				limit, label = b.MaxMonthlyCostUSD, "monthly"
			}
			summary, err := h.store.GetUsageSummary(r.Context(), record.ModelID, "", period)
			if err != nil || summary == nil {
				w.Header().Set("X-LLM-Reporting-Warning", "budget_summary_unavailable")
				continue
			}
			if limit > 0 && summary.TotalCostUSD > limit {
				w.Header().Add("X-Budget-Warning", label+"_cost_exceeded")
			} else if limit > 0 && b.AlertThreshold > 0 && summary.TotalCostUSD >= limit*b.AlertThreshold/100 {
				w.Header().Add("X-Budget-Warning", label+"_cost_threshold")
			}
			if period == "day" && b.MaxDailyTokens > 0 && summary.TotalTokens > b.MaxDailyTokens {
				w.Header().Add("X-Budget-Warning", "daily_tokens_exceeded")
			}
		}
	}
	if limits := rc.RateLimit; limits != nil {
		recent, err := h.store.ListUsageRecords(r.Context(), record.ModelID, "", time.Now().Add(-time.Minute), 0)
		if err != nil {
			w.Header().Set("X-LLM-Reporting-Warning", "rate_summary_unavailable")
			return
		}
		if limits.RequestsPerMinute > 0 && len(recent) > limits.RequestsPerMinute {
			w.Header().Add("X-RateLimit-Warning", "requests_per_minute_exceeded")
		}
		var tokens int64
		for _, usage := range recent {
			if usage.TotalTokens < 0 || int64(usage.TotalTokens) > math.MaxInt64-tokens {
				w.Header().Set("X-LLM-Reporting-Warning", "rate_summary_out_of_range")
				return
			}
			tokens += int64(usage.TotalTokens)
		}
		if limits.TokensPerMinute > 0 && tokens > int64(limits.TokensPerMinute) {
			w.Header().Add("X-RateLimit-Warning", "tokens_per_minute_exceeded")
		}
	}
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type membershipDeploymentConfig struct {
	HTTP          membership.HTTPConfig `json:"http"`
	IdentityRef   string                `json:"identity_ref"`
	CacheTTL      time.Duration         `json:"cache_ttl,omitempty"`
	CacheCapacity int                   `json:"cache_capacity,omitempty"`
}

func configuredMembership(spec *membershipDeploymentConfig, h *api.ConnectedHandler, tenants []api.QuotaTenantConfig) (api.QuotaMembershipResolver, error) {
	if spec == nil {
		return nil, nil
	}
	if h.Runtime == nil || h.Runtime.Policy == nil || h.Runtime.HTTP == nil || spec.IdentityRef == "" || h.Runtime.Policy.ValidateURL(spec.HTTP.Endpoint) != nil {
		return nil, fmt.Errorf("membership authority needs configured endpoint and credential policy")
	}
	allowed := map[string]bool{}
	for _, tenant := range tenants {
		allowed[tenant.Policy.TenantID] = true
		if err := h.Runtime.CheckDestination(tenant.Policy.TenantID, spec.IdentityRef, spec.HTTP.Endpoint); err != nil {
			return nil, err
		}
	}
	authority, err := membership.NewHTTP(spec.HTTP, h.Runtime.HTTP, func(ctx context.Context, tenant string) (string, error) {
		if !allowed[tenant] {
			return "", fmt.Errorf("membership tenant unavailable")
		}
		if err := h.Runtime.CheckDestination(tenant, spec.IdentityRef, spec.HTTP.Endpoint); err != nil {
			return "", err
		}
		return h.Runtime.Token(ctx, tenant, spec.IdentityRef)
	})
	if err != nil {
		return nil, err
	}
	capacity := spec.CacheCapacity
	if capacity == 0 {
		capacity = 1024
	}
	cached, err := membership.NewCache(authority, spec.CacheTTL, capacity)
	if err != nil {
		return nil, err
	}
	return auth.NewDirectoryMembershipResolver(cached)
}

type quotaSchedulingRoute struct {
	ResourceID  string `json:"resource_id"`
	OperationID string `json:"operation_id"`
	PayerID     string `json:"payer_id,omitempty"`
	FlowID      string `json:"flow_id"`
}
type quotaSchedulingDeployment struct {
	ExpectedDigest string                    `json:"expected_digest,omitempty"`
	Config         quotaplex.SchedulerConfig `json:"config"`
	Routes         []quotaSchedulingRoute    `json:"routes"`
	PollInterval   time.Duration             `json:"poll_interval,omitempty"`
}
type configuredSchedule struct {
	deployment quotaSchedulingDeployment
	scheduler  *quotaplex.AtomicScheduler
	policy     *quotaplex.CompiledPolicy
}
type quotaScheduledAdmission struct {
	manifest []quotaSchedulingDeployment
	entries  map[string]*configuredSchedule
	digest   string
}

func schedulerCoordinate(tenant, pool string) string { return quotaCoordinate(tenant, pool) }
func newQuotaScheduledAdmission(manifest []quotaSchedulingDeployment) (*quotaScheduledAdmission, error) {
	if len(manifest) > 128 {
		return nil, fmt.Errorf("too many configured scheduler pools")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var frozen []quotaSchedulingDeployment
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	manifest = frozen
	a := &quotaScheduledAdmission{manifest: manifest, entries: map[string]*configuredSchedule{}, digest: quotaDigest(manifest)}
	for _, m := range manifest {
		tenant, pool := m.Config.Capacity.TenantID, m.Config.Capacity.PoolID
		key := schedulerCoordinate(tenant, pool)
		if quotaplex.ValidateStoreCoordinate(tenant, "pool", pool) != nil || a.entries[key] != nil || len(m.Routes) == 0 || len(m.Routes) > 1024 {
			return nil, fmt.Errorf("unique scheduler pool and bounded routes required")
		}
		if m.PollInterval == 0 {
			m.PollInterval = 100 * time.Millisecond
		}
		if m.PollInterval < time.Millisecond || m.PollInterval > time.Second {
			return nil, fmt.Errorf("scheduler polling interval must be 1ms..1s")
		}
		seen := map[string]bool{}
		for i, route := range m.Routes {
			for j := 0; j < i; j++ {
				previous := m.Routes[j]
				if previous.ResourceID == route.ResourceID && previous.OperationID == route.OperationID && (previous.PayerID == "" || route.PayerID == "" || previous.PayerID == route.PayerID) {
					return nil, fmt.Errorf("overlapping scheduler payer routes")
				}
			}
		}
		for _, route := range m.Routes {
			if quotaplex.ValidateStoreCoordinate(tenant, route.ResourceID, route.OperationID) != nil || m.Config.Queue.Weights[route.FlowID] == 0 {
				return nil, fmt.Errorf("exact scheduler route and configured flow required")
			}
			rkey := quotaDigest([]string{route.ResourceID, route.OperationID, route.PayerID})
			if seen[rkey] {
				return nil, fmt.Errorf("ambiguous scheduler route")
			}
			seen[rkey] = true
		}
		a.entries[key] = &configuredSchedule{deployment: m}
	}
	return a, nil
}
func (a *quotaScheduledAdmission) ConfigurationDigest() string { return a.digest }
func (a *quotaScheduledAdmission) initialize(ctx context.Context, engines map[string]*quotaplex.Engine, tenants []api.QuotaTenantConfig) error {
	policies := map[string]*quotaplex.CompiledPolicy{}
	for _, tenant := range tenants {
		p, err := quotaplex.Compile(tenant.Policy)
		if err != nil {
			return err
		}
		policies[tenant.Policy.TenantID] = p
	}
	for _, entry := range a.entries {
		routes := append([]quotaSchedulingRoute(nil), entry.deployment.Routes...)
		sort.Slice(routes, func(i, j int) bool { return quotaDigest(routes[i]) < quotaDigest(routes[j]) })
		flowDigest := quotaDigest(routes)
		if configured := entry.deployment.Config.FlowAuthorityDigest; configured != "" && configured != flowDigest {
			return fmt.Errorf("scheduler flow authority disagrees with configured routing")
		}
		entry.deployment.Config.FlowAuthorityDigest = flowDigest
		tenant := entry.deployment.Config.Capacity.TenantID
		engine := engines[tenant]
		if engine == nil {
			return fmt.Errorf("scheduler references unavailable tenant")
		}
		scheduler, err := quotaplex.NewAtomicScheduler(engine, entry.deployment.Config)
		if err != nil {
			return err
		}
		if err = scheduler.Activate(ctx, entry.deployment.ExpectedDigest); err != nil {
			return err
		}
		entry.scheduler = scheduler
		entry.policy = policies[tenant]
	}
	return nil
}
func (a *quotaScheduledAdmission) Recheck(ctx context.Context, e *quotaplex.Engine, d quotaplex.Demand) error {
	if entry := a.entries[schedulerCoordinate(d.Context.TenantID, d.Target.PoolID)]; entry != nil {
		if entry.scheduler == nil {
			return fmt.Errorf("scheduler not initialized")
		}
		return entry.scheduler.Ready(ctx)
	}
	return nil
}
func (a *quotaScheduledAdmission) Admit(ctx context.Context, e *quotaplex.Engine, d quotaplex.Demand) (quotaplex.Decision, error) {
	entry := a.entries[schedulerCoordinate(d.Context.TenantID, d.Target.PoolID)]
	if entry == nil {
		return e.Admit(ctx, d)
	}
	if entry.scheduler == nil || entry.policy == nil {
		return quotaplex.Decision{}, fmt.Errorf("scheduler not initialized")
	}
	plan, err := entry.policy.Resolve(d, time.Now())
	if err != nil {
		return e.Admit(ctx, d)
	}
	flow := ""
	for _, route := range entry.deployment.Routes {
		if route.ResourceID == d.Target.ResourceID && route.OperationID == d.Target.Operation && (route.PayerID == "" || route.PayerID == plan.PayerID) {
			if flow != "" {
				return quotaplex.Decision{}, fmt.Errorf("scheduler route overlaps for selected payer")
			}
			flow = route.FlowID
		}
	}
	if flow == "" {
		return quotaplex.Decision{}, fmt.Errorf("scheduled target has no authorized flow")
	}
	deadline := time.Now().Add(entry.deployment.Config.Queue.MaxWait)
	if stop, ok := ctx.Deadline(); ok && stop.Before(deadline) {
		deadline = stop
	}
	if previous, getErr := entry.scheduler.Get(ctx, d.AttemptID); getErr == nil {
		if previous.Decision != nil {
			return e.Admit(ctx, d)
		}
		deadline = previous.Deadline
	} else if !errors.Is(getErr, quotaplex.ErrNotFound) {
		return quotaplex.Decision{}, getErr
	}
	status, err := entry.scheduler.Enqueue(ctx, quotaplex.ScheduledRequest{Demand: d, FlowID: flow, Deadline: deadline})
	if err != nil {
		return quotaplex.Decision{}, err
	}
	finished := false
	defer func() {
		if !finished {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = entry.scheduler.Cancel(bounded, d.AttemptID)
			_ = e.Abort(bounded, d.Context.TenantID, d.AttemptID)
		}
	}()
	timer := time.NewTicker(entry.deployment.PollInterval)
	defer timer.Stop()
	for {
		if status.Decision != nil {
			finished = true
			return *status.Decision, nil
		}
		if status.State != "queued" {
			if status.State == "expired" {
				return quotaplex.Decision{}, context.DeadlineExceeded
			}
			return quotaplex.Decision{}, fmt.Errorf("scheduled attempt is %s", status.State)
		}
		if !time.Now().Before(deadline) {
			return quotaplex.Decision{}, context.DeadlineExceeded
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return quotaplex.Decision{}, err
		}
		_, err = entry.scheduler.Next(ctx, hex.EncodeToString(nonce[:]))
		if err != nil {
			return quotaplex.Decision{}, err
		}
		status, err = entry.scheduler.Get(ctx, d.AttemptID)
		if err != nil {
			return quotaplex.Decision{}, err
		}
		if status.Decision != nil {
			continue
		}
		select {
		case <-ctx.Done():
			return quotaplex.Decision{}, ctx.Err()
		case <-timer.C:
		}
	}
}

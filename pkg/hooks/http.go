package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// Sidecar formats.
const (
	SidecarAIPlex   = "aiplex"
	SidecarPresidio = "presidio"
	SidecarLLMGuard = "llm-guard"

	// ContractVersion is the version string of the AIPlex sidecar contract.
	ContractVersion = "aiplex.hooks.v1"

	maxSidecarResponse = 4 << 20
)

// HTTPConfig calls an operator-run sidecar. The endpoint must be approved by
// the deployment's outbound endpoint policy (the same policy that governs
// tool token endpoints); redirects are never followed.
type HTTPConfig struct {
	Endpoint string `json:"endpoint"`
	// Format is aiplex (default; the contract in docs/request-hooks.md),
	// presidio or llm-guard.
	Format string `json:"format,omitempty"`
	// AuthSecretRef names a secret sent as "Authorization: Bearer <value>".
	// Only the reference is stored.
	AuthSecretRef string `json:"auth_secret_ref,omitempty"`
	// FailOpen lets the exchange continue when the sidecar fails or times
	// out. The default (false) refuses the exchange. Every exchange that
	// used it is marked fail_open_used in the usage ledger.
	FailOpen bool            `json:"fail_open,omitempty"`
	Presidio *PresidioConfig `json:"presidio,omitempty"`
	LLMGuard *LLMGuardConfig `json:"llm_guard,omitempty"`
}

// PresidioConfig adapts Presidio Analyzer (POST {endpoint}/analyze) and,
// optionally, Presidio Anonymizer (POST {anonymizer_endpoint}/anonymize).
type PresidioConfig struct {
	Language       string   `json:"language,omitempty"`
	Entities       []string `json:"entities,omitempty"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
	// Action is mask (default: each detected span becomes [ENTITY_TYPE]) or
	// deny (any detection refuses the exchange).
	Action string `json:"action,omitempty"`
	// AnonymizerEndpoint, when set, masks through the Anonymizer service
	// instead of locally.
	AnonymizerEndpoint string `json:"anonymizer_endpoint,omitempty"`
}

// LLMGuardConfig adapts the LLM Guard API (POST /analyze/prompt,
// /analyze/output, /scan/prompt, /scan/output).
type LLMGuardConfig struct {
	// Mode is analyze (default: use the sanitized text and refuse invalid
	// input) or scan (refuse invalid input; never change text).
	Mode             string   `json:"mode,omitempty"`
	ScannersSuppress []string `json:"scanners_suppress,omitempty"`
}

var entityName = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

// ValidateEndpointURL checks an endpoint's shape. The host's outbound policy
// is checked separately by the caller.
func ValidateEndpointURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\\\r\n\t ") || len(raw) > 2048 {
		return errors.New("endpoint must be an absolute http(s) URL without credentials, query or fragment")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("endpoint must use https (or http on an approved loopback origin)")
	}
	return nil
}

func (c HTTPConfig) validate() error {
	if err := ValidateEndpointURL(c.Endpoint); err != nil {
		return fmt.Errorf("http.%v", err)
	}
	if c.AuthSecretRef != "" && !toolcredential.ValidSecretRef(c.AuthSecretRef) {
		return errors.New("http.auth_secret_ref must be a secret reference name, not a value")
	}
	switch c.Format {
	case "", SidecarAIPlex:
		if c.Presidio != nil || c.LLMGuard != nil {
			return errors.New("http.presidio and http.llm_guard apply only to their formats")
		}
	case SidecarPresidio:
		if c.LLMGuard != nil {
			return errors.New("http.llm_guard applies only to format llm-guard")
		}
		if p := c.Presidio; p != nil {
			if p.Action != "" && p.Action != ActionMask && p.Action != ActionDeny {
				return errors.New("http.presidio.action must be mask or deny")
			}
			if len(p.Language) > 8 || len(p.Entities) > 64 {
				return errors.New("http.presidio language or entities are out of bounds")
			}
			for _, e := range p.Entities {
				if !entityName.MatchString(e) {
					return errors.New("http.presidio.entities must be entity type names such as EMAIL_ADDRESS")
				}
			}
			if p.ScoreThreshold != nil && (*p.ScoreThreshold < 0 || *p.ScoreThreshold > 1) {
				return errors.New("http.presidio.score_threshold must be between 0 and 1")
			}
			if p.AnonymizerEndpoint != "" {
				if err := ValidateEndpointURL(p.AnonymizerEndpoint); err != nil {
					return fmt.Errorf("http.presidio.anonymizer_%v", err)
				}
			}
		}
	case SidecarLLMGuard:
		if c.Presidio != nil {
			return errors.New("http.presidio applies only to format presidio")
		}
		if g := c.LLMGuard; g != nil {
			if g.Mode != "" && g.Mode != "analyze" && g.Mode != "scan" {
				return errors.New("http.llm_guard.mode must be analyze or scan")
			}
			if len(g.ScannersSuppress) > 64 {
				return errors.New("http.llm_guard.scanners_suppress has too many entries")
			}
		}
	default:
		return errors.New("http.format must be aiplex, presidio or llm-guard")
	}
	return nil
}

// Endpoints lists every URL the hook may call, for egress validation.
func (c HTTPConfig) Endpoints() []string {
	out := []string{c.Endpoint}
	if c.Presidio != nil && c.Presidio.AnonymizerEndpoint != "" {
		out = append(out, c.Presidio.AnonymizerEndpoint)
	}
	return out
}

// sidecar posts JSON and decodes a bounded JSON answer. Errors never carry
// the request or response body.
type sidecar struct {
	client *http.Client
	auth   string
}

func (s sidecar) post(ctx context.Context, endpoint string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return errors.New("sidecar request could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("sidecar endpoint is invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if s.auth != "" {
		req.Header.Set("Authorization", "Bearer "+s.auth)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("sidecar unreachable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSidecarResponse+1))
	if err != nil || len(data) > maxSidecarResponse {
		return errors.New("sidecar response unreadable or too large")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sidecar answered HTTP %d", resp.StatusCode)
	}
	if media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); media != "application/json" {
		return errors.New("sidecar answer is not JSON")
	}
	if _, err := parseJSON(data); err != nil {
		return errors.New("sidecar answer is not unambiguous JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, strict := out.(*sidecarAnswer); strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(out); err != nil {
		return errors.New("sidecar answer does not match the contract")
	}
	return nil
}

func runHTTP(ctx context.Context, rt Runtime, h Hook, in Input) (Verdict, error) {
	if rt.Client == nil {
		return Verdict{}, errors.New("sidecar client unavailable")
	}
	client := *rt.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	s := sidecar{client: &client}
	if ref := h.HTTP.AuthSecretRef; ref != "" {
		if rt.Secret == nil {
			return Verdict{}, errors.New("sidecar secret unavailable")
		}
		value, err := rt.Secret(ctx, ref)
		if err != nil || value == "" || strings.ContainsAny(value, "\r\n") {
			return Verdict{}, errors.New("sidecar secret unavailable")
		}
		s.auth = value
	}
	switch h.HTTP.Format {
	case SidecarPresidio:
		return runPresidio(ctx, s, h.HTTP, in)
	case SidecarLLMGuard:
		return runLLMGuard(ctx, s, h.HTTP, in)
	}
	return runNative(ctx, s, h.HTTP, in)
}

// ── AIPlex contract ───────────────────────────────────────────────────────

// SidecarRequest is the body POSTed to an aiplex-format sidecar.
type SidecarRequest struct {
	Version     string        `json:"version"`
	Phase       string        `json:"phase"`
	Meta                      // plane, operation, provider, model, tool, caller kind, principal, access point
	Parts       []Part        `json:"parts"`
	Uninspected []Uninspected `json:"uninspected"`
}

type sidecarAnswer struct {
	Decision string `json:"decision"`
	// Reason is a rule identifier; it is recorded only when it is a short
	// lowercase identifier and never shown to the caller.
	Reason string            `json:"reason,omitempty"`
	Parts  []sidecarPart     `json:"parts,omitempty"`
	Rules  []sidecarRuleHits `json:"rules,omitempty"`
}

type sidecarPart struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type sidecarRuleHits struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func safeRule(id string) string {
	if identifier.MatchString(id) {
		return id
	}
	return "sidecar"
}

func runNative(ctx context.Context, s sidecar, cfg *HTTPConfig, in Input) (Verdict, error) {
	req := SidecarRequest{Version: ContractVersion, Phase: in.Phase, Meta: in.Meta, Parts: in.Parts, Uninspected: in.Uninspected}
	if req.Parts == nil {
		req.Parts = []Part{}
	}
	if req.Uninspected == nil {
		req.Uninspected = []Uninspected{}
	}
	var answer sidecarAnswer
	if err := s.post(ctx, cfg.Endpoint, req, &answer); err != nil {
		return Verdict{}, err
	}
	v := Verdict{Action: answer.Decision, Counts: map[string]int{}}
	for _, r := range answer.Rules {
		if r.Count < 0 || r.Count > 1<<20 {
			return Verdict{}, errors.New("sidecar rule count out of range")
		}
		v.Counts[safeRule(r.ID)] += r.Count
	}
	switch answer.Decision {
	case Allow:
		if len(answer.Parts) > 0 {
			return Verdict{}, errors.New("sidecar allow carries parts")
		}
	case Deny:
		v.Rule = safeRule(answer.Reason)
		if answer.Reason == "" {
			v.Rule = "sidecar"
		}
	case Transform:
		v.Replacements = map[int]string{}
		for _, p := range answer.Parts {
			if _, dup := v.Replacements[p.Index]; dup || p.Index < 0 || p.Index >= len(in.Parts) || !utf8.ValidString(p.Text) {
				return Verdict{}, errors.New("sidecar transform is invalid")
			}
			v.Replacements[p.Index] = p.Text
		}
	default:
		return Verdict{}, errors.New("sidecar decision is not allow, deny or transform")
	}
	return v, nil
}

// ── Presidio ──────────────────────────────────────────────────────────────

type presidioResult struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

func runPresidio(ctx context.Context, s sidecar, cfg *HTTPConfig, in Input) (Verdict, error) {
	p := PresidioConfig{}
	if cfg.Presidio != nil {
		p = *cfg.Presidio
	}
	if p.Language == "" {
		p.Language = "en"
	}
	base := strings.TrimSuffix(cfg.Endpoint, "/")
	v := Verdict{Action: Allow, Counts: map[string]int{}, Replacements: map[int]string{}}
	for i, part := range in.Parts {
		if part.Text == "" {
			continue
		}
		req := map[string]any{"text": part.Text, "language": p.Language}
		if len(p.Entities) > 0 {
			req["entities"] = p.Entities
		}
		if p.ScoreThreshold != nil {
			req["score_threshold"] = *p.ScoreThreshold
		}
		var results []presidioResult
		if err := s.post(ctx, base+"/analyze", req, &results); err != nil {
			return Verdict{}, err
		}
		if len(results) == 0 {
			continue
		}
		// Presidio offsets index the Python string: Unicode code points.
		runes := []rune(part.Text)
		for _, r := range results {
			if r.Start < 0 || r.End <= r.Start || r.End > len(runes) || !entityName.MatchString(r.EntityType) {
				return Verdict{}, errors.New("presidio result is out of range")
			}
			v.Counts[strings.ToLower(r.EntityType)]++
		}
		if p.Action == ActionDeny {
			if v.Rule == "" {
				v.Rule = strings.ToLower(results[0].EntityType)
			}
			continue
		}
		if p.AnonymizerEndpoint != "" {
			var anonymized struct {
				Text string `json:"text"`
			}
			req := map[string]any{"text": part.Text, "analyzer_results": results, "anonymizers": map[string]any{"DEFAULT": map[string]any{"type": "replace"}}}
			if err := s.post(ctx, strings.TrimSuffix(p.AnonymizerEndpoint, "/")+"/anonymize", req, &anonymized); err != nil {
				return Verdict{}, err
			}
			v.Replacements[i] = anonymized.Text
			continue
		}
		v.Replacements[i] = maskRunes(runes, results)
	}
	switch {
	case v.Rule != "":
		v.Action, v.Replacements = Deny, nil
	case len(v.Replacements) > 0:
		v.Action = Transform
	}
	return v, nil
}

// maskRunes replaces each span (code point offsets) with [ENTITY_TYPE];
// overlapping spans merge into the earliest, longest one.
func maskRunes(runes []rune, results []presidioResult) string {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Start != results[j].Start {
			return results[i].Start < results[j].Start
		}
		return results[i].End > results[j].End
	})
	var b strings.Builder
	at := 0
	for _, r := range results {
		if r.Start < at {
			continue
		}
		b.WriteString(string(runes[at:r.Start]))
		b.WriteString("[" + r.EntityType + "]")
		at = r.End
	}
	b.WriteString(string(runes[at:]))
	return b.String()
}

// ── LLM Guard ─────────────────────────────────────────────────────────────

type llmGuardAnswer struct {
	IsValid         *bool              `json:"is_valid"`
	Scanners        map[string]float64 `json:"scanners"`
	SanitizedPrompt *string            `json:"sanitized_prompt"`
	SanitizedOutput *string            `json:"sanitized_output"`
}

var scannerName = regexp.MustCompile(`[^a-z0-9_.-]+`)

func runLLMGuard(ctx context.Context, s sidecar, cfg *HTTPConfig, in Input) (Verdict, error) {
	g := LLMGuardConfig{}
	if cfg.LLMGuard != nil {
		g = *cfg.LLMGuard
	}
	mode := "analyze"
	if g.Mode == "scan" {
		mode = "scan"
	}
	target := "prompt"
	if in.Phase == PhaseResponse {
		target = "output"
	}
	endpoint := strings.TrimSuffix(cfg.Endpoint, "/") + "/" + mode + "/" + target
	suppress := g.ScannersSuppress
	if suppress == nil {
		suppress = []string{}
	}
	v := Verdict{Action: Allow, Counts: map[string]int{}, Replacements: map[int]string{}}
	for i, part := range in.Parts {
		if part.Text == "" {
			continue
		}
		req := map[string]any{"scanners_suppress": suppress}
		if target == "prompt" {
			req["prompt"] = part.Text
		} else {
			// The request prompt is not kept for the response phase; output
			// scanners that compare against the prompt see an empty one.
			req["prompt"], req["output"] = "", part.Text
		}
		var answer llmGuardAnswer
		if err := s.post(ctx, endpoint, req, &answer); err != nil {
			return Verdict{}, err
		}
		if answer.IsValid == nil {
			return Verdict{}, errors.New("llm-guard answer has no is_valid")
		}
		names := make([]string, 0, len(answer.Scanners))
		for name, score := range answer.Scanners {
			if score > 0 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			v.Counts[safeRule(strings.Trim(scannerName.ReplaceAllString(strings.ToLower(name), "_"), "_"))]++
		}
		if !*answer.IsValid {
			if v.Rule == "" {
				v.Rule = "llm_guard"
				if len(names) > 0 {
					v.Rule = safeRule(strings.Trim(scannerName.ReplaceAllString(strings.ToLower(names[0]), "_"), "_"))
				}
			}
			continue
		}
		if mode == "analyze" {
			sanitized := answer.SanitizedPrompt
			if target == "output" {
				sanitized = answer.SanitizedOutput
			}
			if sanitized == nil {
				return Verdict{}, errors.New("llm-guard analyze answer has no sanitized text")
			}
			if *sanitized != part.Text {
				v.Replacements[i] = *sanitized
			}
		}
	}
	switch {
	case v.Rule != "":
		v.Action, v.Replacements = Deny, nil
	case len(v.Replacements) > 0:
		v.Action = Transform
	}
	return v, nil
}

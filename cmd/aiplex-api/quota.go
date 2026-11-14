package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

type quotaDeploymentFile struct {
	MembershipAuthority *membershipDeploymentConfig `json:"membership_authority,omitempty"`
	Scheduling          []quotaSchedulingDeployment `json:"scheduling,omitempty"`
	Administration      api.QuotaAdminOptions       `json:"administration,omitempty"`
	Version             string                      `json:"version"`
	Host                api.QuotaHostConfig         `json:"host"`
	ProviderLimits      []providerLimitEvidence     `json:"provider_limits,omitempty"`
	Actors              []quotaplex.Subject         `json:"actors,omitempty"`
	RecoveryInterval    string                      `json:"recovery_interval,omitempty"`
	// UnknownWarnAfter is the age after which an attempt still in the
	// unknown state is logged as a warning. Go duration, 1m to 720h.
	UnknownWarnAfter string `json:"unknown_warn_after,omitempty"`
}

// defaultUnknownWarnAfter is the documented default of unknown_warn_after.
const defaultUnknownWarnAfter = time.Hour

func firestoreConfiguration(cfg *config.Config) (string, string) {
	project, database := cfg.GCPProject, cfg.FirestoreDB
	if project == "" {
		project = os.Getenv("GCP_PROJECT_ID")
	}
	if project == "" {
		project = os.Getenv("FIRESTORE_PROJECT_ID")
	}
	if _, canonical := os.LookupEnv("FIRESTORE_DATABASE"); !canonical && (database == "" || database == "(default)") {
		if legacy := os.Getenv("FIRESTORE_DATABASE_ID"); legacy != "" {
			database = legacy
		}
	}
	if database == "" {
		database = "(default)"
	}
	return project, database
}

// providerLimitEvidence is an operator attestation of provider-enforced limits,
// not a client request limit or tokenizer estimate. Production deployment must
// verify its source and the exact model/version/account/region before activation.
type providerLimitEvidence struct {
	ID                    string    `json:"id"`
	TenantID              string    `json:"tenant_id"`
	BindingID             string    `json:"binding_id"`
	Provider              string    `json:"provider"`
	Model                 string    `json:"model"`
	ModelVersion          string    `json:"model_version"`
	BillingAccount        string    `json:"billing_account"`
	Region                string    `json:"region"`
	InputCeiling          int64     `json:"input_ceiling"`
	CombinedOutputCeiling int64     `json:"combined_output_ceiling"`
	SourceRef             string    `json:"source_ref"`
	IssuedAt              time.Time `json:"issued_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type declaredProviderLimits struct {
	entries map[string]providerLimitEvidence
	digest  string
}

func quotaDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func quotaCoordinate(tenant, id string) string { return quotaDigest([]string{tenant, id}) }

func newDeclaredProviderLimits(entries []providerLimitEvidence, now time.Time) (*declaredProviderLimits, error) {
	if len(entries) > 256 {
		return nil, fmt.Errorf("too many provider limit attestations")
	}
	copy := append([]providerLimitEvidence(nil), entries...)
	sort.Slice(copy, func(i, j int) bool {
		return quotaCoordinate(copy[i].TenantID, copy[i].BindingID) < quotaCoordinate(copy[j].TenantID, copy[j].BindingID)
	})
	a := &declaredProviderLimits{entries: map[string]providerLimitEvidence{}, digest: quotaDigest(copy)}
	for _, e := range copy {
		for _, v := range []string{e.ID, e.TenantID, e.BindingID, e.Provider, e.Model, e.ModelVersion, e.BillingAccount, e.Region, e.SourceRef} {
			if strings.TrimSpace(v) == "" || len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
				return nil, fmt.Errorf("provider limits require bounded exact provenance coordinates")
			}
		}
		if e.InputCeiling <= 0 || e.CombinedOutputCeiling <= 0 || e.IssuedAt.IsZero() || e.IssuedAt.After(now) || !e.ExpiresAt.After(now) || !e.ExpiresAt.After(e.IssuedAt) {
			return nil, fmt.Errorf("provider limit evidence is incomplete or expired")
		}
		key := quotaCoordinate(e.TenantID, e.BindingID)
		if _, ok := a.entries[key]; ok {
			return nil, fmt.Errorf("duplicate provider limit binding")
		}
		a.entries[key] = e
	}
	return a, nil
}

func (a *declaredProviderLimits) ConfigurationDigest() string { return a.digest }
func (a *declaredProviderLimits) Bounds(ctx context.Context, r api.QuotaBoundsRequest) (api.QuotaBoundsProof, error) {
	if err := ctx.Err(); err != nil {
		return api.QuotaBoundsProof{}, err
	}
	e, ok := a.entries[quotaCoordinate(r.TenantID, r.BindingID)]
	if !ok || r.At.Before(e.IssuedAt) || !r.At.Before(e.ExpiresAt) || r.Model != e.Model || r.Target.Provider != e.Provider || r.Target.ModelVersion != e.ModelVersion || r.Target.BillingAccount != e.BillingAccount || r.Target.Region != e.Region || r.RequestedOutput <= 0 || r.RequestedOutput > e.CombinedOutputCeiling {
		return api.QuotaBoundsProof{}, fmt.Errorf("current provider-enforced limit evidence does not cover this target")
	}
	return api.QuotaBoundsProof{NativeBounds: map[string]int64{"uncached_input_tokens": e.InputCeiling, "cached_tokens": e.InputCeiling, "output_tokens": r.RequestedOutput, "thought_tokens": r.RequestedOutput}, EvidenceRef: e.ID + ":" + quotaDigest(e), ExpiresAt: e.ExpiresAt, ConfigurationDigest: r.Target.ConfigurationDigest, ModelVersion: e.ModelVersion}, nil
}

type configuredActorMembership struct{ actors map[string]quotaplex.Subject }

func newConfiguredActorMembership(subjects []quotaplex.Subject, now time.Time) (*configuredActorMembership, error) {
	if len(subjects) > 1024 {
		return nil, fmt.Errorf("too many configured actor snapshots")
	}
	a := &configuredActorMembership{actors: map[string]quotaplex.Subject{}}
	for _, s := range subjects {
		m := s.Membership
		if s.Kind != "agent" || s.ID != m.SubjectID || quotaplex.ValidateStoreCoordinate(m.TenantID, "actor", s.ID) != nil || quotaplex.ValidateStoreCoordinate(m.TenantID, m.Ref.ID, m.Ref.Version) != nil || m.IssuedAt.IsZero() || m.IssuedAt.After(now) || !m.ExpiresAt.After(now) || !m.ExpiresAt.After(m.IssuedAt) || m.Groups == nil || len(m.Groups) > 1024 {
			return nil, fmt.Errorf("independent actor membership evidence is invalid or expired")
		}
		seen := map[string]bool{}
		for _, g := range m.Groups {
			key := quotaDigest(g)
			if quotaplex.ValidateStoreCoordinate(m.TenantID, g.Source, g.ID) != nil || seen[key] {
				return nil, fmt.Errorf("actor groups require unique source-qualified identities")
			}
			seen[key] = true
		}
		key := quotaCoordinate(m.TenantID, s.ID)
		if _, exists := a.actors[key]; exists {
			return nil, fmt.Errorf("duplicate actor membership snapshot")
		}
		s.Membership.Groups = append([]quotaplex.GroupRef{}, s.Membership.Groups...)
		a.actors[key] = s
	}
	return a, nil
}
func (a *configuredActorMembership) ResolveActor(ctx context.Context, tenant, id string, now time.Time) (quotaplex.Subject, error) {
	if err := ctx.Err(); err != nil {
		return quotaplex.Subject{}, err
	}
	s, ok := a.actors[quotaCoordinate(tenant, id)]
	if !ok || s.Membership.IssuedAt.After(now) || !s.Membership.ExpiresAt.After(now) {
		return quotaplex.Subject{}, fmt.Errorf("independent actor membership unavailable")
	}
	s.Membership.Groups = append([]quotaplex.GroupRef{}, s.Membership.Groups...)
	return s, nil
}

func loadQuotaFile(filename string) (quotaDeploymentFile, error) {
	var cfg quotaDeploymentFile
	f, err := os.Open(filename)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return cfg, fmt.Errorf("quota configuration exceeds its 4MiB bound")
	}
	if err = validateQuotaJSON(raw); err != nil {
		return cfg, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid quota configuration: %w", err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return cfg, fmt.Errorf("trailing quota configuration")
	}
	if cfg.Version != quotaplex.Version {
		return cfg, fmt.Errorf("unsupported quota configuration version")
	}
	return cfg, nil
}

func validateQuotaJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	remaining := 100000
	var value func(int) error
	value = func(depth int) error {
		remaining--
		if depth > 32 || remaining < 0 {
			return fmt.Errorf("quota configuration exceeds complexity bound")
		}
		token, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid quota JSON")
		}
		delim, nested := token.(json.Delim)
		if !nested {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return fmt.Errorf("invalid quota object")
				}
				canonical := strings.ToLower(name)
				if keys[canonical] {
					return fmt.Errorf("duplicate quota JSON key")
				}
				keys[canonical] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid quota object")
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid quota array")
			}
		default:
			return fmt.Errorf("invalid quota JSON")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing quota JSON")
	}
	return nil
}

type quotaRecoveryRunner interface {
	RunOnce(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error)
	RecoverAttempts(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error)
	ReconcileLoans(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error)
}
type quotaWorker struct {
	tenant   string
	recovery quotaRecoveryRunner
	cursors  [3]string
	// scan accumulates the unknown-attempt summary of the current attempts
	// scan across pages; broken records that a page of it failed. unknown is
	// the last complete scan (guarded by quotaRuntime.unknownMu).
	scan    *quotaplex.UnknownExposure
	broken  bool
	unknown *quotaplex.UnknownExposure
	// completedAt is when unknown was published (guarded by unknownMu).
	completedAt time.Time
}
type quotaRuntime struct {
	mu             sync.Mutex
	host           *api.QuotaHost
	workers        []quotaWorker
	interval       time.Duration
	cancel         context.CancelFunc
	done           chan struct{}
	running        bool
	administration api.QuotaAdminOptions
	warnAfter      time.Duration
	unknownMu      sync.Mutex
	// clock is time.Now unless a test replaces it.
	clock func() time.Time
}

func (m *quotaRuntime) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// lastCompleteScanAge is how long ago the tenant's figures were published,
// or "never". The caller holds no lock.
func (m *quotaRuntime) lastCompleteScanAge(w *quotaWorker) string {
	m.unknownMu.Lock()
	at := w.completedAt
	m.unknownMu.Unlock()
	if at.IsZero() {
		return "never"
	}
	return m.now().Sub(at).Round(time.Second).String()
}

func configureQuota(ctx context.Context, cfg *config.Config, store registry.Store, h *api.ConnectedHandler) (*quotaRuntime, error) {
	enabled := false
	for _, name := range cfg.EnabledModules {
		if name == "quotaplex" {
			enabled = true
		}
	}
	if !enabled {
		if cfg.QuotaFile != "" {
			return nil, fmt.Errorf("quota configuration requires the quotaplex module")
		}
		return nil, nil
	}
	if cfg.QuotaFile == "" {
		return nil, fmt.Errorf("quotaplex requires AIPLEX_QUOTA_FILE")
	}
	spec, err := loadQuotaFile(cfg.QuotaFile)
	if err != nil {
		return nil, err
	}
	interval := 5 * time.Second
	if spec.RecoveryInterval != "" {
		interval, err = time.ParseDuration(spec.RecoveryInterval)
		if err != nil || interval < time.Second || interval > time.Hour {
			return nil, fmt.Errorf("quota recovery interval must be between 1s and 1h")
		}
	}
	warnAfter := defaultUnknownWarnAfter
	if spec.UnknownWarnAfter != "" {
		warnAfter, err = time.ParseDuration(spec.UnknownWarnAfter)
		if err != nil || warnAfter < time.Minute || warnAfter > 720*time.Hour {
			return nil, fmt.Errorf("quota unknown_warn_after must be between 1m and 720h")
		}
	}
	var quotaStore quotaplex.Store
	if durable, ok := store.(interface{ QuotaStore() quotaplex.Store }); ok {
		quotaStore = durable.QuotaStore()
	} else if cfg.DevelopmentMode {
		quotaStore = quotaplex.NewMemoryStore()
	} else {
		return nil, fmt.Errorf("production quotaplex requires a shared durable quota store")
	}
	limits, err := newDeclaredProviderLimits(spec.ProviderLimits, time.Now())
	if err != nil {
		return nil, err
	}
	actors, err := newConfiguredActorMembership(spec.Actors, time.Now())
	if err != nil {
		return nil, err
	}
	var options []api.QuotaHostOption
	membershipResolver, err := configuredMembership(spec.MembershipAuthority, h, spec.Host.Tenants)
	if err != nil {
		return nil, err
	}
	if membershipResolver != nil {
		options = append(options, api.WithQuotaMembershipResolver(membershipResolver))
	}
	var scheduled *quotaScheduledAdmission
	if len(spec.Scheduling) > 0 {
		scheduled, err = newQuotaScheduledAdmission(spec.Scheduling)
		if err != nil {
			return nil, err
		}
		options = append(options, api.WithQuotaAdmissionController(scheduled))
	}
	host, err := api.NewQuotaHost(ctx, spec.Host, quotaStore, time.Now, actors, limits, options...)
	if err != nil {
		return nil, err
	}
	if scheduled != nil {
		if err = scheduled.initialize(ctx, host.Engines(), spec.Host.Tenants); err != nil {
			return nil, err
		}
	}
	runtime := &quotaRuntime{host: host, interval: interval, administration: spec.Administration, warnAfter: warnAfter}
	engines := host.Engines()
	names := make([]string, 0, len(engines))
	for tenant := range engines {
		names = append(names, tenant)
	}
	sort.Strings(names)
	for _, tenant := range names {
		worker, err := quotaplex.NewRecovery(engines[tenant], nil)
		if err != nil {
			return nil, err
		}
		runtime.workers = append(runtime.workers, quotaWorker{tenant: tenant, recovery: worker})
	}
	h.Quota = host
	h.AttachBudgets()
	telemetry.Metrics().ObserveQuotaUnknown(runtime.unknownMetrics)
	return runtime, nil
}

func (m *quotaRuntime) Descriptor() modules.Descriptor {
	return modules.Descriptor{Name: "quotaplex", Version: "0.3.0", ContractVersion: modules.ContractVersion, Capabilities: []string{"quota.managed-model", "quota.managed-tool", "quota.terminal-outbox", "quota.recovery", "quota.administration", "quota.scheduling"}, Navigation: []modules.Navigation{{Path: "/quotaplex", Title: "QuotaPlex"}}, Dependencies: []modules.Dependency{{Name: "identity", ContractVersion: modules.ContractVersion}}, APIPrefixes: []string{"/api/v1/quota"}}
}
func (m *quotaRuntime) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.host.Ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.running = true
	go m.run(workerCtx, m.done)
	return nil
}
func (m *quotaRuntime) Ready(ctx context.Context) error {
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if !running {
		return fmt.Errorf("quotaplex runtime is not running")
	}
	return m.host.Ready(ctx)
}
func (m *quotaRuntime) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.cancel()
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
		m.mu.Lock()
		if m.done == done {
			m.running = false
		}
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *quotaRuntime) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		m.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *quotaRuntime) pass(ctx context.Context) {
	for i := range m.workers {
		w := &m.workers[i]
		operations := []func(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error){w.recovery.RunOnce, w.recovery.RecoverAttempts, w.recovery.ReconcileLoans}
		for kind, operation := range operations {
			if ctx.Err() != nil {
				return
			}
			bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
			report, err := operation(bounded, quotaplex.RecoveryPage{TenantID: w.tenant, AfterID: w.cursors[kind], Limit: 32})
			cancel()
			if report.Done {
				w.cursors[kind] = ""
			} else if report.Scanned > 0 && report.NextAfterID != "" && quotaplex.ValidateStoreCoordinate(w.tenant, "checkpoint", report.NextAfterID) == nil {
				w.cursors[kind] = report.NextAfterID
			}
			if kind == 1 {
				m.observeUnknown(w, report, err)
			}
			if err != nil {
				log.Error().Str("tenant", w.tenant).Int("kind", kind).Msg("quota recovery pass failed")
				continue
			}
			if report.Failed > 0 {
				log.Warn().Str("tenant", w.tenant).Int("failed", report.Failed).Msg("quota recovery has pending failures")
			}
		}
	}
}

// observeUnknown accumulates one attempts page into the tenant's scan. When
// the scan completes it becomes the tenant's published summary and, if the
// oldest unknown attempt is older than warnAfter (or its age or some records
// cannot be read), one warning is logged. A scan with a failed page is not
// published: its figures could be understated, and the previous ones stay.
func (m *quotaRuntime) observeUnknown(w *quotaWorker, report quotaplex.RecoveryReport, err error) {
	if err != nil || report.Unknown == nil {
		w.broken = true
		// One warning per failed page, that is at most one per tenant and
		// pass, so a scan that never completes is not silent.
		log.Warn().Str("tenant", w.tenant).Str("last_complete_scan", m.lastCompleteScanAge(w)).Msg("quota unknown-attempt scan page failed; the published figures are from the last complete scan")
	} else if w.scan == nil {
		summary := *report.Unknown
		w.scan = &summary
	} else {
		w.scan.Merge(*report.Unknown)
	}
	if !report.Done {
		return
	}
	scan, broken := w.scan, w.broken
	w.scan, w.broken = nil, false
	if broken || scan == nil {
		if err == nil && report.Unknown != nil {
			log.Warn().Str("tenant", w.tenant).Str("last_complete_scan", m.lastCompleteScanAge(w)).Msg("quota unknown-attempt scan did not complete; the previous summary is kept")
		}
		return
	}
	m.unknownMu.Lock()
	w.unknown, w.completedAt = scan, m.now()
	m.unknownMu.Unlock()
	age, known := scan.OldestAge()
	switch {
	case scan.Attempts > 0 && (!known || age > m.warnAfter):
		oldest := "unknown"
		if known {
			oldest = age.Round(time.Second).String()
		}
		exposure := make([]string, 0, len(scan.Exposure))
		for _, e := range scan.Exposure {
			exposure = append(exposure, e.String())
		}
		log.Warn().Str("tenant", w.tenant).Int("attempts", scan.Attempts).Str("oldest_age", oldest).Str("threshold", m.warnAfter.String()).Int("undated", scan.UndatedAttempts).Int("unreadable", scan.Unreadable).Bool("exposure_complete", scan.Complete()).Strs("exposure", exposure).Msg("quota attempts in the unknown state are older than the warning threshold and still hold budget")
	case scan.Unreadable > 0:
		log.Warn().Str("tenant", w.tenant).Int("unreadable", scan.Unreadable).Int("attempts", scan.Attempts).Msg("quota attempt records could not be read; unknown attempts and their exposure may be understated")
	}
}

// unknownMetrics sums the last complete scan of every tenant. It reports
// nothing until every tenant has completed one, so a missing tenant is never
// shown as zero.
func (m *quotaRuntime) unknownMetrics() (telemetry.QuotaUnknown, bool) {
	m.unknownMu.Lock()
	defer m.unknownMu.Unlock()
	if len(m.workers) == 0 {
		return telemetry.QuotaUnknown{}, false
	}
	var total quotaplex.UnknownExposure
	for i := range m.workers {
		if m.workers[i].unknown == nil {
			return telemetry.QuotaUnknown{}, false
		}
		total.Merge(*m.workers[i].unknown)
	}
	out := telemetry.QuotaUnknown{Attempts: int64(total.Attempts), Undated: int64(total.UndatedAttempts), Unreadable: int64(total.Unreadable)}
	// Each tenant's age is taken at its own scan; the oldest is the largest.
	for i := range m.workers {
		if age, ok := m.workers[i].unknown.OldestAge(); ok && age > out.OldestAge {
			out.OldestAge = age
		}
	}
	out.OldestAgeKnown = total.Attempts > 0 && total.UndatedAttempts == 0
	// The stalest tenant decides how old the figures are.
	now := m.now()
	for i := range m.workers {
		if age := now.Sub(m.workers[i].completedAt); age > out.ScanAge {
			out.ScanAge = age
		}
	}
	for _, e := range total.Exposure {
		known := int64(e.Attempts - e.UncomputedAttempts)
		if known == 0 && e.KnownPart > 0 {
			// A flagged cost can carry a known lower bound (see attemptExposure).
			known = 1
		}
		out.Exposure = append(out.Exposure, telemetry.QuotaExposure{Meter: e.MeterID, Unit: e.Unit, Currency: e.Currency, Scale: int(e.Scale), KnownPart: e.KnownPart, KnownAttempts: known, Complete: e.Quantity != nil, Uncomputed: int64(e.UncomputedAttempts)})
	}
	return out, true
}

func (m *quotaRuntime) mount(r chi.Router, h *api.ConnectedHandler) {
	h.MountQuotaAdministrationWithOptions(r, m.administration)
	r.Get("/quota/attempts/{id}", func(w http.ResponseWriter, r *http.Request) {
		tenant, err := h.Tenant(r)
		if err != nil || tenant == "" {
			api.Error(w, r, 403, "TENANT_REQUIRED", "verified tenant required")
			return
		}
		identity := api.GetWIFIdentity(r.Context())
		if identity == nil {
			api.Error(w, r, 401, "IDENTITY_REQUIRED", "verified identity required")
			return
		}
		engine := m.host.Engines()[tenant]
		if engine == nil {
			api.Error(w, r, 404, "NOT_FOUND", "quota attempt unavailable")
			return
		}
		attempt, err := engine.GetAttempt(r.Context(), tenant, chi.URLParam(r, "id"))
		if err != nil {
			status := 503
			if errors.Is(err, quotaplex.ErrNotFound) {
				status = 404
			}
			api.Error(w, r, status, "QUOTA_ATTEMPT_UNAVAILABLE", "quota attempt unavailable")
			return
		}
		// The caller's own attempts: those it made as the principal, or as the
		// delegated actor. Anything else needs the quota read grant, the same
		// grant every other quota read requires; a role alone is not enough.
		own := attempt.Demand.Context.PrincipalID == identity.PrincipalID || (identity.ActorID != "" && attempt.Demand.Context.ActorID == identity.ActorID)
		if !own && h.AuthorizeCurrent(r, "aiplex:quota:read") != nil {
			api.Error(w, r, 404, "NOT_FOUND", "quota attempt unavailable")
			return
		}
		api.JSON(w, 200, attempt)
	})
}

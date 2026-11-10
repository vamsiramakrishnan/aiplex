package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func feedbackFixture(t *testing.T, comments bool) *UsageHandler {
	t.Helper()
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	for id, principal := range map[string]string{"r-alice": "alice", "r-bob": "bob"} {
		rec := usageledger.Record{ID: id, TenantID: "tenant", StartedAt: now, FinishedAt: now, Plane: usageledger.PlaneLLM, ModelID: "m", PrincipalID: principal, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
		if err := store.Append(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	h := NewUsageHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false)
	h.Feedback = usageledger.NewMemoryFeedbackStore(0)
	h.FeedbackComments = comments
	return h
}

func feedbackCall(t *testing.T, h *UsageHandler, method, id, body string, access *models.ResolvedAccess) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/api/v1/usage/records/"+id+"/feedback", strings.NewReader(body))
	r.SetPathValue("id", id)
	ctx := r.Context()
	if access != nil {
		identity := access.Identity
		ctx = context.WithValue(context.WithValue(ctx, keyWIFIdentity, &identity), keyWIFAccess, access)
	}
	w := httptest.NewRecorder()
	if method == http.MethodPost {
		h.PostFeedback(w, r.WithContext(ctx))
	} else {
		h.ListFeedback(w, r.WithContext(ctx))
	}
	return w
}

func deployerAccess(principal string) *models.ResolvedAccess {
	return &models.ResolvedAccess{Identity: models.WIFIdentity{PrincipalID: principal}, Roles: []models.IAMRole{models.RoleDeployer}}
}

func TestFeedbackIsForTheRecordsOwnerOrAnOperator(t *testing.T) {
	h := feedbackFixture(t, false)
	if w := feedbackCall(t, h, "POST", "r-alice", `{"score":1,"label":"helpful"}`, viewerAccess("alice")); w.Code != 201 {
		t.Fatalf("owner: %d %s", w.Code, w.Body.String())
	}
	// Another caller cannot tell that the record exists.
	if w := feedbackCall(t, h, "POST", "r-alice", `{"score":-1}`, viewerAccess("mallory")); w.Code != 404 {
		t.Fatalf("stranger: %d %s", w.Code, w.Body.String())
	}
	if w := feedbackCall(t, h, "GET", "r-alice", ``, viewerAccess("mallory")); w.Code != 404 {
		t.Fatalf("stranger reading history: %d", w.Code)
	}
	for _, op := range []*models.ResolvedAccess{adminAccess("root"), deployerAccess("dev")} {
		if w := feedbackCall(t, h, "POST", "r-bob", `{"score":-0.5}`, op); w.Code != 201 {
			t.Fatalf("operator %v: %d %s", op.Roles, w.Code, w.Body.String())
		}
	}
	if w := feedbackCall(t, h, "POST", "missing", `{"score":0}`, adminAccess("root")); w.Code != 404 {
		t.Fatalf("missing record: %d", w.Code)
	}
	// The author is the verified principal, never a body field.
	var fb usageledger.Feedback
	w := feedbackCall(t, h, "POST", "r-alice", `{"score":0.25}`, viewerAccess("alice"))
	_ = json.Unmarshal(w.Body.Bytes(), &fb)
	if fb.By != "alice" || fb.TenantID != "tenant" || fb.RecordID != "r-alice" {
		t.Fatalf("%+v", fb)
	}
	if w := feedbackCall(t, h, "POST", "r-alice", `{"score":1,"by":"bob"}`, viewerAccess("alice")); w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
}

func TestFeedbackValidationAndComments(t *testing.T) {
	h := feedbackFixture(t, false)
	alice := viewerAccess("alice")
	for name, body := range map[string]string{
		"no score":      `{}`,
		"high":          `{"score":2}`,
		"string":        `{"score":"up"}`,
		"bad label":     `{"score":1,"label":"Has Space"}`,
		"trailing json": `{"score":1}{"score":2}`,
		"not json":      `score=1`,
	} {
		if w := feedbackCall(t, h, "POST", "r-alice", body, alice); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	w := feedbackCall(t, h, "POST", "r-alice", `{"score":1,"comment":"free text"}`, alice)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "COMMENTS_DISABLED") {
		t.Fatalf("comments are off by default: %d %s", w.Code, w.Body.String())
	}
	if hist, _ := h.Feedback.History(context.Background(), "tenant", "r-alice", 0); len(hist) != 0 {
		t.Fatalf("a refused feedback was stored: %+v", hist)
	}
	on := feedbackFixture(t, true)
	if w := feedbackCall(t, on, "POST", "r-alice", `{"score":1,"comment":"free text"}`, alice); w.Code != 201 || !strings.Contains(w.Body.String(), "free text") {
		t.Fatalf("comments enabled: %d %s", w.Code, w.Body.String())
	}
	cfg := httptest.NewRecorder()
	on.Config(cfg, httptest.NewRequest("GET", "/api/v1/usage/config", nil))
	var uc UsageConfig
	_ = json.Unmarshal(cfg.Body.Bytes(), &uc)
	if !uc.Feedback || !uc.FeedbackComments || uc.TagKeysAllowed == nil {
		t.Fatalf("%+v", uc)
	}
	h.Feedback = nil
	if w := feedbackCall(t, h, "POST", "r-alice", `{"score":1}`, alice); w.Code != 503 {
		t.Fatalf("not configured: %d", w.Code)
	}
}

func TestFeedbackAppearsInRecordDetailAndSummaries(t *testing.T) {
	h := feedbackFixture(t, false)
	alice := viewerAccess("alice")
	feedbackCall(t, h, "POST", "r-alice", `{"score":-1}`, alice)
	feedbackCall(t, h, "POST", "r-alice", `{"score":1,"label":"helpful"}`, alice)
	var detail struct {
		ID       string
		Feedback *usageledger.Feedback
	}
	if err := json.Unmarshal(usageGet(t, h.GetRecord, "/api/v1/usage/records/r-alice", alice, 200), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != "r-alice" || detail.Feedback == nil || detail.Feedback.Score != 1 || detail.Feedback.Label != "helpful" {
		t.Fatalf("latest wins in the record detail: %+v", detail)
	}
	hist := feedbackCall(t, h, "GET", "r-alice", ``, alice)
	var history struct{ Feedback []usageledger.Feedback }
	_ = json.Unmarshal(hist.Body.Bytes(), &history)
	if len(history.Feedback) != 2 || history.Feedback[0].Score != 1 || history.Feedback[1].Score != -1 {
		t.Fatalf("append-only history, newest first: %s", hist.Body.String())
	}

	var s usageledger.Summary
	body := usageGet(t, h.Summary, "/api/v1/usage/summary?feedback=true", adminAccess("root"), 200)
	_ = json.Unmarshal(body, &s)
	fb := s.Groups[0].Feedback
	if fb == nil || fb.Records != 1 || fb.Without != 1 || fb.AverageScore == nil || *fb.AverageScore != 1 {
		t.Fatalf("summary feedback: %s", body)
	}
	plain := usageGet(t, h.Summary, "/api/v1/usage/summary", adminAccess("root"), 200)
	if strings.Contains(string(plain), `"feedback"`) {
		t.Fatalf("feedback totals are opt-in: %s", plain)
	}
	usageGet(t, h.Summary, "/api/v1/usage/summary?feedback=maybe", adminAccess("root"), 400)
	usageGet(t, h.Summary, "/api/v1/usage/summary?merge_sources=true&source=managed", adminAccess("root"), 400)
}

func TestUsageQueriesFilterAndGroupByTagAndHarness(t *testing.T) {
	store := usageledger.NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	add := func(id string, mutate func(*usageledger.Record)) {
		r := usageledger.Record{ID: id, TenantID: "tenant", StartedAt: now, FinishedAt: now, Plane: usageledger.PlaneLLM, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
		mutate(&r)
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	add("a", func(r *usageledger.Record) { r.Tags = map[string]string{"experiment": "arm-a"} })
	add("b", func(r *usageledger.Record) { r.Tags = map[string]string{"experiment": "arm-b"} })
	add("c", func(r *usageledger.Record) { r.Source, r.Harness = usageledger.SourceReported, "claude-code" })
	h := NewUsageHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, false)
	admin := adminAccess("root")
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?tag.experiment=arm-a", admin, 200)); strings.Join(ids, ",") != "a" {
		t.Fatalf("tag filter: %v", ids)
	}
	if ids := recordIDs(t, usageGet(t, h.ListRecords, "/api/v1/usage/records?harness=claude-code", admin, 200)); strings.Join(ids, ",") != "c" {
		t.Fatalf("harness filter: %v", ids)
	}
	var s usageledger.Summary
	_ = json.Unmarshal(usageGet(t, h.Summary, "/api/v1/usage/summary?group_by=tag:experiment&source=managed", admin, 200), &s)
	if len(s.Groups) != 2 || s.SourceSplit {
		t.Fatalf("%+v", s)
	}
	usageGet(t, h.ListRecords, "/api/v1/usage/records?tag.Bad=x", admin, 400)
	usageGet(t, h.ListRecords, "/api/v1/usage/records?tag.experiment=bad%0Avalue", admin, 400)
	usageGet(t, h.Summary, "/api/v1/usage/summary?group_by=tag:Bad", admin, 400)
}

func TestTagPolicyAllowlistConflictsAndCardinalityCap(t *testing.T) {
	p, err := NewTagPolicy([]string{"variant", "prompt"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got := p.Resolve(ctx, "t", map[string]string{"arm": "control", "variant": "configured"}, "variant=hdr,prompt=v1,secret=x,arm=hack,bad key=1,prompt")
	// Configuration wins; unlisted and malformed parts are dropped.
	want := map[string]string{"arm": "control", "variant": "configured", "prompt": "v1"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%v", got)
		}
	}
	// A third distinct value for a key in one tenant becomes (other); known values keep working.
	p.Resolve(ctx, "t", nil, "prompt=v2")
	if got := p.Resolve(ctx, "t", nil, "prompt=v3"); got["prompt"] != usageledger.TagOverflowValue {
		t.Fatalf("over the cap: %v", got)
	}
	if got := p.Resolve(ctx, "t", nil, "prompt=v1"); got["prompt"] != "v1" {
		t.Fatalf("a value seen before is still admitted: %v", got)
	}
	// The cap is per tenant.
	if got := p.Resolve(ctx, "other", nil, "prompt=v3"); got["prompt"] != "v3" {
		t.Fatalf("another tenant: %v", got)
	}
	// Literal overflow marker is not accepted from a caller.
	if got := p.Resolve(ctx, "other", nil, "prompt=(other)"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// Default: nothing allowed.
	var none *TagPolicy
	if got := none.Resolve(ctx, "t", map[string]string{"arm": "c"}, "variant=x"); len(got) != 1 || got["arm"] != "c" {
		t.Fatalf("%v", got)
	}
	if _, err := NewTagPolicy([]string{"Bad Key"}, 0); err == nil {
		t.Fatal("invalid allowlist key accepted")
	}
	if _, err := TagPolicyFromEnv(func(k string) string {
		if k == EnvTagMaxValues {
			return "0"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid cap accepted")
	}
}

func TestAccessPointLabelsAndCallerTagsReachTheLedgerRecord(t *testing.T) {
	f := newAPFixture(t)
	records := attachLedger(t, f.h)
	policy, err := NewTagPolicy([]string{"variant"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Tags = policy
	spec := apSpec()
	spec.Labels = map[string]string{"experiment": "exp-7"}
	current, err := f.store.Get(context.Background(), "tenant", AccessPointKind, "ide")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: AccessPointKind, ID: "ide", Spec: specJSON(t, spec)}, current.Revision); err != nil {
		t.Fatal(err)
	}
	chat := `{"model":"chat-model","messages":[{"role":"user","content":"hi"}]}`
	w := f.do("POST", "/ap/ide/v1/chat/completions", chat, "X-AIPlex-Tags", "variant=b,experiment=hijack,other=x", "X-Client-Request-Id", "client-req-1", "Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	r := onlyRecord(t, records())
	if len(r.Tags) != 2 || r.Tags["experiment"] != "exp-7" || r.Tags["variant"] != "b" {
		t.Fatalf("tags: %+v", r.Tags)
	}
	if r.ClientRequestID != "client-req-1" || r.CallerSpanID != "b7ad6b7169203331" {
		t.Fatalf("correlation keys: %+v", r)
	}
	// An access point save with invalid labels is refused.
	bad := apSpec()
	bad.Labels = map[string]string{"Bad Key": "x"}
	if _, err := f.h.validateAccessPoint(context.Background(), "tenant", "ide", specJSON(t, bad)); err == nil {
		t.Fatal("invalid label key accepted")
	}
	bad.Labels = map[string]string{"experiment": usageledger.TagOverflowValue}
	if _, err := f.h.validateAccessPoint(context.Background(), "tenant", "ide", specJSON(t, bad)); err == nil {
		t.Fatal("the overflow marker is reserved")
	}
}

func specJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTraceURLTemplateValidation(t *testing.T) {
	for _, ok := range []string{"", "https://trace.example/t/{trace_id}", "http://localhost:16686/trace/{trace_id}?x=1"} {
		if err := ValidateTraceURLTemplate(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"https://trace.example/t", "javascript:alert({trace_id})", "ftp://x/{trace_id}", "//x/{trace_id}", "https://user:pw@x/{trace_id}", "/relative/{trace_id}"} {
		if err := ValidateTraceURLTemplate(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

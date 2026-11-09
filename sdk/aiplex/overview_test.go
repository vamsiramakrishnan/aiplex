package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seenCall struct{ method, path, query, ifMatch, body string }

func recordingServer(t *testing.T, calls *[]seenCall, handler func(w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, seenCall{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("If-Match"), string(body)})
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Error("missing bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	c := NewClient(server.URL)
	c.SetToken("t")
	return c
}

func TestAccessPointCRUDAndQuotaPlan(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/quota-plan"):
			_, _ = w.Write([]byte(`{"access_point_id":"ide","tenant_id":"t","applied":false,"fragment":{"rates":[{"id":"ap-ide-rpm","rate":60}]},"scheduler":{"class":"gold","flow_id":"ap-ide","weight":10,"routes":[]},"policy":{},"validation":{"valid":true,"validator":"compile"},"warnings":["w"]}`))
		case r.URL.Path == "/api/v1/access-points/":
			_, _ = w.Write([]byte(`[{"tenant_id":"t","kind":"access-point","id":"ide","revision":3,"spec":{"id":"ide","caller_kinds":["user"],"subjects":{},"models":[{"name":"m","surface_ref":"s","native_model":"n"}],"qos":{"class":"gold"}}}]`))
		default:
			_, _ = w.Write([]byte(`{"tenant_id":"t","kind":"access-point","id":"ide","revision":4,"spec":{"id":"ide","caller_kinds":["user"],"subjects":{}}}`))
		}
	})
	ctx := context.Background()
	list, err := c.ListAccessPoints(ctx)
	if err != nil || len(list) != 1 || list[0].Revision != 3 || list[0].Spec.QoS.Class != "gold" || list[0].Spec.Models[0].SurfaceRef != "s" {
		t.Fatal(list, err)
	}
	got, err := c.GetAccessPoint(ctx, "ide")
	if err != nil || got.Revision != 4 {
		t.Fatal(got, err)
	}
	spec := AccessPoint{ID: "ide", CallerKinds: []string{CallerKindUser}, Models: []AccessPointModel{{Name: "m", SurfaceRef: "s", NativeModel: "n"}}}
	if _, err = c.PutAccessPoint(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutAccessPoint(ctx, spec, 4); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteAccessPoint(ctx, "ide", 4); err != nil {
		t.Fatal(err)
	}
	plan, err := c.PlanAccessPointQuota(ctx, "ide")
	if err != nil || plan.Applied || len(plan.Fragment.Rates) != 1 || plan.Scheduler.Weight != 10 || !plan.Validation.Valid || plan.Warnings[0] != "w" {
		t.Fatal(plan, err)
	}
	// calls: list, get, put(create), put(replace), delete, plan
	if calls[2].ifMatch != "0" || calls[3].ifMatch != "4" || calls[4].ifMatch != "4" {
		t.Fatalf("If-Match wrong: %+v", calls)
	}
	if calls[0].path != "/api/v1/access-points/" || calls[2].method != http.MethodPut || calls[2].path != "/api/v1/access-points/ide" || calls[5].method != http.MethodPost || calls[5].path != "/api/v1/access-points/ide/quota-plan" {
		t.Fatalf("paths wrong: %+v", calls)
	}
	if !strings.Contains(calls[2].body, `"caller_kinds":["user"]`) {
		t.Fatalf("body = %s", calls[2].body)
	}
	if err = c.DeleteAccessPoint(ctx, "ide", 0); err == nil {
		t.Fatal("delete without revision accepted")
	}
	if _, err = c.GetAccessPoint(ctx, "../x"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err = c.PutAccessPoint(ctx, AccessPoint{}, 0); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestUserIdentityProviderRoutesAndExtraFields(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/identity/user-provider-presets":
			_, _ = w.Write([]byte(`{"presets":[{"id":"okta","display_name":"Okta"}],"live_authentication":true}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/plan"):
			_, _ = w.Write([]byte(`{"vendor":"okta","issuer":"https://i","audience":"a","steps":[],"aiplex_steps":[],"principal_pool":"user-federation/t"}`))
		case r.URL.Path == "/api/v1/identity/user-providers":
			_, _ = w.Write([]byte(`[{"id":"okta","preset":"okta","issuer":"https://i","revision":2,"principal_pool":"user-federation/t","directory":{"kind":"scim"},"client_claim":"azp"}]`))
		default:
			_, _ = w.Write([]byte(`{"id":"okta","preset":"okta","revision":2,"principal_pool":"p","new_field":[1,2]}`))
		}
	})
	ctx := context.Background()
	cat, err := c.ListUserIdentityPresets(ctx)
	if err != nil || !cat.LiveAuthentication || len(cat.Presets) != 1 {
		t.Fatal(cat, err)
	}
	if p, ok := cat.Preset("okta"); !ok || p.ID != "okta" {
		t.Fatal("preset lookup")
	}
	list, err := c.ListUserIdentityProviders(ctx)
	if err != nil || len(list) != 1 || list[0].Revision != 2 || list[0].PrincipalPool != "user-federation/t" || list[0].Issuer != "https://i" {
		t.Fatal(list, err)
	}
	if string(list[0].Extra["directory"]) != `{"kind":"scim"}` || string(list[0].Extra["client_claim"]) != `"azp"` {
		t.Fatalf("extra fields dropped: %v", list[0].Extra)
	}
	if _, ok := list[0].Extra["revision"]; ok {
		t.Fatal("known field leaked into Extra")
	}
	// A response re-encodes with its extras.
	raw, _ := json.Marshal(list[0])
	if !strings.Contains(string(raw), `"directory":{"kind":"scim"}`) || !strings.Contains(string(raw), `"principal_pool":"user-federation/t"`) {
		t.Fatalf("round trip lost fields: %s", raw)
	}
	got, err := c.GetUserIdentityProvider(ctx, "okta")
	if err != nil || string(got.Extra["new_field"]) != `[1,2]` {
		t.Fatal(got, err)
	}

	var req UserIdentityProviderRequest
	in := `{"preset":"okta","inputs":{"domain":"acme.okta.com"},"display_name":"Okta","directory":{"kind":"scim","url":"https://d"},"client_ids":["a","b"],"acknowledge_overlap":true}`
	if err = json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	if req.Preset != "okta" || req.Inputs["domain"] != "acme.okta.com" || len(req.Extra) != 3 {
		t.Fatalf("request = %+v", req)
	}
	if _, err = c.PutUserIdentityProvider(ctx, "okta", req, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutUserIdentityProvider(ctx, "okta", req, 2); err != nil {
		t.Fatal(err)
	}
	var sent map[string]json.RawMessage
	if err = json.Unmarshal([]byte(calls[len(calls)-1].body), &sent); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"preset", "inputs", "display_name", "directory", "client_ids", "acknowledge_overlap"} {
		if _, ok := sent[key]; !ok {
			t.Errorf("request body dropped %q: %s", key, calls[len(calls)-1].body)
		}
	}
	if string(sent["client_ids"]) != `["a","b"]` {
		t.Errorf("client_ids = %s", sent["client_ids"])
	}
	// Extra never overrides a known field.
	req.Extra = map[string]json.RawMessage{"preset": json.RawMessage(`"evil"`)}
	b, _ := json.Marshal(req)
	if !strings.Contains(string(b), `"preset":"okta"`) {
		t.Fatalf("extra overrode a known field: %s", b)
	}
	if err = c.DeleteUserIdentityProvider(ctx, "okta", 2); err != nil {
		t.Fatal(err)
	}
	plan, err := c.PlanUserIdentityProvider(ctx, "okta")
	if err != nil || plan.Vendor != "okta" || plan.PrincipalPool != "user-federation/t" {
		t.Fatal(plan, err)
	}
	// calls: presets, list, get, put, put, delete, plan
	put, replace := calls[3], calls[4]
	if put.ifMatch != "" || replace.ifMatch != "2" || put.path != "/api/v1/identity/user-providers/okta" {
		t.Fatalf("put calls wrong: %+v %+v", put, replace)
	}
	if last := calls[len(calls)-1]; last.method != http.MethodPost || last.path != "/api/v1/identity/user-providers/okta/plan" {
		t.Fatalf("plan call wrong: %+v", last)
	}
	if err = c.DeleteUserIdentityProvider(ctx, "okta", 0); err == nil {
		t.Fatal("delete without revision accepted")
	}
	if _, err = c.GetUserIdentityProvider(ctx, ""); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestUserIdentityVerifyRedactsToken(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"valid":false,"checks":[{"name":"signature","status":"fail","reason":"bad signature"}],"groups_available":false,"principal_id":"","is_workforce":false,"live_authentication":true}`))
	})
	res, err := c.VerifyUserIdentityToken(context.Background(), UserIdentityVerifyRequest{Token: "SECRET", ProviderID: "okta"})
	if err != nil || res.Valid || len(res.Checks) != 1 || res.Checks[0].Reason != "bad signature" || !res.LiveAuthentication {
		t.Fatal(res, err)
	}
	if !strings.Contains(calls[0].body, `"provider_id":"okta"`) || calls[0].path != "/api/v1/identity/user-providers/verify" {
		t.Fatalf("call = %+v", calls[0])
	}
	r := UserIdentityVerifyRequest{Token: "SECRET", ProviderID: "okta"}
	if strings.Contains(fmt.Sprintf("%v %+v %#v %s", r, r, r, r), "SECRET") {
		t.Fatal("token printed by formatting")
	}
	if _, err = c.VerifyUserIdentityToken(context.Background(), UserIdentityVerifyRequest{}); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestOverviewCalls(t *testing.T) {
	var calls []seenCall
	c := recordingServer(t, &calls, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/connect"):
			_, _ = w.Write([]byte(`{"access_point_id":"ide","client":"generic","base_urls":{"openai":"https://g/ap/ide/v1"},"models":[],"mcp_url":"https://g/ap/ide/mcp","auth":{"scheme":"bearer","header":"h","token_env":"AIPLEX_TOKEN","caller_kinds":["user"],"steps":["s"]},"snippets":[{"title":"t","language":"bash","content":"c"}],"notes":["n"]}`))
		case r.URL.Path == "/api/v1/overview/subjects":
			_, _ = w.Write([]byte(`{"kind":"user","since":"2026-11-08T00:00:00Z","subjects":[{"kind":"user","id":"u1","identity":{"source":"workforce-principal","detail":"d"},"access_points":["ide"],"last_seen":null,"usage":{"requests":2,"records":2,"errors":0,"denied":0,"tokens":{"input_tokens":10},"tokens_unknown":{"input_tokens":1,"total_tokens":2},"cost":[{"currency":"USD","basis":"settled","amount":"1234","scale":4}],"unknown_cost_records":1},"known_from":["usage-ledger"]}],"next_cursor":"abc","usage_available":true,"usage_truncated":false,"derived_from":["x"],"limitations":["l"]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/overview/subjects/"):
			_, _ = w.Write([]byte(`{"kind":"agent","id":"tutor","identity":{"source":"spiffe","detail":"d"},"access_points":[{"id":"ide","admits":"yes","reason":"r"}],"access":{"models":[{"kind":"model","name":"m","access_point_id":"ide","state":"granted","reasons":[{"type":"manual-scope","ref":"p"}]}],"tools":[],"skills":[],"scopes_known":true,"scopes_note":"","truncated":false},"quota":{"access_points":[],"balance":{"available":false,"reason":"why"}},"since":"2026-11-08T00:00:00Z","last_seen":null,"usage":null,"usage_by_model":[],"usage_by_tool":[],"notes":[],"known_from":[]}`))
		default:
			_, _ = w.Write([]byte(`{"access_point":{"id":"ide","caller_kinds":["user"],"subjects":{}},"revision":3,"since":"2026-11-08T00:00:00Z","callers":{"caller_kinds":["user"],"open_to_all_of_kind":["user"],"users":[],"agents":[],"clients":[],"groups":[]},"resources":{"models":[],"tools":[],"skills":[]},"qos":null,"quota":{"access_point_id":"ide","coverage":{"state":"not_required","detail":"d"},"allocations":{"budgets":[],"rates":[],"concurrency":[]}},"balance":{"available":false,"reason":"r"},"usage":{"available":true,"truncated":false,"total":null,"by_caller_kind":[],"by_model":[],"by_tool":[]},"notes":[]}`))
		}
	})
	ctx := context.Background()
	page, err := c.ListOverviewSubjects(ctx, OverviewSubjectsQuery{Kind: "user", Since: "7d", Limit: 5, Cursor: "c"})
	if err != nil || len(page.Subjects) != 1 || page.NextCursor != "abc" {
		t.Fatal(page, err)
	}
	u := page.Subjects[0].Usage
	if _, present := u.Tokens["total_tokens"]; present || u.TokensUnknown["total_tokens"] != 2 || u.Cost[0].Amount != "1234" || u.UnknownCostRecords != 1 || page.Subjects[0].LastSeen != nil {
		t.Fatalf("usage = %+v", u)
	}
	if calls[0].query != "cursor=c&kind=user&limit=5&since=7d" {
		t.Fatalf("query = %s", calls[0].query)
	}
	detail, err := c.GetOverviewSubject(ctx, "agent", "tutor", "")
	if err != nil || detail.Access.Models[0].Reasons[0].Type != "manual-scope" || detail.Quota.Balance.Available || detail.Quota.Balance.Reason != "why" || detail.Usage != nil {
		t.Fatal(detail, err)
	}
	if calls[1].path != "/api/v1/overview/subjects/agent/tutor" {
		t.Fatalf("path = %s", calls[1].path)
	}
	if _, err = c.GetOverviewSubject(ctx, "agent", "spiffe://x/tutor", "24h"); err != nil {
		t.Fatal(err)
	}
	if calls[2].path != "/api/v1/overview/subjects/agent" || calls[2].query != "id=spiffe%3A%2F%2Fx%2Ftutor&since=24h" {
		t.Fatalf("slash id call = %+v", calls[2])
	}
	ap, err := c.GetOverviewAccessPoint(ctx, "ide", "")
	if err != nil || ap.Revision != 3 || ap.Quota.Coverage.State != "not_required" {
		t.Fatal(ap, err)
	}
	conn, err := c.ConnectAccessPoint(ctx, "ide", "claude-code")
	if err != nil || conn.BaseURLs["openai"] != "https://g/ap/ide/v1" || conn.Snippets[0].Content != "c" || conn.Auth.TokenEnv != "AIPLEX_TOKEN" {
		t.Fatal(conn, err)
	}
	if calls[4].path != "/api/v1/overview/access-points/ide/connect" || calls[4].query != "client=claude-code" {
		t.Fatalf("connect call = %+v", calls[4])
	}
	if _, err = c.ListOverviewSubjects(ctx, OverviewSubjectsQuery{Kind: "robot"}); err == nil {
		t.Fatal("bad kind accepted")
	}
	if _, err = c.GetOverviewSubject(ctx, "user", "", ""); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err = c.ConnectAccessPoint(ctx, "../x", ""); err == nil {
		t.Fatal("traversal accepted")
	}
}

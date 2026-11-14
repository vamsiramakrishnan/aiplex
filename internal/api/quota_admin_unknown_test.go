package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

func TestQuotaInventoryOlderThanListsUnknownAttemptsWithExposure(t *testing.T) {
	f, _, router, grants := adminHTTPFixture(t)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "native", "{}")
	lease, err := f.host.Prepare(context.Background(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(context.Background(), execution.Result{StatusCode: 200, Err: errors.New("stream truncated before final usage")}); !errors.Is(err, quotaplex.ErrTerminalEvidenceRequired) {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/inventory?kind=attempts&older_than=0s&limit=20", "", "reader", "", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page quotaplex.AdministrationPage
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.OlderThan != "0s" || page.Unknown == nil || page.Unknown.Attempts != 1 || len(page.Records) != 1 || page.Unknown.OldestDispatchAt == nil || len(page.Unknown.Exposure) == 0 {
		t.Fatalf("%s", w.Body.String())
	}
	for _, e := range page.Unknown.Exposure {
		if e.Quantity == nil && e.UncomputedAttempts == 0 && !e.Overflow {
			t.Fatalf("an unknown quantity needs a reason: %+v", e)
		}
	}
	// Younger than the filter: nothing listed, and the summary says none.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/inventory?kind=attempts&older_than=1h", "", "reader", "", nil))
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Unknown.Attempts != 0 || len(page.Records) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Without the filter the inventory is unchanged.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/inventory?kind=attempts", "", "reader", "", nil))
	var plain quotaplex.AdministrationPage
	if err = json.Unmarshal(w.Body.Bytes(), &plain); err != nil || w.Code != 200 || plain.Unknown != nil || plain.OlderThan != "" || len(plain.Records) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Malformed, negative, repeated or misplaced filters are refused like a
	// malformed limit.
	for _, path := range []string{
		"/quota/inventory?kind=attempts&older_than=soon",
		"/quota/inventory?kind=attempts&older_than=",
		"/quota/inventory?kind=attempts&older_than=-1h",
		"/quota/inventory?kind=attempts&older_than=1h&older_than=2h",
		"/quota/inventory?kind=budgets&older_than=1h",
		"/quota/inventory?kind=attempts&older_than=1h&limit=65",
	} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, adminRequest("GET", path, "", "reader", "", nil))
		if w.Code != 400 {
			t.Fatal("refused filter accepted", path, w.Code, w.Body.String())
		}
	}
	// The filter is a read: it needs the current read grant.
	*grants = []string{"aiplex:quota:simulate"}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, adminRequest("GET", "/quota/inventory?kind=attempts&older_than=0s", "", "reader", "", []string{"aiplex:quota:*"}))
	if w.Code != 403 {
		t.Fatal("stale ingress read grant used", w.Code)
	}
}

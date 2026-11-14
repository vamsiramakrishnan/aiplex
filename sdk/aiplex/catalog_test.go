package aiplex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestListCatalog_PageAndPageSizeInQuery(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"templates":[],"total":0,"page":2,"page_size":5}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.ListCatalog(ctx, nil); err != nil || seenQuery != "" {
		t.Fatalf("nil opts must send no query: %v %q", err, seenQuery)
	}
	if _, err := c.ListCatalog(ctx, &aiplex.ListCatalogOpts{}); err != nil || seenQuery != "" {
		t.Fatalf("zero opts must send no query: %v %q", err, seenQuery)
	}
	page, err := c.ListCatalog(ctx, &aiplex.ListCatalogOpts{Plane: "mcplex", Page: 2, PageSize: 5})
	if err != nil || page.Page != 2 || page.PageSize != 5 {
		t.Fatalf("%v %+v", err, page)
	}
	if seenQuery != "page=2&page_size=5&plane=mcplex" {
		t.Errorf("query %q", seenQuery)
	}
}

// A negative page or page size is refused before any request; zero still means
// "not set" and sends nothing.
func TestListCatalog_NegativePageRefused(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"templates":[],"total":0,"page":0,"page_size":20}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	for _, opts := range []*aiplex.ListCatalogOpts{
		{PageSize: -1},
		{Page: -1},
		{Page: -2, PageSize: 5},
	} {
		if _, err := c.ListCatalog(ctx, opts); err == nil {
			t.Errorf("%+v must be refused", *opts)
		}
	}
	if requests != 0 {
		t.Fatalf("refused options must not call the API: %d request(s)", requests)
	}
	if _, err := c.ListCatalog(ctx, &aiplex.ListCatalogOpts{PageSize: 0, Page: 0}); err != nil || requests != 1 {
		t.Fatalf("zero values must call the API with the server default: %v, %d request(s)", err, requests)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// catalogBody answers GET /api/v1/catalog with the given ids as one page.
func catalogBody(total, page, pageSize int, ids ...string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, `{"id":"`+id+`","plane":"mcplex","name":"Name `+id+`","provider":""}`)
	}
	return fmt.Sprintf(`{"templates":[%s],"total":%d,"page":%d,"page_size":%d}`, strings.Join(parts, ","), total, page, pageSize)
}

// catalogMoreHint returns the command after "More: ", or "" when none is printed.
func catalogMoreHint(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, "More: "); ok {
			return rest
		}
	}
	return ""
}

// catalogExitCode is the exit code main would report for err.
func catalogExitCode(err error) int {
	return reportError(io.Discard, catalogCmd(), nil, err)
}

func TestCatalogListPagingHintParsesBack(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(3, 0, 2, "a", "b")})
	out, err := runSP(t, "", catalogCmd(), "list", "--page-size", "2")
	if err != nil {
		t.Fatal(out, err)
	}
	if len(calls) != 1 || calls[0].query != "page_size=2" {
		t.Fatalf("calls %+v", calls)
	}
	more := catalogMoreHint(out)
	if more != "aiplex catalog list --page 1 --page-size 2" {
		t.Fatalf("hint %q\n%s", more, out)
	}
	f := strings.Fields(more)
	calls = nil
	if out, err = runSP(t, "", catalogCmd(), f[2:]...); err != nil {
		t.Fatalf("printed command %q does not parse: %v\n%s", more, err, out)
	}
	if len(calls) != 1 || calls[0].query != "page=1&page_size=2" {
		t.Fatalf("%q -> %+v", more, calls)
	}
}

func TestCatalogListHintKeepsPlaneFilter(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(2, 0, 1, "a")})
	out, err := runSP(t, "", catalogCmd(), "list", "--plane", "llmplex", "--page-size", "1")
	if err != nil {
		t.Fatal(out, err)
	}
	more := catalogMoreHint(out)
	if more != "aiplex catalog list --plane llmplex --page 1 --page-size 1" {
		t.Fatalf("hint %q", more)
	}
	f := strings.Fields(more)
	calls = nil
	if out, err = runSP(t, "", catalogCmd(), f[2:]...); err != nil {
		t.Fatalf("printed command %q does not parse: %v\n%s", more, err, out)
	}
	if len(calls) != 1 || calls[0].query != "page=1&page_size=1&plane=llmplex" {
		t.Fatalf("%q -> %+v", more, calls)
	}
}

func TestCatalogListNoHintOnLastPage(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(3, 1, 2, "c")})
	out, err := runSP(t, "", catalogCmd(), "list", "--page", "1", "--page-size", "2")
	if err != nil || catalogMoreHint(out) != "" {
		t.Fatalf("last page must print no hint: %v\n%s", err, out)
	}
	if !strings.Contains(out, "3 template(s) (page 1)") {
		t.Fatalf("footer:\n%s", out)
	}
}

func TestCatalogListPageFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--page-size=0"},
		{"--page-size=-1"},
		{"--page-size=101"},
		{"--page-size=abc"},
		{"--page=-1"},
		{"--page=x"},
	} {
		var calls []spCall
		spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(0, 0, 20)})
		_, err := runSP(t, "", catalogCmd(), append([]string{"list"}, args...)...)
		if err == nil {
			t.Errorf("%v must be refused", args)
			continue
		}
		if code := catalogExitCode(err); code != 2 {
			t.Errorf("%v: exit code %d, want 2 (%v)", args, code, err)
		}
		if len(calls) != 0 {
			t.Errorf("%v must not call the API: %+v", args, calls)
		}
	}
}

func TestCatalogListStructuredOutput(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(1, 0, 20, "a")})

	output = "json"
	out, err := runSP(t, "", catalogCmd(), "list")
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["total"] != float64(1) {
		t.Fatalf("json: %v\n%s", err, out)
	}

	output = "yaml"
	out, err = runSP(t, "", catalogCmd(), "list")
	if err != nil || !strings.Contains(out, "templates:") || !strings.Contains(out, "total: 1") || strings.Contains(out, "More:") {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
}

func TestCatalogListShowsMissingProviderAsUnknown(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/catalog": catalogBody(1, 0, 20, "a")})
	out, err := runSP(t, "", catalogCmd(), "list")
	if err != nil || !strings.Contains(out, "unknown") {
		t.Fatalf("a template without a provider must show unknown: %v\n%s", err, out)
	}
}

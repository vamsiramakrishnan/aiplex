//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pages and filters the API has and the CLI now reaches: catalog paging, skill
// invocation limits, instance history, usage config and the denials limit.
func TestPagesAndFiltersReachableFromCLIAndAPI(t *testing.T) {
	e := start(t)

	t.Run("catalog paging hint parses back", func(t *testing.T) {
		r := e.mustCLI("catalog", "list", "--page-size", "1", "-o", "json")
		var page struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
			t.Fatalf("catalog json: %v\n%s", err, r.Out)
		}
		if page.Total < 2 {
			t.Fatalf("the stack needs at least two catalog templates to page; got %d", page.Total)
		}
		r = e.mustCLI("catalog", "list", "--page-size", "1")
		var more string
		for _, l := range strings.Split(r.Out, "\n") {
			if rest, ok := strings.CutPrefix(l, "More: "); ok {
				more = rest
			}
		}
		if more != "aiplex catalog list --page 1 --page-size 1" {
			t.Fatalf("paging hint %q\n%s", more, r.Out)
		}
		// The hint starts with "aiplex"; the real root command takes the rest.
		next := e.cli(strings.Fields(more)[1:]...)
		if next.Code != 0 {
			t.Fatalf("printed command failed: %s", next)
		}
		contains(t, next.Out, "(page 1)", "printed catalog page")
	})

	t.Run("bad page flags exit 2", func(t *testing.T) {
		for _, args := range [][]string{
			{"catalog", "list", "--page-size", "0"},
			{"catalog", "list", "--page-size", "101"},
			{"catalog", "list", "--page", "-1"},
			{"skills", "invocations", "--limit", "0"},
			{"skills", "invocations", "--limit", "1001"},
		} {
			if r := e.cli(args...); r.Code != 2 {
				t.Errorf("%v: exit %d, want 2: %s", args, r.Code, r)
			}
		}
	})

	t.Run("skill invocations accept a limit", func(t *testing.T) {
		r := e.mustCLI("skills", "invocations", "--limit", "5", "-o", "json")
		if strings.TrimSpace(r.Out) == "" {
			t.Fatal("empty output")
		}
	})

	t.Run("usage config", func(t *testing.T) {
		r := e.mustCLI("usage", "config")
		contains(t, r.Out, "Trace URL template", "usage config")
		contains(t, r.Out, "Tag keys allowed", "usage config")
		r = e.mustCLI("usage", "config", "-o", "yaml")
		contains(t, r.Out, "feedback:", "usage config yaml")
	})

	t.Run("instance history", func(t *testing.T) {
		r := e.mustCLI("get", "instance", "no-such-instance", "--history")
		contains(t, r.Out, "No history recorded for instance no-such-instance", "instance history")
	})

	t.Run("denials limit over HTTP", func(t *testing.T) {
		if resp := e.do("GET", "/api/v1/dashboard/denials?limit=0", "", e.auth()...); resp.Status != 400 || !strings.Contains(resp.Body, "INVALID_LIMIT") {
			t.Fatalf("limit=0: %d %s", resp.Status, resp.Body)
		}
		resp := e.do("GET", "/api/v1/dashboard/denials?limit=3", "", e.auth()...)
		if resp.Status != 200 {
			t.Fatalf("limit=3: %d %s", resp.Status, resp.Body)
		}
		var denials []json.RawMessage
		if err := json.Unmarshal([]byte(resp.Body), &denials); err != nil || len(denials) > 3 {
			t.Fatalf("limit=3 returned %d denials: %v", len(denials), err)
		}
	})
}

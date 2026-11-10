package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

// pagedUpstream serves tools/list pages per connection:
//
//	three  - three pages (t1,t2 | t3 | t4)
//	loop   - every page returns the same cursor
//	fail2  - page one is fine, page two is an upstream failure
//	endless- a new cursor forever (one tool per page)
//	big    - 1000 tools per page, forever
func pagedUpstream(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		connection := strings.TrimPrefix(r.URL.Path, "/mcp/")
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &message)
		w.Header().Set("Content-Type", "application/json")
		if message.Method != "tools/list" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(message.ID)+`,"result":{"content":[]}}`)
			return
		}
		mu.Lock()
		counts[connection]++
		mu.Unlock()
		page := func(next string, names ...string) {
			result := map[string]any{"tools": []json.RawMessage{}}
			tools := []json.RawMessage{}
			for _, n := range names {
				tools = append(tools, json.RawMessage(`{"name":"`+n+`","inputSchema":{"type":"object"}}`))
			}
			result["tools"] = tools
			if next != "" {
				result["nextCursor"] = next
			}
			out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result})
			_, _ = w.Write(out)
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(message.Params.Cursor, "p"))
		switch connection {
		case "three":
			switch message.Params.Cursor {
			case "":
				page("p1", "t1", "t2")
			case "p1":
				page("p2", "t3")
			default:
				page("", "t4")
			}
		case "loop":
			page("same", "l"+strconv.Itoa(counts[connection]))
		case "fail2":
			if message.Params.Cursor == "" {
				page("p1", "f1")
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "upstream secret stack trace")
		case "endless":
			page("p"+strconv.Itoa(n+1), "e"+strconv.Itoa(n))
		case "big":
			names := make([]string, 1000)
			for i := range names {
				names[i] = fmt.Sprintf("b%d", i)
			}
			page("p"+strconv.Itoa(n+1), names...)
		}
	}))
	t.Cleanup(server.Close)
	return server, func(c string) int { mu.Lock(); defer mu.Unlock(); return counts[c] }
}

var pagedToolNames = func() map[string][]string {
	seq := func(prefix string, from, to int) []string {
		var out []string
		for i := from; i < to; i++ {
			out = append(out, prefix+strconv.Itoa(i))
		}
		return out
	}
	return map[string][]string{"three": seq("t", 1, 5), "loop": seq("l", 1, 30), "fail2": {"f1"}, "endless": seq("e", 0, 30), "big": seq("b", 0, 1000)}
}()

func aggregateFixture(t *testing.T, connections_ ...string) (*apFixture, func(string) int) {
	t.Helper()
	f := newAPFixture(t)
	paged, count := pagedUpstream(t)
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.upstream.URL, paged.URL}, AllowLoopback: true}, Identities: []connections.Identity{
		{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{f.upstream.URL, paged.URL}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Runtime = runtime
	spec := AccessPoint{ID: "paged", DisplayName: "Paged", CallerKinds: []string{CallerUser}}
	scopes := append([]string(nil), apAllScopes...)
	for _, id := range connections_ {
		endpoint := paged.URL + "/mcp/" + id
		allowed := pagedToolNames[id]
		for _, name := range allowed {
			scopes = append(scopes, "mcp:tools:"+name)
		}
		f.put("tool-connection", id, ToolConnection{ID: id, ResourceURL: endpoint, Execution: &toolexec.Connection{ID: id, Endpoint: endpoint, IdentityRef: "host", AllowedTools: allowed, ProtocolVersion: "2025-06-18", Mode: toolexec.StatelessJSON}})
		spec.Tools = append(spec.Tools, AccessPointTool{ConnectionRef: id})
	}
	f.put(AccessPointKind, "paged", spec)
	f.as(apUser("alice", "platform-eng"), scopes...)
	return f, count
}

type aggregateResult struct {
	Result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		NextCursor string                       `json:"nextCursor"`
		Meta       map[string][]json.RawMessage `json:"_meta"`
	} `json:"result"`
}

func listAggregate(t *testing.T, f *apFixture) aggregateResult {
	t.Helper()
	w := mcpPost(f, "/ap/paged/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var out aggregateResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("aggregate list: %d %s", w.Code, w.Body.String())
	}
	return out
}

func names(out aggregateResult) map[string]bool {
	set := map[string]bool{}
	for _, tool := range out.Result.Tools {
		set[tool.Name] = true
	}
	return set
}

func TestAggregateToolsListFollowsUpstreamPagesAndBoundsLoops(t *testing.T) {
	f, count := aggregateFixture(t, "three", "loop", "fail2")
	out := listAggregate(t, f)
	got := names(out)
	for _, want := range []string{"three__t1", "three__t2", "three__t3", "three__t4"} {
		if !got[want] {
			t.Errorf("tool %s on a later upstream page is not discoverable: %v", want, got)
		}
	}
	// Upstream failure on page two: the whole connection is omitted, silently.
	for name := range got {
		if strings.HasPrefix(name, "fail2__") {
			t.Errorf("partially paginated connection leaked %s", name)
		}
	}
	if strings.Contains(fmt.Sprint(out), "secret") {
		t.Error("upstream error leaked")
	}
	// A looping cursor stops after one repeat and keeps what was fetched.
	if c := count("loop"); c != 2 {
		t.Errorf("loop connection fetched %d pages, want 2", c)
	}
	if !got["loop__l1"] || !got["loop__l2"] {
		t.Errorf("loop tools dropped: %v", got)
	}
	if out.Result.NextCursor != "" {
		t.Error("aggregate returned a cursor")
	}
	reported := out.Result.Meta[aggregateTruncationMeta]
	if len(reported) != 1 || !strings.Contains(string(reported[0]), `"connection":"loop"`) || !strings.Contains(string(reported[0]), "cursor_loop") {
		t.Errorf("truncation not reported: %v", out.Result.Meta)
	}
	if count("three") != 3 {
		t.Errorf("three-page connection fetched %d pages", count("three"))
	}
}

func TestAggregateToolsListPageAndToolBounds(t *testing.T) {
	f, count := aggregateFixture(t, "endless", "big")
	out := listAggregate(t, f)
	if c := count("endless"); c != aggregateMaxPagesPerConnection {
		t.Errorf("endless connection fetched %d pages, want %d", c, aggregateMaxPagesPerConnection)
	}
	got := names(out)
	if !got["endless__e0"] || !got[fmt.Sprintf("endless__e%d", aggregateMaxPagesPerConnection-1)] {
		t.Errorf("fetched pages were dropped on truncation: %d tools", len(got))
	}
	reasons := ""
	for _, m := range out.Result.Meta[aggregateTruncationMeta] {
		reasons += string(m)
	}
	if !strings.Contains(reasons, "page_limit") {
		t.Errorf("page bound not reported: %s", reasons)
	}
	// The tool bound is checked between pages: "big" stops once the total
	// reaches the cap, and keeps everything it fetched.
	if n := len(out.Result.Tools); n < aggregateMaxTools || n > aggregateMaxTools+1000 {
		t.Errorf("tool total %d outside [%d, %d]", n, aggregateMaxTools, aggregateMaxTools+1000)
	}
	if !strings.Contains(reasons, "tool_limit") {
		t.Errorf("tool bound not reported: %s", reasons)
	}
}

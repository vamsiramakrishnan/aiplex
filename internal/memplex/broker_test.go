package memplex

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

func newTestBroker(t *testing.T) *Broker {
	t.Helper()
	return NewBroker(NewLocalBackend())
}

func TestBroker_WriteReadDelete(t *testing.T) {
	b := newTestBroker(t)
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "students/profile", "v1")
	b.Register(Namespace{URI: uri, Backend: "local", DataClass: "internal"}, nil)

	body := bytes.NewBufferString(`{"data":{"name":"alice","grade":7}}`)
	req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/students/profile@v1/alice-key", body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/cap/memory/students/profile@v1/alice-key")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET: status=%v err=%v", resp.StatusCode, err)
	}
	var got Value
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Data["name"] != "alice" {
		t.Errorf("Data = %+v", got.Data)
	}

	req, _ = http.NewRequest("DELETE", srv.URL+"/cap/memory/students/profile@v1/alice-key", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("DELETE status = %d", resp.StatusCode)
	}

	resp, _ = http.Get(srv.URL + "/cap/memory/students/profile@v1/alice-key")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestBroker_Search(t *testing.T) {
	b := newTestBroker(t)
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "notes", "v1")
	b.Register(Namespace{URI: uri, Backend: "local"}, nil)

	for _, kv := range []struct {
		key string
		v   Value
	}{
		{"k1", Value{Data: map[string]any{"text": "ball follows parabolic path"}, Embedding: []float32{1, 0, 0}}},
		{"k2", Value{Data: map[string]any{"text": "circle is round"}, Embedding: []float32{0, 1, 0}}},
		{"k3", Value{Data: map[string]any{"text": "another parabolic note"}, Embedding: []float32{0.95, 0.1, 0}}},
	} {
		body, _ := json.Marshal(kv.v)
		req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/notes@v1/"+kv.key, bytes.NewReader(body))
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != 204 {
			t.Fatalf("write %s: status=%d", kv.key, resp.StatusCode)
		}
	}

	q := Query{Embedding: []float32{1, 0, 0}, TopK: 2}
	body, _ := json.Marshal(q)
	resp, err := http.Post(srv.URL+"/cap/memory/notes@v1/_search", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("search: status=%v err=%v", resp.StatusCode, err)
	}
	var out struct {
		Hits []Hit `json:"hits"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(out.Hits))
	}
	// Most-similar (k1 with embedding [1,0,0]) should rank first.
	if out.Hits[0].Key != "k1" {
		t.Errorf("top hit = %s, want k1", out.Hits[0].Key)
	}
}

func TestBroker_PIIRedact(t *testing.T) {
	b := NewBroker(NewLocalBackend())
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "students/profile", "v1")
	b.Register(Namespace{
		URI:       uri,
		Backend:   "local",
		DataClass: "pii",
		PII: &PIIPolicy{
			Enabled: true,
			Rules: []PIIRule{
				{Field: "ssn", Action: "reject"},
				{Field: "email", Action: "hash"},
			},
		},
	}, nil)

	// reject path
	body := bytes.NewBufferString(`{"data":{"ssn":"123-45-6789","name":"alice"}}`)
	req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/students/profile@v1/k1", body)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("expected 403 PII_REJECTED, got %d", resp.StatusCode)
	}

	// hash path
	body = bytes.NewBufferString(`{"data":{"email":"alice@school.edu","name":"alice"}}`)
	req, _ = http.NewRequest("PUT", srv.URL+"/cap/memory/students/profile@v1/k2", body)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("write hash case: status=%d", resp.StatusCode)
	}

	resp, _ = http.Get(srv.URL + "/cap/memory/students/profile@v1/k2")
	var got Value
	json.NewDecoder(resp.Body).Decode(&got)
	if email, _ := got.Data["email"].(string); email == "alice@school.edu" {
		t.Errorf("email was not hashed: %v", email)
	}
}

func TestBroker_List(t *testing.T) {
	b := newTestBroker(t)
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "lessons", "v1")
	b.Register(Namespace{URI: uri, Backend: "local"}, nil)

	for _, k := range []string{"lesson-1", "lesson-2", "lesson-3", "other"} {
		body := bytes.NewBufferString(`{"data":{}}`)
		req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/lessons@v1/"+k, body)
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != 204 {
			t.Fatalf("write %s: status=%d", k, resp.StatusCode)
		}
	}

	resp, err := http.Get(srv.URL + "/cap/memory/lessons@v1/?prefix=lesson-")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("list: status=%v err=%v", resp.StatusCode, err)
	}
	var out Listing
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Keys) != 3 {
		t.Errorf("expected 3 lesson- keys, got %v", out.Keys)
	}
}

func TestBroker_TTL(t *testing.T) {
	b := newTestBroker(t)
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "scratch", "v1")
	b.Register(Namespace{URI: uri, Backend: "local"}, nil)

	body := bytes.NewBufferString(`{"data":{"x":1}}`)
	req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/scratch@v1/k", body)
	req.Header.Set("X-AIPlex-TTL-Seconds", "1")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("write: %d", resp.StatusCode)
	}

	// Immediately readable.
	resp, _ = http.Get(srv.URL + "/cap/memory/scratch@v1/k")
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 just after write, got %d", resp.StatusCode)
	}

	time.Sleep(1100 * time.Millisecond)
	resp, _ = http.Get(srv.URL + "/cap/memory/scratch@v1/k")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after TTL expiry, got %d", resp.StatusCode)
	}
}

func TestBroker_IfNoneMatch(t *testing.T) {
	b := newTestBroker(t)
	srv := httptest.NewServer(b)
	defer srv.Close()

	uri := capability.New(capability.KindMemory, "uniq", "v1")
	b.Register(Namespace{URI: uri, Backend: "local"}, nil)

	body1 := bytes.NewBufferString(`{"data":{"v":1}}`)
	req, _ := http.NewRequest("PUT", srv.URL+"/cap/memory/uniq@v1/key", body1)
	req.Header.Set("If-None-Match", "*")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("first write: %d", resp.StatusCode)
	}

	body2 := bytes.NewBufferString(`{"data":{"v":2}}`)
	req, _ = http.NewRequest("PUT", srv.URL+"/cap/memory/uniq@v1/key", body2)
	req.Header.Set("If-None-Match", "*")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 409 {
		t.Errorf("expected 409 conflict on second CAS write, got %d", resp.StatusCode)
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		in       string
		wantURI  string
		wantAct  string
		wantKey  string
		wantErr  bool
	}{
		{"/cap/memory/foo@v1/bar", "cap://memory/foo@v1", "", "bar", false},
		{"/cap/memory/students/alice/profile@v1/k", "cap://memory/students/alice/profile@v1", "", "k", false},
		{"/cap/memory/foo@v1/_search", "cap://memory/foo@v1", "search", "", false},
		{"/cap/memory/foo@v1/_subscribe", "cap://memory/foo@v1", "subscribe", "", false},
		{"/cap/memory/foo@v1/", "cap://memory/foo@v1", "list", "", false},
		{"/wrong/path", "", "", "", true},
		{"/cap/memory/foo", "", "", "", true},
	}
	for _, c := range cases {
		u, act, key, err := parsePath(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePath(%q): want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePath(%q): unexpected err: %v", c.in, err)
			continue
		}
		if u.String() != c.wantURI || act != c.wantAct || key != c.wantKey {
			t.Errorf("parsePath(%q) = (%s, %s, %s), want (%s, %s, %s)",
				c.in, u, act, key, c.wantURI, c.wantAct, c.wantKey)
		}
	}
}

func TestLocalBackend_DirectAPI(t *testing.T) {
	ctx := context.Background()
	b := NewLocalBackend()
	uri := capability.New(capability.KindMemory, "x", "v1")
	ns := Namespace{URI: uri}

	if err := b.Write(ctx, ns, "k", Value{Data: map[string]any{"hello": "world"}}, WriteOpts{}); err != nil {
		t.Fatal(err)
	}
	v, err := b.Read(ctx, ns, "k")
	if err != nil {
		t.Fatal(err)
	}
	if v.Data["hello"] != "world" {
		t.Errorf("got %v", v.Data)
	}

	// Subscribe then write triggers an event.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, _ := b.Subscribe(cctx, ns, "")
	go func() {
		_ = b.Write(ctx, ns, "k2", Value{Data: map[string]any{"x": 1}}, WriteOpts{})
	}()
	select {
	case ev := <-ch:
		if ev.Op != "put" || ev.Key != "k2" {
			t.Errorf("unexpected change: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Error("expected change event within 1s")
	}
}

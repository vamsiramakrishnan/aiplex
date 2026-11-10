package deploy

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeAPIServer is a minimal Kubernetes API: discovery for a core group and
// the Gateway API group, and recording of apply (PATCH) and DELETE calls.
type fakeAPIServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
	existing map[string]bool // object paths that exist (DELETE 404s otherwise)
	failWith map[string]int  // path -> forced status
}

type recorded struct {
	Method, Path, Query, ContentType, Auth, Body string
}

func newFakeAPIServer(t *testing.T, tls bool) *fakeAPIServer {
	t.Helper()
	f := &fakeAPIServer{existing: map[string]bool{}, failWith: map[string]int{}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), string(b)})
		code := f.failWith[r.URL.Path]
		exists := f.existing[r.URL.Path]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1":
			io.WriteString(w, `{"resources":[{"name":"services","kind":"Service","namespaced":true},{"name":"services/status","kind":"Service","namespaced":true},{"name":"namespaces","kind":"Namespace","namespaced":false}]}`)
		case r.URL.Path == "/apis/gateway.networking.k8s.io/v1":
			io.WriteString(w, `{"resources":[{"name":"httproutes","kind":"HTTPRoute","namespaced":true}]}`)
		case strings.HasPrefix(r.URL.Path, "/apis/") && strings.Count(r.URL.Path, "/") == 3:
			w.WriteHeader(404)
			io.WriteString(w, `{"kind":"Status","message":"not found"}`)
		case code != 0:
			w.WriteHeader(code)
			io.WriteString(w, `{"kind":"Status","message":"boom: denied"}`)
		case r.Method == http.MethodDelete && !exists:
			w.WriteHeader(404)
			io.WriteString(w, `{"kind":"Status","message":"not found"}`)
		default:
			io.WriteString(w, `{}`)
		}
	})
	if tls {
		f.Server = httptest.NewTLSServer(h)
	} else {
		f.Server = httptest.NewServer(h)
	}
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPIServer) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeAPIServer) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.Method == method {
			n++
		}
	}
	return n
}

func liveClientFor(f *fakeAPIServer) *LiveK8sClient {
	return NewLiveK8sClientConfigured(WithAPIServer(f.URL, f.Client(), "tok", "default"))
}

func TestLiveCleanupUsesInventoryAPIGroupAndRejectsBadIdentities(t *testing.T) {
	f := newFakeAPIServer(t, false)
	f.existing["/apis/gateway.networking.k8s.io/v1/namespaces/mcplex/httproutes/owned"] = true
	client := liveClientFor(f)
	if err := client.Delete(context.Background(), "gateway.networking.k8s.io/v1", "HTTPRoute", "owned", "mcplex"); err != nil {
		t.Fatal(err)
	}
	r := f.last()
	if r.Method != http.MethodDelete || r.Path != "/apis/gateway.networking.k8s.io/v1/namespaces/mcplex/httproutes/owned" || r.Auth != "Bearer tok" {
		t.Fatalf("cleanup discarded recorded group or auth: %+v", r)
	}
	before := len(f.requests)
	for _, resource := range []struct{ version, kind, name, namespace string }{
		{"v1", "Secret", "--all", "mcplex"},
		{"other/v1/extra", "HTTPRoute", "owned", "mcplex"},
		{"v1", "--all", "owned", "mcplex"},
		{"v1", "Secret", "owned", "--all-namespaces"},
		{"v1", "Secret", "../x", "mcplex"},
	} {
		if err := client.Delete(context.Background(), resource.version, resource.kind, resource.name, resource.namespace); err == nil {
			t.Fatalf("invalid inventory reached the API: %+v", resource)
		}
	}
	if len(f.requests) != before {
		t.Fatal("invalid cleanup inventory sent requests")
	}
	// Core resources use /api/v1; a missing object is not an error.
	if err := client.Delete(context.Background(), "v1", "Service", "gone", "mcplex"); err != nil {
		t.Fatalf("missing object should be ignored: %v", err)
	}
	if got := f.last().Path; got != "/api/v1/namespaces/mcplex/services/gone" {
		t.Fatalf("core delete path = %s", got)
	}
}

func TestLiveApplyIsServerSideApplyWithFixedManager(t *testing.T) {
	f := newFakeAPIServer(t, false)
	client := liveClientFor(f)
	m := Manifest{APIVersion: "v1", Kind: "Service", Name: "svc", Namespace: "mcplex", YAML: "spec:\n  ports:\n  - port: 80\n"}
	if err := client.Apply(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	r := f.last()
	if r.Method != http.MethodPatch || r.Path != "/api/v1/namespaces/mcplex/services/svc" {
		t.Fatalf("apply request = %+v", r)
	}
	if r.ContentType != "application/apply-patch+yaml" || r.Query != "fieldManager=aiplex-deploy&force=true" {
		t.Fatalf("not server-side apply with a fixed manager: %+v", r)
	}
	if !strings.Contains(r.Body, "kind: Service") || !strings.Contains(r.Body, "port: 80") {
		t.Fatalf("body = %q", r.Body)
	}
	// Discovery is cached: a second apply does not rediscover.
	n := f.count(http.MethodGet)
	if err := client.Apply(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if f.count(http.MethodGet) != n {
		t.Fatal("discovery was repeated")
	}
}

func TestLiveApplyErrorsNameTheObjectAndNeverEchoTheBody(t *testing.T) {
	f := newFakeAPIServer(t, false)
	f.failWith["/api/v1/namespaces/mcplex/services/svc"] = 403
	client := liveClientFor(f)
	m := Manifest{APIVersion: "v1", Kind: "Service", Name: "svc", Namespace: "mcplex", YAML: "metadata:\n  annotations:\n    note: resolved-secret-value\n"}
	err := client.Apply(context.Background(), m)
	if err == nil {
		t.Fatal("forbidden apply succeeded")
	}
	for _, want := range []string{"Service/svc", "mcplex", "403", "boom: denied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "resolved-secret-value") {
		t.Fatalf("error leaked the manifest body: %v", err)
	}
	// A kind the cluster does not serve and a cluster-scoped kind are refused.
	if err := client.Apply(context.Background(), Manifest{APIVersion: "v1", Kind: "Nope", Name: "x", Namespace: "mcplex"}); err == nil {
		t.Fatal("unknown kind applied")
	}
	if err := client.Apply(context.Background(), Manifest{APIVersion: "v1", Kind: "Namespace", Name: "x", Namespace: "mcplex"}); err == nil || !strings.Contains(err.Error(), "cluster-scoped") {
		t.Fatalf("cluster-scoped kind should be refused: %v", err)
	}
	if err := client.Apply(context.Background(), Manifest{APIVersion: "example.com/v1", Kind: "Widget", Name: "x", Namespace: "mcplex"}); err == nil || !strings.Contains(err.Error(), "CRD") {
		t.Fatalf("absent API group should mention the CRD: %v", err)
	}
}

func TestLiveApplyEmptyNamespaceUsesConnectionDefault(t *testing.T) {
	f := newFakeAPIServer(t, false)
	client := liveClientFor(f)
	if err := client.Apply(context.Background(), Manifest{APIVersion: "v1", Kind: "Service", Name: "svc"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last().Path; got != "/api/v1/namespaces/default/services/svc" {
		t.Fatalf("path = %s", got)
	}
}

func TestLiveClientDryRunNeverConnects(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))
	client := NewLiveK8sClientConfigured(WithDryRun(true))
	if err := client.Apply(context.Background(), Manifest{APIVersion: "v1", Kind: "Service", Name: "svc", Namespace: "mcplex"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), "v1", "Service", "svc", "mcplex"); err != nil {
		t.Fatal(err)
	}
}

func writeKubeconfig(t *testing.T, f *fakeAPIServer, user string) string {
	t.Helper()
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.Certificate().Raw}))
	cfg := "current-context: c\ncontexts:\n- name: c\n  context: {cluster: k, user: u, namespace: team}\n" +
		"clusters:\n- name: k\n  cluster: {server: " + f.URL + ", certificate-authority-data: " + ca + "}\n" +
		"users:\n- name: u\n  user:\n" + user
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLiveClientKubeconfigFallback(t *testing.T) {
	f := newFakeAPIServer(t, true)
	path := writeKubeconfig(t, f, "    token: kube-token\n")
	client := NewLiveK8sClientConfigured(WithKubeconfig(path))
	if err := client.Apply(context.Background(), Manifest{APIVersion: "v1", Kind: "Service", Name: "svc"}); err != nil {
		t.Fatal(err)
	}
	r := f.last()
	if r.Auth != "Bearer kube-token" || r.Path != "/api/v1/namespaces/team/services/svc" {
		t.Fatalf("kubeconfig connection not used: %+v", r)
	}
}

func TestLiveClientKubeconfigRefusesHelpersAndInsecureTLS(t *testing.T) {
	f := newFakeAPIServer(t, true)
	for name, user := range map[string]string{
		"exec":        "    exec: {command: gke-gcloud-auth-plugin}\n",
		"no creds":    "    {}\n",
		"basic":       "    username: a\n    password: b\n",
		"impersonate": "    token: t\n    as: someone\n",
	} {
		path := writeKubeconfig(t, f, user)
		if _, err := loadKubeconfigConn(path); err == nil {
			t.Errorf("%s: kubeconfig accepted", name)
		}
	}
	path := writeKubeconfig(t, f, "    token: t\n")
	b, _ := os.ReadFile(path)
	insecure := strings.Replace(string(b), "certificate-authority-data", "insecure-skip-tls-verify: true, certificate-authority-data", 1)
	os.WriteFile(path, []byte(insecure), 0o600)
	if _, err := loadKubeconfigConn(path); err == nil || !strings.Contains(err.Error(), "TLS verification") {
		t.Fatalf("insecure TLS accepted: %v", err)
	}
}

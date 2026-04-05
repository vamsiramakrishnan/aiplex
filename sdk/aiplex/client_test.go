package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestClient_Health(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClient_BearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", auth)
		}
		json.NewEncoder(w).Encode([]aiplex.Instance{})
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	c.SetToken("test-token")
	_, err := c.ListInstances(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClient_ListInstances(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/instances" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		plane := r.URL.Query().Get("plane")
		if plane != "mcplex" {
			t.Errorf("expected plane=mcplex, got %s", plane)
		}
		json.NewEncoder(w).Encode([]aiplex.Instance{
			{ID: "inst-1", Plane: "mcplex", Status: "running"},
			{ID: "inst-2", Plane: "mcplex", Status: "stopped"},
		})
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	list, err := c.ListInstances(context.Background(), &aiplex.ListInstancesOpts{Plane: "mcplex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 instances, got %d", len(list))
	}
	if list[0].ID != "inst-1" {
		t.Errorf("expected inst-1, got %s", list[0].ID)
	}
}

func TestClient_Deploy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req aiplex.DeployRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Plane != "mcplex" {
			t.Errorf("expected mcplex, got %s", req.Plane)
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(aiplex.Instance{
			ID: "kb-xyz", Plane: "mcplex", Status: "running",
		})
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	inst, err := c.Deploy(context.Background(), &aiplex.DeployRequest{
		Plane:      "mcplex",
		TemplateID: "kb-search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inst.ID != "kb-xyz" {
		t.Errorf("expected kb-xyz, got %s", inst.ID)
	}
}

func TestClient_ErrorHandling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{
			"code":    "not_found",
			"message": "instance not found",
		})
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	_, err := c.GetInstance(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*aiplex.Error)
	if !ok {
		t.Fatalf("expected *aiplex.Error, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("expected 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("expected not_found, got %s", apiErr.Code)
	}
}

func TestClient_GetDashboardStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(aiplex.DashboardStats{
			TotalInstances:   5,
			RunningInstances: 3,
			DailyCostUSD:     1.25,
		})
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	stats, err := c.GetDashboardStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalInstances != 5 {
		t.Errorf("expected 5, got %d", stats.TotalInstances)
	}
	if stats.DailyCostUSD != 1.25 {
		t.Errorf("expected 1.25, got %f", stats.DailyCostUSD)
	}
}

func TestClient_LLMRoutes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode([]aiplex.LLMRouteConfig{
				{ModelID: "gemini-2.5-flash", Backends: []aiplex.LLMBackend{
					{Provider: "google", Weight: 100, Enabled: true},
				}},
			})
		case "PUT":
			var rc aiplex.LLMRouteConfig
			json.NewDecoder(r.Body).Decode(&rc)
			json.NewEncoder(w).Encode(rc)
		}
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	routes, err := c.ListLLMRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(routes))
	}
}

func TestClient_Delegations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(aiplex.Delegation{
				ID: "del-001", Status: "pending",
			})
		case r.Method == "GET":
			json.NewEncoder(w).Encode([]aiplex.Delegation{
				{ID: "del-001", Status: "completed"},
			})
		}
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	d, err := c.RecordDelegation(context.Background(), &aiplex.RecordDelegationRequest{
		ID:            "del-001",
		CallerAgentID: "tutor",
		CalleeAgentID: "research",
		TaskType:      "research",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "pending" {
		t.Errorf("expected pending, got %s", d.Status)
	}
}

func TestClient_Agents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(aiplex.Agent{
				ClientID: "tutor-agent", Status: "active",
			})
		case "GET":
			json.NewEncoder(w).Encode([]aiplex.Agent{
				{ClientID: "tutor-agent", Status: "active"},
			})
		}
	}))
	defer srv.Close()

	c := aiplex.NewClient(srv.URL)
	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

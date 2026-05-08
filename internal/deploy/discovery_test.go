package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestDiscoverTools_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tools/list" {
			t.Errorf("expected tools/list, got %v", req["method"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "search_docs", "description": "Search documentation"},
					{"name": "get_file", "description": "Read a file"},
				},
			},
		})
	}))
	defer srv.Close()

	tools, err := DiscoverTools(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "search_docs" {
		t.Errorf("tools[0].Name = %q, want search_docs", tools[0].Name)
	}
}

func TestDiscoverTools_ServerDown(t *testing.T) {
	_, err := DiscoverTools(context.Background(), "http://localhost:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestDiscoverAgentCard_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/agent.json" {
			t.Errorf("expected /.well-known/agent.json, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"name":        "research-agent",
			"description": "Research delegation agent",
			"url":         "/a2a/research-agent",
			"version":     "1.0.0",
			"task_types": []map[string]any{
				{"type": "web_search", "description": "Search the public web"},
				{"type": "summarize", "description": "Summarize a document"},
			},
			"auth_schemes": []map[string]any{
				{"scheme": "bearer"},
			},
		})
	}))
	defer srv.Close()

	card, err := DiscoverAgentCard(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverAgentCard: %v", err)
	}
	if card.Name != "research-agent" {
		t.Errorf("Name = %q, want research-agent", card.Name)
	}
	if len(card.TaskTypes) != 2 {
		t.Fatalf("got %d task types, want 2", len(card.TaskTypes))
	}
	if card.TaskTypes[0].Type != "web_search" {
		t.Errorf("TaskTypes[0].Type = %q, want web_search", card.TaskTypes[0].Type)
	}
}

func TestDiscoverAgentCard_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := DiscoverAgentCard(context.Background(), srv.URL)
	if !errors.Is(err, ErrAgentCardNotFound) {
		t.Fatalf("expected ErrAgentCardNotFound, got %v", err)
	}
}

func TestDiscoverAgentCard_InvalidCard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name":       "",
			"task_types": []map[string]any{{"type": "x"}},
		})
	}))
	defer srv.Close()

	_, err := DiscoverAgentCard(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateAgentCard(t *testing.T) {
	cases := []struct {
		name    string
		card    *models.AgentCard
		wantErr bool
	}{
		{"nil", nil, true},
		{"empty name", &models.AgentCard{TaskTypes: []models.TaskTypeInfo{{Type: "x"}}}, true},
		{"no task types", &models.AgentCard{Name: "ok"}, true},
		{"empty task type", &models.AgentCard{Name: "ok", TaskTypes: []models.TaskTypeInfo{{Type: ""}}}, true},
		{"duplicate task types", &models.AgentCard{
			Name: "ok", TaskTypes: []models.TaskTypeInfo{{Type: "a"}, {Type: "a"}},
		}, true},
		{"valid", &models.AgentCard{
			Name: "ok", TaskTypes: []models.TaskTypeInfo{{Type: "research"}, {Type: "summarize"}},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgentCard(tc.card)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestDiscoverTasks_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "tasks/list" {
			t.Errorf("expected tasks/list, got %v", req["method"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"tasks": []map[string]any{
					{"type": "research"},
					{"type": "summarize"},
				},
			},
		})
	}))
	defer srv.Close()

	tasks, err := DiscoverTasks(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverTasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0] != "research" {
		t.Fatalf("got %v, want [research summarize]", tasks)
	}
}

func TestDiscoverAgentCard_ServerDown(t *testing.T) {
	_, err := DiscoverAgentCard(context.Background(), "http://localhost:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestDiscoverTools_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  map[string]any{"tools": []any{}},
		})
	}))
	defer srv.Close()

	tools, err := DiscoverTools(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("got %d tools, want 0", len(tools))
	}
}

package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	if err == nil {
		t.Fatal("expected error for 404 response")
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

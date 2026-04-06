# Core Flows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MCPlex tool discovery and LLMPlex route application actually work — so deployed MCP servers report real tools and LLM route config changes propagate to Envoy gateway CRDs.

**Architecture:** Tool discovery calls the running MCP server's `tools/list` endpoint after deploy. LLM route updates regenerate Envoy LLMRoute + AIServiceBackend CRDs and apply them via the existing K8s client. Both integrate into the existing deploy engine.

**Tech Stack:** Go 1.24, MCP JSON-RPC, K8s server-side apply (kubectl)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/deploy/discovery.go` | Create | MCP tool discovery (call tools/list on running server) |
| `internal/deploy/discovery_test.go` | Create | Tests for tool discovery |
| `internal/deploy/engine.go` | Modify | Call discovery after K8s apply, use real tools for scopes |
| `internal/api/llm.go` | Modify | Regenerate + apply CRDs on route config change |
| `internal/deploy/routes.go` | Modify | Generate LLMRoute with real backend weights from config |
| `internal/deploy/routes_test.go` | Create | Test LLMRoute generation with weights |

---

### Task 1: MCP tool discovery

**Files:**
- Create: `internal/deploy/discovery.go`
- Create: `internal/deploy/discovery_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/deploy/discovery_test.go`:

```go
package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverTools_Success(t *testing.T) {
	// Mock MCP server that responds to tools/list
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/deploy/ -run TestDiscoverTools -v
```

Expected: FAIL — `DiscoverTools` not defined.

- [ ] **Step 3: Implement discovery**

Create `internal/deploy/discovery.go`:

```go
package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DiscoveredTool represents a tool found on a running MCP server.
type DiscoveredTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DiscoverTools calls the MCP tools/list method on a running server
// and returns the discovered tools.
func DiscoverTools(ctx context.Context, endpoint string) ([]DiscoveredTool, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tools/list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tools/list returned status %d", resp.StatusCode)
	}

	var rpcResp struct {
		Result struct {
			Tools []DiscoveredTool `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode tools/list response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("tools/list error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result.Tools, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/deploy/ -run TestDiscoverTools -v
```

Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/discovery.go internal/deploy/discovery_test.go
git commit -m "feat: add MCP tool discovery — calls tools/list on running servers"
```

---

### Task 2: Integrate tool discovery into deploy engine

**Files:**
- Modify: `internal/deploy/engine.go`

- [ ] **Step 1: Read engine.go**

The current deploy flow (lines 43-144) determines scopes from template metadata at step 4 (lines 68-83). We need to add a discovery step AFTER K8s apply (step 6) that calls the running server and updates scopes with real tools.

- [ ] **Step 2: Add discovery call after K8s apply**

Find the section after K8s manifests are applied (around line 115) and before route CRD application (around line 117). Add:

```go
	// 6b. Discover actual tools/tasks from running server (MCPlex/A2APlex only)
	if plane != "llmplex" {
		serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", instanceID, namespace)
		discovered, err := DiscoverTools(ctx, serviceURL)
		if err != nil {
			log.Warn().Err(err).Str("instance", instanceID).Msg("tool discovery failed — using template scopes")
		} else if len(discovered) > 0 {
			// Replace template-derived scopes with discovered scopes
			var prefix string
			switch plane {
			case "mcplex":
				prefix = "mcp:tools:"
			case "a2aplex":
				prefix = "a2a:task:"
			}
			discoveredScopes := make([]string, len(discovered))
			for i, tool := range discovered {
				discoveredScopes[i] = prefix + tool.Name
			}
			instance.Scopes = discoveredScopes
			log.Info().Int("tools", len(discovered)).Str("instance", instanceID).Msg("discovered tools from running server")
		}
	}
```

This is **non-fatal** — if discovery fails (server not ready yet, network issue), it falls back to template-defined scopes. This is the correct behavior for a deploy engine.

- [ ] **Step 3: Build and run existing tests**

```bash
go build ./... && go test ./internal/deploy/ -v
```

Existing deploy tests use NoOpK8sClient, so discovery will fail (no running server) and fall back to template scopes. Tests should still pass.

- [ ] **Step 4: Commit**

```bash
git add internal/deploy/engine.go
git commit -m "feat: call tools/list on deployed MCP servers to discover real scopes"
```

---

### Task 3: Generate LLMRoute CRDs with real backend weights

**Files:**
- Modify: `internal/deploy/routes.go`
- Create: `internal/deploy/routes_test.go` (or add to existing)

- [ ] **Step 1: Write test for weighted LLMRoute generation**

Add to route tests:

```go
func TestGenerateRoutesFromConfig(t *testing.T) {
	config := &models.LLMRouteConfig{
		ModelID: "gemini-2.5-flash",
		Backends: []models.LLMBackend{
			{Provider: "google", ModelID: "gemini-2.5-flash", Weight: 80, Enabled: true},
			{Provider: "anthropic", ModelID: "claude-sonnet-4", Weight: 20, Enabled: true},
		},
		Fallbacks: []string{"gpt-4.1-mini"},
	}

	manifests := GenerateRoutesFromConfig(config, "aiplex-gateway")

	if len(manifests) < 2 {
		t.Fatalf("expected at least 2 manifests (LLMRoute + backends), got %d", len(manifests))
	}

	// Verify LLMRoute YAML contains both backends
	routeYAML := manifests[0].YAML
	if !strings.Contains(routeYAML, "weight: 80") {
		t.Error("LLMRoute should contain weight: 80")
	}
	if !strings.Contains(routeYAML, "weight: 20") {
		t.Error("LLMRoute should contain weight: 20")
	}
}
```

- [ ] **Step 2: Implement GenerateRoutesFromConfig**

Add to `internal/deploy/routes.go`:

```go
// GenerateRoutesFromConfig creates Envoy LLMRoute + AIServiceBackend manifests
// from a route configuration with weighted backends and fallbacks.
func GenerateRoutesFromConfig(config *models.LLMRouteConfig, gatewayName string) []Manifest {
	var manifests []Manifest

	// Generate AIServiceBackend for each enabled backend
	var backendRefs []string
	for _, backend := range config.Backends {
		if !backend.Enabled {
			continue
		}
		backendName := fmt.Sprintf("%s-%s-backend", config.ModelID, backend.Provider)

		backendYAML := fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: %s
  namespace: aiplex-system
spec:
  provider: %s
  model: %s
  apiKey:
    secretRef:
      name: %s-api-key`, backendName, backend.Provider, backend.ModelID, backend.Provider)

		manifests = append(manifests, Manifest{
			Kind:      "AIServiceBackend",
			Name:      backendName,
			Namespace: "aiplex-system",
			YAML:      backendYAML,
		})

		backendRefs = append(backendRefs, fmt.Sprintf("    - name: %s\n      weight: %d", backendName, backend.Weight))
	}

	// Generate LLMRoute with weighted backend refs
	routeYAML := fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-%s
  namespace: aiplex-system
spec:
  parentRefs:
    - name: %s
  rules:
    - matches:
        - headers:
            - name: x-model-id
              value: %s
      backendRefs:
%s`, config.ModelID, gatewayName, config.ModelID, strings.Join(backendRefs, "\n"))

	// Add fallback section if configured
	if len(config.Fallbacks) > 0 {
		var fallbackLines []string
		for _, fb := range config.Fallbacks {
			fallbackLines = append(fallbackLines, fmt.Sprintf("        - name: %s-backend", fb))
		}
		routeYAML += fmt.Sprintf("\n      fallback:\n%s", strings.Join(fallbackLines, "\n"))
	}

	manifests = append([]Manifest{{
		Kind:      "LLMRoute",
		Name:      "llm-" + config.ModelID,
		Namespace: "aiplex-system",
		YAML:      routeYAML,
	}}, manifests...)

	return manifests
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/deploy/ -run TestGenerateRoutes -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/deploy/routes.go internal/deploy/routes_test.go
git commit -m "feat: generate LLMRoute CRDs with real backend weights and fallbacks"
```

---

### Task 4: Apply LLMRoute CRDs on route config change

**Files:**
- Modify: `internal/api/llm.go`

- [ ] **Step 1: Read llm.go**

Find the PutRouteConfig handler (lines 52-82). Line 79 has the TODO.

- [ ] **Step 2: Add K8s client to LLMHandler**

The LLMHandler currently only has a `store` field. Add a K8s client and gateway name:

```go
type LLMHandler struct {
	store       registry.Store
	k8s         deploy.K8sClient
	gatewayName string
}

func NewLLMHandler(store registry.Store, k8s deploy.K8sClient, gatewayName string) *LLMHandler {
	return &LLMHandler{store: store, k8s: k8s, gatewayName: gatewayName}
}
```

- [ ] **Step 3: Replace the TODO with CRD generation + apply**

In PutRouteConfig, after `h.store.PutRouteConfig(ctx, &rc)`, replace the TODO line with:

```go
	// Generate and apply Envoy LLMRoute + AIServiceBackend CRDs
	manifests := deploy.GenerateRoutesFromConfig(&rc, h.gatewayName)
	for _, m := range manifests {
		if err := h.k8s.Apply(r.Context(), m); err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Str("kind", m.Kind).Str("name", m.Name).
				Msg("failed to apply LLM route CRD")
		}
	}
```

- [ ] **Step 4: Update NewLLMHandler callsite in main.go**

Read `cmd/aiplex-api/main.go`, find where `NewLLMHandler` is called, and pass the K8s client and gateway name:

```go
llmHandler := api.NewLLMHandler(store, k8sClient, cfg.GatewayName)
```

If `cfg.GatewayName` doesn't exist, use `os.Getenv("GATEWAY_NAME")` with a default of `"aiplex-gateway"`.

- [ ] **Step 5: Also apply CRDs on delete**

In DeleteRouteConfig, after deleting from store, delete the CRDs:

```go
	// Delete Envoy CRDs for this route
	h.k8s.Delete(r.Context(), deploy.Manifest{
		Kind:      "LLMRoute",
		Name:      "llm-" + modelID,
		Namespace: "aiplex-system",
	})
```

Check if K8sClient interface has a `Delete` method. If not, this step can be skipped (routes will be orphaned until cleanup).

- [ ] **Step 6: Build and run tests**

```bash
go build ./... && go test ./... -short
```

- [ ] **Step 7: Commit**

```bash
git add internal/api/llm.go cmd/aiplex-api/main.go
git commit -m "feat: apply Envoy LLMRoute CRDs on route config create/update/delete"
```

---

### Task 5: Final integration verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -v -count=1
```

All tests must pass.

- [ ] **Step 2: Build both binaries**

```bash
make build
```

- [ ] **Step 3: Verify no TODOs remain in critical path**

```bash
grep -rn "TODO" internal/api/llm.go internal/deploy/engine.go internal/auth/hydra.go
```

The only remaining TODOs should be non-critical (scope descriptions, monthly budget, etc.).

- [ ] **Step 4: Commit any remaining changes**

```bash
git status && git add -A && git commit -m "chore: final verification — all critical TODOs resolved"
```

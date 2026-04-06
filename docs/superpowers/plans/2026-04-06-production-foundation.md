# Production Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace in-memory storage with real Firestore, fix Hydra scope registration, and capture OAuth client secrets on agent registration — so data persists across restarts and agents can actually authenticate.

**Architecture:** The FirestoreStore already exists as a wrapper around MemoryStore. We replace each delegating method with real Firestore SDK calls. Hydra's CreateScope becomes a client-scope-update instead of a no-op. Agent registration returns the client_secret from Hydra's response.

**Tech Stack:** Go 1.24, cloud.google.com/go/firestore, Ory Hydra Admin API v2

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/registry/firestore.go` | Rewrite | Real Firestore SDK calls for all Store methods |
| `internal/registry/firestore_test.go` | Create | Integration tests against Firestore emulator |
| `internal/auth/hydra.go` | Modify | Fix CreateScope, parse client_secret from CreateClient response |
| `internal/api/agents.go` | Modify | Return client_secret in registration response |
| `internal/models/agent.go` | Modify | Add ClientSecret field (response-only) |
| `go.mod` | Modify | Add cloud.google.com/go/firestore dependency |

---

### Task 1: Add Firestore SDK dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add Firestore dependency**

```bash
cd /home/user/aiplex && go get cloud.google.com/go/firestore@latest google.golang.org/api@latest
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add Firestore SDK"
```

---

### Task 2: Implement real FirestoreStore — instances and templates

**Files:**
- Modify: `internal/registry/firestore.go`
- Create: `internal/registry/firestore_test.go`

- [ ] **Step 1: Write failing test for Firestore instance CRUD**

Create `internal/registry/firestore_test.go`:

```go
package registry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func skipWithoutEmulator(t *testing.T) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set — skipping Firestore tests")
	}
}

func TestFirestoreStore_InstanceCRUD(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("aiplex-test")
	if err != nil {
		t.Fatalf("NewFirestoreStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	inst := &models.Instance{
		ID:          "test-inst-001",
		Plane:       "mcplex",
		TemplateID:  "kb-search",
		DisplayName: "Test Instance",
		Owner:       "test@example.com",
		Status:      "running",
		DeployedAt:  time.Now(),
	}

	// Put
	if err := store.PutInstance(ctx, inst); err != nil {
		t.Fatalf("PutInstance: %v", err)
	}

	// Get
	got, err := store.GetInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got.DisplayName != inst.DisplayName {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, inst.DisplayName)
	}

	// List
	all, err := store.ListInstances(ctx, "")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	found := false
	for _, i := range all {
		if i.ID == inst.ID {
			found = true
		}
	}
	if !found {
		t.Error("instance not in list")
	}

	// Delete
	if err := store.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	_, err = store.GetInstance(ctx, inst.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
FIRESTORE_EMULATOR_HOST=localhost:8086 go test ./internal/registry/ -run TestFirestoreStore -v
```

Expected: FAIL — FirestoreStore still delegates to MemoryStore, but the test structure should compile. If emulator not running, test skips.

- [ ] **Step 3: Rewrite FirestoreStore with real Firestore SDK**

Read `internal/registry/firestore.go` then replace it. The key changes:

1. Import `cloud.google.com/go/firestore` and `google.golang.org/api/iterator`
2. Initialize a real `*firestore.Client` in `NewFirestoreStore`
3. Replace each delegating method with Firestore collection reads/writes
4. Add a `Close()` method

The pattern for each method:

```go
func (f *FirestoreStore) GetInstance(ctx context.Context, id string) (*models.Instance, error) {
    doc, err := f.client.Collection("instances").Doc(id).Get(ctx)
    if status.Code(err) == codes.NotFound {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("firestore get instance: %w", err)
    }
    var inst models.Instance
    if err := doc.DataTo(&inst); err != nil {
        return nil, fmt.Errorf("firestore decode instance: %w", err)
    }
    return &inst, nil
}

func (f *FirestoreStore) PutInstance(ctx context.Context, inst *models.Instance) error {
    _, err := f.client.Collection("instances").Doc(inst.ID).Set(ctx, inst)
    return err
}

func (f *FirestoreStore) DeleteInstance(ctx context.Context, id string) error {
    _, err := f.client.Collection("instances").Doc(id).Delete(ctx)
    return err
}

func (f *FirestoreStore) ListInstances(ctx context.Context, plane string) ([]models.Instance, error) {
    q := f.client.Collection("instances").Query
    if plane != "" {
        q = f.client.Collection("instances").Where("Plane", "==", plane)
    }
    iter := q.Documents(ctx)
    defer iter.Stop()
    var result []models.Instance
    for {
        doc, err := iter.Next()
        if err == iterator.Done {
            break
        }
        if err != nil {
            return nil, err
        }
        var inst models.Instance
        doc.DataTo(&inst)
        result = append(result, inst)
    }
    return result, nil
}
```

Apply this same pattern for: Templates (`templates` collection), Agents (`agents` collection), Deploy History (`deploy_history` collection), User Scopes (`user_scopes` collection), Route Configs (`route_configs` collection), Provider Configs (`provider_configs` collection), Usage Records (`usage_records` collection), Delegations (`delegations` collection), Policy Denials (`policy_denials` collection), Role Bindings (`role_bindings` collection).

The constructor:

```go
func NewFirestoreStore(projectID string) (*FirestoreStore, error) {
    if projectID == "" {
        return nil, fmt.Errorf("firestore project ID required")
    }
    ctx := context.Background()
    client, err := firestore.NewClient(ctx, projectID)
    if err != nil {
        return nil, fmt.Errorf("firestore client: %w", err)
    }
    return &FirestoreStore{client: client, projectID: projectID}, nil
}

func (f *FirestoreStore) Close() error {
    return f.client.Close()
}
```

- [ ] **Step 4: Run tests with emulator**

```bash
docker compose up -d firestore
FIRESTORE_EMULATOR_HOST=localhost:8086 go test ./internal/registry/ -run TestFirestoreStore -v
```

Expected: PASS

- [ ] **Step 5: Add tests for agents and templates**

Add to `firestore_test.go`:

```go
func TestFirestoreStore_AgentCRUD(t *testing.T) {
    skipWithoutEmulator(t)
    store, _ := NewFirestoreStore("aiplex-test")
    defer store.Close()
    ctx := context.Background()

    agent := &models.Agent{
        ClientID:      "test-agent",
        DisplayName:   "Test Agent",
        AuthMethod:    "client_credentials",
        AllowedScopes: []string{"mcp:tools:search"},
        Status:        "active",
        RegisteredAt:  time.Now(),
    }

    if err := store.PutAgent(ctx, agent); err != nil {
        t.Fatalf("PutAgent: %v", err)
    }

    got, err := store.GetAgent(ctx, agent.ClientID)
    if err != nil {
        t.Fatalf("GetAgent: %v", err)
    }
    if got.DisplayName != agent.DisplayName {
        t.Errorf("DisplayName = %q, want %q", got.DisplayName, agent.DisplayName)
    }

    if err := store.DeleteAgent(ctx, agent.ClientID); err != nil {
        t.Fatalf("DeleteAgent: %v", err)
    }
}
```

- [ ] **Step 6: Run all tests**

```bash
FIRESTORE_EMULATOR_HOST=localhost:8086 go test ./internal/registry/ -v
```

Expected: All pass (both MemoryStore and FirestoreStore tests).

- [ ] **Step 7: Commit**

```bash
git add internal/registry/firestore.go internal/registry/firestore_test.go go.mod go.sum
git commit -m "feat: implement real Firestore persistence for all Store methods"
```

---

### Task 3: Fix Hydra client_secret capture on agent registration

**Files:**
- Modify: `internal/auth/hydra.go`
- Modify: `internal/models/agent.go`
- Modify: `internal/api/agents.go`

- [ ] **Step 1: Write failing test**

Add to `internal/api/auth_test.go` (or the appropriate test file):

```go
func TestAgentRegister_ReturnsClientSecret(t *testing.T) {
    // This tests that the registration response includes client_secret
    store := registry.NewMemoryStore()
    h := NewAgentHandler(store) // no Hydra in test
    
    body := `{"client_id":"secret-test","display_name":"Test","auth_method":"client_credentials","allowed_scopes":["mcp:tools:x"]}`
    req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
    w := httptest.NewRecorder()
    h.Register(w, req)

    if w.Code != 201 {
        t.Fatalf("status = %d, want 201", w.Code)
    }

    var resp map[string]any
    json.NewDecoder(w.Body).Decode(&resp)

    // Without Hydra, client_secret should be empty (no OAuth client created)
    // With Hydra, it would contain the secret
    if _, exists := resp["client_id"]; !exists {
        t.Error("response missing client_id")
    }
}
```

- [ ] **Step 2: Update Hydra CreateClient to return client_secret**

In `internal/auth/hydra.go`, modify `CreateClient` to parse the response body and return the client_secret:

```go
type CreateClientResponse struct {
    ClientID     string `json:"client_id"`
    ClientSecret string `json:"client_secret"`
}

func (h *HydraClient) CreateClient(ctx context.Context, client OAuthClient) (*CreateClientResponse, error) {
    data, err := json.Marshal(client)
    if err != nil {
        return nil, err
    }
    req, err := http.NewRequestWithContext(ctx, "POST", h.adminURL+"/admin/clients", bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := h.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 300 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("hydra create client: status %d: %s", resp.StatusCode, body)
    }

    var result CreateClientResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("hydra decode response: %w", err)
    }
    return &result, nil
}
```

Add `"io"` to imports.

- [ ] **Step 3: Add ClientSecret to Agent model (response-only)**

In `internal/models/agent.go`, add a field:

```go
type Agent struct {
    // ... existing fields ...
    ClientSecret string `json:"client_secret,omitempty" firestore:"-"` // Only in registration response, never persisted
}
```

The `firestore:"-"` tag ensures it's never stored in Firestore.

- [ ] **Step 4: Update agent registration to capture and return secret**

In `internal/api/agents.go`, update the Register handler where Hydra CreateClient is called. Change from fire-and-forget to capturing the response:

Find the block that calls `h.hydra.CreateClient()` and replace with:

```go
if h.hydra != nil {
    grantTypes := agent.GrantTypes
    if len(grantTypes) == 0 {
        grantTypes = []string{"client_credentials"}
    }
    oauthClient := auth.OAuthClient{
        ClientID:                agent.ClientID,
        ClientName:              agent.DisplayName,
        GrantTypes:              grantTypes,
        Scope:                   strings.Join(agent.AllowedScopes, " "),
        RedirectURIs:            agent.RedirectURIs,
        TokenEndpointAuthMethod: "client_secret_basic",
    }
    resp, err := h.hydra.CreateClient(r.Context(), oauthClient)
    if err != nil {
        zerolog.Ctx(r.Context()).Warn().Err(err).Str("client_id", agent.ClientID).Msg("hydra client creation failed")
    } else if resp != nil {
        agent.ClientSecret = resp.ClientSecret
    }
}
```

- [ ] **Step 5: Build and run tests**

```bash
go build ./... && go test ./internal/api/ -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/auth/hydra.go internal/models/agent.go internal/api/agents.go
git commit -m "feat: capture and return client_secret from Hydra on agent registration"
```

---

### Task 4: Fix Hydra CreateScope — update client allowed_scopes

**Files:**
- Modify: `internal/auth/hydra.go`

- [ ] **Step 1: Implement CreateScope as an update-client-scopes operation**

Hydra doesn't have a scope registry. The correct approach: when new scopes are discovered (during deploy), update the relevant client's `allowed_scope` field. Replace the no-op `CreateScope` with `UpdateClientScopes`:

```go
// UpdateClientScopes adds scopes to an existing OAuth client's allowed_scope list.
func (h *HydraClient) UpdateClientScopes(ctx context.Context, clientID string, scopes []string) error {
    // PATCH /admin/clients/{clientID}
    patch := map[string]any{
        "scope": strings.Join(scopes, " "),
    }
    data, err := json.Marshal(patch)
    if err != nil {
        return err
    }
    req, err := http.NewRequestWithContext(ctx, "PATCH", h.adminURL+"/admin/clients/"+clientID, bytes.NewReader(data))
    if err != nil {
        return err
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := h.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 300 {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("hydra update client scopes: status %d: %s", resp.StatusCode, body)
    }
    return nil
}
```

Keep the old `CreateScope` as a deprecated wrapper or remove it. Check all callers first — if `CreateScope` is called in `deploy/engine.go` or elsewhere, update those callsites to use the new method.

- [ ] **Step 2: Build and verify**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/auth/hydra.go
git commit -m "feat: replace no-op CreateScope with UpdateClientScopes via Hydra PATCH"
```

---

### Task 5: Wire Firestore into API server startup

**Files:**
- Modify: `cmd/aiplex-api/main.go`

- [ ] **Step 1: Read `cmd/aiplex-api/main.go`**

Find where the store is initialized. Currently it likely uses `NewMemoryStore()` or `NewFirestoreStore()` which delegates to memory.

- [ ] **Step 2: Update store initialization to use real Firestore when configured**

```go
var store registry.Store
if os.Getenv("FIRESTORE_EMULATOR_HOST") != "" || os.Getenv("GCP_PROJECT_ID") != "" {
    projectID := os.Getenv("GCP_PROJECT_ID")
    if projectID == "" {
        projectID = os.Getenv("FIRESTORE_PROJECT_ID")
    }
    fs, err := registry.NewFirestoreStore(projectID)
    if err != nil {
        log.Fatal().Err(err).Msg("firestore init failed")
    }
    defer fs.Close()
    store = fs
    log.Info().Str("project", projectID).Msg("using Firestore store")
} else {
    store = registry.NewMemoryStore()
    log.Warn().Msg("using in-memory store (data will not persist)")
}
```

- [ ] **Step 3: Build and test**

```bash
go build ./cmd/aiplex-api/ && go test ./... -short
```

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-api/main.go
git commit -m "feat: wire real Firestore store into API server startup"
```

---

### Task 6: Input validation for agent registration

**Files:**
- Modify: `internal/api/agents.go`

- [ ] **Step 1: Add validation for auth_method, scope format, redirect URIs**

In the Register handler, after the existing ClientID/DisplayName check, add:

```go
// Validate auth method
validAuthMethods := map[string]bool{
    "client_credentials":  true,
    "authorization_code":  true,
    "device_code":         true,
}
if !validAuthMethods[agent.AuthMethod] {
    writeError(w, http.StatusBadRequest, "INVALID_AUTH_METHOD",
        fmt.Sprintf("auth_method must be one of: client_credentials, authorization_code, device_code; got %q", agent.AuthMethod))
    return
}

// Validate scope format
for _, scope := range agent.AllowedScopes {
    if !strings.HasPrefix(scope, "mcp:") && !strings.HasPrefix(scope, "a2a:") && !strings.HasPrefix(scope, "llm:") {
        writeError(w, http.StatusBadRequest, "INVALID_SCOPE",
            fmt.Sprintf("scope %q must start with mcp:, a2a:, or llm:", scope))
        return
    }
}

// Validate redirect URIs for authorization_code flow
if agent.AuthMethod == "authorization_code" {
    if len(agent.RedirectURIs) == 0 {
        writeError(w, http.StatusBadRequest, "MISSING_REDIRECT_URIS",
            "authorization_code flow requires at least one redirect_uri")
        return
    }
    for _, uri := range agent.RedirectURIs {
        if !strings.HasPrefix(uri, "https://") && !strings.HasPrefix(uri, "http://localhost") {
            writeError(w, http.StatusBadRequest, "INVALID_REDIRECT_URI",
                fmt.Sprintf("redirect_uri %q must use HTTPS (or http://localhost for dev)", uri))
            return
        }
    }
}
```

- [ ] **Step 2: Add test for validation**

```go
func TestAgentRegister_InvalidAuthMethod(t *testing.T) {
    store := registry.NewMemoryStore()
    h := NewAgentHandler(store)
    body := `{"client_id":"bad","display_name":"Bad","auth_method":"invalid","allowed_scopes":["mcp:tools:x"]}`
    req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
    w := httptest.NewRecorder()
    h.Register(w, req)
    if w.Code != 400 {
        t.Errorf("status = %d, want 400", w.Code)
    }
}

func TestAgentRegister_InvalidScope(t *testing.T) {
    store := registry.NewMemoryStore()
    h := NewAgentHandler(store)
    body := `{"client_id":"bad","display_name":"Bad","auth_method":"client_credentials","allowed_scopes":["invalid:scope"]}`
    req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
    w := httptest.NewRecorder()
    h.Register(w, req)
    if w.Code != 400 {
        t.Errorf("status = %d, want 400", w.Code)
    }
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/api/ -run TestAgentRegister -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/api/agents.go internal/api/*_test.go
git commit -m "feat: add input validation for agent registration — auth method, scopes, redirect URIs"
```

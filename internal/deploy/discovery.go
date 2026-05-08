package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

type DiscoveredTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DiscoverTools calls MCP tools/list against a deployed MCP server's /mcp
// endpoint and returns the tool definitions. Use this for MCPlex only —
// A2A agents speak the Agent Card protocol; see DiscoverAgentCard.
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

// ErrAgentCardNotFound is returned when an A2A agent does not expose a
// /.well-known/agent.json endpoint. Callers may fall back to JSON-RPC
// tasks/list discovery when this is returned.
var ErrAgentCardNotFound = fmt.Errorf("agent card not found")

// DiscoverAgentCard fetches an A2A agent's well-known card. baseURL is the
// agent's service base URL (without the /.well-known/agent.json suffix).
// The returned AgentCard contains the agent's task types, auth schemes, and
// metadata as advertised by the running agent itself. Returns
// ErrAgentCardNotFound when the endpoint returns 404 so callers can fall back.
func DiscoverAgentCard(ctx context.Context, baseURL string) (*models.AgentCard, error) {
	cardURL := strings.TrimRight(baseURL, "/") + "/.well-known/agent.json"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent card request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrAgentCardNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent card returned status %d", resp.StatusCode)
	}

	var card models.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}
	if err := ValidateAgentCard(&card); err != nil {
		return nil, fmt.Errorf("invalid agent card: %w", err)
	}
	return &card, nil
}

// ValidateAgentCard checks that an Agent Card has the required fields per the
// A2A spec. Required: name and at least one task type with a non-empty type.
// Each task type must have a unique, non-empty type identifier.
func ValidateAgentCard(card *models.AgentCard) error {
	if card == nil {
		return fmt.Errorf("agent card is nil")
	}
	if strings.TrimSpace(card.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(card.TaskTypes) == 0 {
		return fmt.Errorf("at least one task type is required")
	}
	seen := make(map[string]struct{}, len(card.TaskTypes))
	for i, tt := range card.TaskTypes {
		t := strings.TrimSpace(tt.Type)
		if t == "" {
			return fmt.Errorf("task_types[%d].type is empty", i)
		}
		if _, dup := seen[t]; dup {
			return fmt.Errorf("task_types[%d].type %q is duplicated", i, t)
		}
		seen[t] = struct{}{}
	}
	return nil
}

// DiscoverTasks calls the JSON-RPC tasks/list method on an A2A agent as a
// fallback when the Agent Card is unavailable. The endpoint is the agent's
// JSON-RPC URL (typically the service root). Returns task type names.
func DiscoverTasks(ctx context.Context, endpoint string) ([]string, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tasks/list",
		"params":  map[string]any{},
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tasks/list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tasks/list returned status %d", resp.StatusCode)
	}

	var rpcResp struct {
		Result struct {
			Tasks []struct {
				Type string `json:"type"`
			} `json:"tasks"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode tasks/list response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("tasks/list error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	tasks := make([]string, 0, len(rpcResp.Result.Tasks))
	for _, t := range rpcResp.Result.Tasks {
		if t.Type != "" {
			tasks = append(tasks, t.Type)
		}
	}
	return tasks, nil
}

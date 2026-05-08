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

// DiscoverAgentCard fetches an A2A agent's well-known card. baseURL is the
// agent's service base URL (without the /.well-known/agent.json suffix).
// The returned AgentCard contains the agent's task types, auth schemes, and
// metadata as advertised by the running agent itself.
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent card returned status %d", resp.StatusCode)
	}

	var card models.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}
	return &card, nil
}

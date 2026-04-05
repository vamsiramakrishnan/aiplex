---
sidebar_position: 3
title: Go SDK
description: Programmatic access to the AIPlex API using the Go SDK.
---

# Go SDK

The AIPlex Go SDK provides typed access to the AIPlex API for building integrations and automation.

## Installation

```bash
go get github.com/vamsiramakrishnan/aiplex/sdk/aiplex
```

## Quick Start

```go
package main

import (
    "fmt"
    "github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func main() {
    client := aiplex.NewClient("https://aiplex.example.com")
    client.SetToken("your-bearer-token")

    // Deploy an MCP tool
    instance, err := client.Deploy(aiplex.DeployRequest{
        TemplateID: "github-mcp-server",
        Name:       "my-github-tools",
        Plane:      "mcplex",
        Config: map[string]string{
            "GITHUB_TOKEN": "ghp_...",
        },
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("Deployed: %s at %s\n", instance.ID, instance.Endpoint)
}
```

## Client

### Creating a Client

```go
client := aiplex.NewClient("https://aiplex.example.com")
client.SetToken(token)
```

The client uses a 30-second HTTP timeout and sends bearer tokens in the `Authorization` header.

## Catalog

```go
// List all templates
templates, err := client.ListCatalog()

// Get a specific template
template, err := client.GetTemplate("github-mcp-server")
```

## Instances

```go
// Deploy
instance, err := client.Deploy(aiplex.DeployRequest{
    TemplateID: "github-mcp-server",
    Name:       "my-tools",
    Plane:      "mcplex",
    Config:     map[string]string{"KEY": "value"},
})

// List (with optional filtering)
instances, err := client.ListInstances(aiplex.ListOptions{
    Plane: "mcplex",
})

// Get details
instance, err := client.GetInstance("my-tools")

// Undeploy
err := client.Undeploy("my-tools")

// Deploy history
history, err := client.GetHistory("my-tools")
```

## Agents

```go
// Register
agent, err := client.RegisterAgent(aiplex.RegisterAgentRequest{
    Name:        "my-agent",
    Description: "My custom agent",
    Scopes:      []string{"mcp:tools:search", "llm:model:gemini-2.5-flash"},
})

// List
agents, err := client.ListAgents()

// Get with permissions
agent, err := client.GetAgent("my-agent")
perms, err := client.GetAgentPermissions("my-agent")

// Delete
err := client.DeleteAgent("my-agent")
```

## LLM Routes

```go
// List routes
routes, err := client.ListLLMRoutes()

// Create/update a route
err := client.PutLLMRoute(aiplex.LLMRouteConfig{
    Name: "default",
    Backends: []aiplex.Backend{
        {Provider: "google", Model: "gemini-2.5-flash", Weight: 80},
        {Provider: "anthropic", Model: "claude-sonnet-4-20250514", Weight: 20},
    },
})

// Get usage
usage, err := client.GetUsageSummary(aiplex.UsageQuery{
    AgentID: "my-agent",
    Period:  "7d",
})

// Delete
err := client.DeleteLLMRoute("old-route")
```

## A2A

```go
// Get agent card
card, err := client.GetAgentCard("research-agent")

// List all agent cards
cards, err := client.ListAgentCards()

// View delegations
delegations, err := client.ListDelegations(aiplex.DelegationQuery{
    AgentID: "tutor-agent",
})

// View delegation chain
chain, err := client.GetDelegationChain("delegation-id")
```

## Dashboard

```go
// Get stats
stats, err := client.GetDashboardStats()

// List policy denials
denials, err := client.ListPolicyDenials()
```

## User Permissions

```go
// Get user scopes
scopes, err := client.GetUserScopes("user@example.com")

// Set user scopes
err := client.SetUserScopes("user@example.com", []string{
    "mcp:tools:search",
    "llm:model:gemini-2.5-flash",
})
```

## Health

```go
err := client.Health()
if err != nil {
    fmt.Println("API is unhealthy:", err)
}
```

## Types

Key types used across the SDK:

```go
type Instance struct {
    ID         string            `json:"id"`
    Plane      string            `json:"plane"`
    TemplateID string            `json:"template_id"`
    Owner      string            `json:"owner"`
    Namespace  string            `json:"namespace"`
    SPIFFEID   string            `json:"spiffe_id"`
    Scopes     []string          `json:"scopes"`
    Status     string            `json:"status"`
    Config     map[string]string `json:"config"`
    Endpoint   string            `json:"endpoint"`
    DeployedAt string            `json:"deployed_at"`
}

type Agent struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    ClientID    string   `json:"client_id"`
    Scopes      []string `json:"scopes"`
    CreatedAt   string   `json:"created_at"`
}

type DashboardStats struct {
    TotalInstances  int            `json:"total_instances"`
    ByPlane         map[string]int `json:"by_plane"`
    TotalAgents     int            `json:"total_agents"`
    RecentDenials   int            `json:"recent_denials"`
    LLMCostToday    float64        `json:"llm_cost_today_usd"`
}
```

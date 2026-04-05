---
sidebar_position: 1
title: CLI Reference
description: Complete reference for the aiplex command-line interface.
---

# CLI Reference

The `aiplex` CLI is the primary interface for managing AIPlex resources. It supports interactive guided workflows, declarative YAML, and direct commands.

## Installation

```bash
curl -fsSL https://get.aiplex.dev | sh
# or
brew install vamsiramakrishnan/tap/aiplex
# or
go install github.com/vamsiramakrishnan/aiplex/cmd/aiplex-cli@latest
```

## Global Flags

| Flag | Env Var | Description |
|------|---------|-------------|
| `--url` | `AIPLEX_URL` | AIPlex API URL |
| `--token` | `AIPLEX_TOKEN` | Bearer token |
| `--output` / `-o` | — | Output format: `table` (default), `json`, `yaml` |
| `--quiet` / `-q` | — | Suppress non-essential output |

## Authentication

### `aiplex login`

Authenticate with GCP and store credentials.

```bash
aiplex login                    # Browser-based OAuth
aiplex login --use-gcloud       # Use existing gcloud credentials
```

Credentials stored in `~/.aiplex/credentials.json`.

## Deploy & Manage

### `aiplex deploy`

Interactive guided deployment.

```bash
aiplex deploy                   # Full interactive flow
aiplex deploy --template github-mcp-server --name my-tools
```

### `aiplex apply`

Declarative deployment from YAML.

```bash
aiplex apply -f aiplex.yaml     # Apply configuration
aiplex apply -f dir/            # Apply all YAML in directory
```

### `aiplex diff`

Preview changes without applying.

```bash
aiplex diff -f aiplex.yaml
```

### `aiplex init`

Generate a starter `aiplex.yaml` interactively.

```bash
aiplex init                     # Interactive wizard
aiplex init --plane mcplex      # MCPlex-focused
```

### `aiplex validate`

Validate configuration without deploying.

```bash
aiplex validate -f aiplex.yaml
```

## Instance Management

### `aiplex ls`

List running instances.

```bash
aiplex ls                       # All planes
aiplex ls --plane mcplex        # MCPlex only
aiplex ls --plane a2aplex       # A2APlex only
aiplex ls -o json               # JSON output
```

### `aiplex get`

Get instance details.

```bash
aiplex get my-github-tools
aiplex get my-github-tools -o yaml
```

### `aiplex status`

Detailed instance status with health, scopes, and endpoint.

```bash
aiplex status my-github-tools
```

### `aiplex logs`

Stream instance logs.

```bash
aiplex logs my-github-tools               # Recent logs
aiplex logs my-github-tools --follow      # Stream live
aiplex logs my-github-tools --level error # Filter by level
```

### `aiplex config`

Update instance configuration.

```bash
aiplex config my-github-tools --set KEY=value
```

### `aiplex scale`

Scale instance replicas (if supported).

```bash
aiplex scale my-github-tools --replicas 3
```

### `aiplex rm`

Remove an instance.

```bash
aiplex rm my-github-tools
aiplex rm my-github-tools --force   # Skip confirmation
```

## Catalog

### `aiplex catalog search`

Search templates.

```bash
aiplex catalog search "github"
aiplex catalog search "database" --plane mcplex
```

### `aiplex catalog list`

List all templates.

```bash
aiplex catalog list --plane mcplex
```

### `aiplex catalog get`

Get template details.

```bash
aiplex catalog get github-mcp-server
```

### `aiplex catalog upload`

Upload a custom template.

```bash
aiplex catalog upload -f my-template.yaml
```

## Agents

### `aiplex agents register`

Register a new agent.

```bash
aiplex agents register \
  --name my-agent \
  --description "My agent" \
  --grant mcp:tools:search
```

### `aiplex agents list`

List registered agents.

```bash
aiplex agents list
```

### `aiplex agents get`

Get agent details with cross-plane scope breakdown.

```bash
aiplex agents get my-agent
```

### `aiplex agents grant` / `revoke`

Manage agent scopes.

```bash
aiplex agents grant my-agent --scope mcp:tools:new_tool
aiplex agents revoke my-agent --scope mcp:tools:old_tool
```

### `aiplex agents credentials`

Get OAuth client credentials.

```bash
aiplex agents credentials my-agent
```

### `aiplex agents delete`

Delete an agent registration.

```bash
aiplex agents delete my-agent
```

## Users

### `aiplex users grant` / `revoke`

Manage user permissions (dimension B).

```bash
aiplex users grant user@example.com --scope mcp:tools:search
aiplex users revoke user@example.com --scope mcp:tools:search
```

### `aiplex users permissions`

View user's current scopes.

```bash
aiplex users permissions user@example.com
```

## LLM

### `aiplex llm routes`

List LLM routing configurations.

```bash
aiplex llm routes
```

### `aiplex llm usage`

View token usage and costs.

```bash
aiplex llm usage --period 7d
aiplex llm usage --agent my-agent --period 30d
```

### `aiplex llm budget`

Set spending limits.

```bash
aiplex llm budget my-agent --daily 10.00 --monthly 200.00
```

### `aiplex llm providers`

List configured providers.

```bash
aiplex llm providers
```

## A2A

### `aiplex a2a list`

List A2A agents.

```bash
aiplex a2a list
```

### `aiplex a2a card`

View an agent's A2A Agent Card.

```bash
aiplex a2a card research-agent
```

### `aiplex a2a delegations`

View delegation history.

```bash
aiplex a2a delegations --agent tutor-agent
aiplex a2a delegations --chain   # Show delegation chains
```

## Platform

### `aiplex platform setup`

Provision AIPlex infrastructure.

```bash
aiplex platform setup
aiplex platform setup --resume   # Resume failed setup
```

### `aiplex platform upgrade`

Upgrade the platform.

```bash
aiplex platform upgrade
```

### `aiplex platform destroy`

Tear down all infrastructure.

```bash
aiplex platform destroy
```

## Diagnostics

### `aiplex doctor`

Run health checks.

```bash
aiplex doctor
```

### `aiplex dashboard`

View metrics summary.

```bash
aiplex dashboard
aiplex dashboard --period 24h
```

### `aiplex health`

Check API health.

```bash
aiplex health
```

## Configuration

Config stored in `~/.aiplex/`:

| File | Purpose |
|------|---------|
| `config.json` | API URL, default output format |
| `credentials.json` | Auth tokens |

Resolution order: CLI flags > environment variables > config file.

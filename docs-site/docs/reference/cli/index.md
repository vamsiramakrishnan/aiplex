---
title: CLI reference
sidebar_label: Overview
description: Every aiplex command, generated from the CLI's own help output.
---

## Commands

| Command | Purpose |
| --- | --- |
| [`aiplex agents`](./agents.md) | Register agents, bind their cloud identities and set their permissions |
| [`aiplex a2a`](./a2a.md) | Invoke connected agents, discover A2A agents and inspect delegations |
| [`aiplex llm`](./llm.md) | Connect model providers (surfaces), and inspect routes, batches and capacity |
| [`aiplex mcp`](./mcp.md) | Connect MCP servers, list their tools and call them through the gateway |
| [`aiplex tools`](./tools.md) | Set a tool connection's credential type and which tools it allows |
| [`aiplex tool-auth`](./tool-auth.md) | Authorize a downstream tool for yourself (OAuth sign-in to the tool) |
| [`aiplex skills`](./skills.md) | Inspect skill servers and skill invocations |
| [`aiplex connect`](./connect.md) | Print an access point configuration for your coding harness or SDK |
| [`aiplex clients`](./clients.md) | Register a coding harness or IDE and review its connection plan |
| [`aiplex dev`](./dev.md) | Start a local stack with a mock agent, models and MCP tools |
| [`aiplex login`](./login.md) | Sign in to an AIPlex API and store the token for the current context |
| [`aiplex logout`](./logout.md) | Forget the stored token for the current context |
| [`aiplex auth`](./auth.md) | Credential helpers for tools that fetch a token by running a command |
| [`aiplex whoami`](./whoami.md) | Show which API and identity the CLI is using, and your roles |
| [`aiplex config`](./config.md) | Manage named contexts (API URL and credentials per environment) |
| [`aiplex ctx`](./ctx.md) | Switch to another context, or show the current one |
| [`aiplex doctor`](./doctor.md) | Diagnose your setup and say what to fix |
| [`aiplex health`](./health.md) | Check that the API and its components answer |
| [`aiplex version`](./version.md) | Show the CLI version, commit and whether the Console is built in |
| [`aiplex init`](./init.md) | Set up AIPlex on a Google Cloud project (guided) |
| [`aiplex quickstart`](./quickstart.md) | Deploy AIPlex and an example workload to Google Cloud |
| [`aiplex access-points`](./access-points.md) | Create and manage access points: one URL with identity, access and quota |
| [`aiplex who`](./who.md) | See every caller: how it signs in, what it can reach and its usage |
| [`aiplex identity`](./identity.md) | Let people sign in with your corporate identity provider |
| [`aiplex iam`](./iam.md) | Map identity-provider groups to roles (admin, deployer, viewer) with role bindings |
| [`aiplex usage`](./usage.md) | See what each request cost: records, totals and model prices |
| [`aiplex quota`](./quota.md) | Inspect quota policy, balances, loans and admission decisions |
| [`aiplex budgets`](./budgets.md) | Budgets: team and project limits above per-caller quota |
| [`aiplex hooks`](./hooks.md) | Request hooks — inspect, mask or refuse prompts, responses and tool calls |
| [`aiplex status`](./status.md) | Show a dashboard summary of the platform |
| [`aiplex resources`](./resources.md) | Low-level access to any resource kind (prefer the guided commands) |
| [`aiplex apply`](./apply.md) | Apply a declarative AIPlex configuration file |
| [`aiplex validate`](./validate.md) | Check an AIPlex configuration file without applying it |
| [`aiplex modules`](./modules.md) | List the modules this API has enabled |
| [`aiplex platform`](./platform.md) | Deploy, inspect or destroy the AIPlex platform on Google Cloud |
| [`aiplex upgrade`](./upgrade.md) | Upgrade a deployed AIPlex platform |
| [`aiplex infra`](./infra.md) | Review, execute and inspect saved infrastructure plans |
| [`aiplex native`](./native.md) | Read and apply native cloud governance plans |
| [`aiplex catalog`](./catalog.md) | Browse templates you can deploy |
| [`aiplex deploy`](./deploy.md) | Deploy an instance from a catalog template |
| [`aiplex undeploy`](./undeploy.md) | Remove a deployed instance |
| [`aiplex list`](./list.md) | List instances, agents, delegations or policy denials |
| [`aiplex get`](./get.md) | Show one instance, agent or delegation |
| [`aiplex logs`](./logs.md) | Stream logs from a deployed instance |
| [`aiplex runs`](./runs.md) | Inspect and operate agent runs |
| [`aiplex console`](./console.md) | Serve the Console locally against the current context |
| [`aiplex tui`](./tui.md) | Open an interactive terminal dashboard |

## Global flags

```text
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
  -h, --help             help for aiplex
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)
```

## Root help

```text
Use AIPlex to invoke agents, call models, run tools and connect coding harnesses.

Try these commands locally with the built-in mocks:
  aiplex dev --detach
  aiplex a2a invoke local-agent --operation query
  aiplex dev try
  aiplex mcp connections call local-tools echo --args '{"text":"hello"}'
  aiplex connect local --client codex
  aiplex dev stop

Usage:
  aiplex [command]

Agents:
  agents        Register agents, bind their cloud identities and set their permissions
  a2a           Invoke connected agents, discover A2A agents and inspect delegations

Models:
  llm           Connect model providers (surfaces), and inspect routes, batches and capacity

Tools:
  mcp           Connect MCP servers, list their tools and call them through the gateway
  tools         Set a tool connection's credential type and which tools it allows
  tool-auth     Authorize a downstream tool for yourself (OAuth sign-in to the tool)
  skills        Inspect skill servers and skill invocations

Harnesses:
  connect       Print an access point configuration for your coding harness or SDK
  clients       Register a coding harness or IDE and review its connection plan

Get started:
  dev           Start a local stack with a mock agent, models and MCP tools
  login         Sign in to an AIPlex API and store the token for the current context
  logout        Forget the stored token for the current context
  auth          Credential helpers for tools that fetch a token by running a command
  whoami        Show which API and identity the CLI is using, and your roles
  config        Manage named contexts (API URL and credentials per environment)
  ctx           Switch to another context, or show the current one
  doctor        Diagnose your setup and say what to fix
  health        Check that the API and its components answer
  version       Show the CLI version, commit and whether the Console is built in
  init          Set up AIPlex on a Google Cloud project (guided)
  quickstart    Deploy AIPlex and an example workload to Google Cloud

Access and identity:
  access-points Create and manage access points: one URL with identity, access and quota
  who           See every caller: how it signs in, what it can reach and its usage
  identity      Let people sign in with your corporate identity provider
  iam           Map identity-provider groups to roles (admin, deployer, viewer) with role bindings

Usage and quota:
  usage         See what each request cost: records, totals and model prices
  quota         Inspect quota policy, balances, loans and admission decisions
  budgets       Budgets: team and project limits above per-caller quota
  hooks         Request hooks — inspect, mask or refuse prompts, responses and tool calls

Operate:
  status        Show a dashboard summary of the platform
  resources     Low-level access to any resource kind (prefer the guided commands)
  apply         Apply a declarative AIPlex configuration file
  validate      Check an AIPlex configuration file without applying it
  modules       List the modules this API has enabled
  platform      Deploy, inspect or destroy the AIPlex platform on Google Cloud
  upgrade       Upgrade a deployed AIPlex platform
  infra         Review, execute and inspect saved infrastructure plans
  native        Read and apply native cloud governance plans
  catalog       Browse templates you can deploy
  deploy        Deploy an instance from a catalog template
  undeploy      Remove a deployed instance
  list          List instances, agents, delegations or policy denials
  get           Show one instance, agent or delegation
  logs          Stream logs from a deployed instance
  runs          Inspect and operate agent runs
  console       Serve the Console locally against the current context
  tui           Open an interactive terminal dashboard

Additional Commands:
  completion    Print a shell completion script (commands, flags and resource ids)
  help          Help about any command

Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
  -h, --help             help for aiplex
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

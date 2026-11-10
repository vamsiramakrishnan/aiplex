---
title: CLI reference
sidebar_label: Overview
description: Every aiplex command, generated from the CLI's own help output.
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

## Global flags

```text
      --debug           Enable debug output
  -h, --help            help for aiplex
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

## Commands

| Command | Purpose |
| --- | --- |
| [`aiplex a2a`](./a2a.md) | A2APlex — manage agent delegation and discovery |
| [`aiplex access-points`](./access-points.md) | Access points: one URL that enforces identity, access, QoS and quota |
| [`aiplex agents`](./agents.md) | Manage registered agents |
| [`aiplex apply`](./apply.md) | Apply a declarative configuration file |
| [`aiplex catalog`](./catalog.md) | Browse the template catalog |
| [`aiplex clients`](./clients.md) | Register developer clients and review connection plans |
| [`aiplex config`](./config.md) | Manage CLI configuration and contexts |
| [`aiplex connect`](./connect.md) | Print ready-to-paste configuration for pointing a client at an access point |
| [`aiplex console`](./console.md) | Launch the AIPlex console locally |
| [`aiplex ctx`](./ctx.md) | Switch context (shorthand for config use-context) |
| [`aiplex deploy`](./deploy.md) | Deploy an instance from a catalog template |
| [`aiplex doctor`](./doctor.md) | Diagnose common issues and suggest fixes |
| [`aiplex get`](./get.md) | Get details of a resource |
| [`aiplex health`](./health.md) | Check connectivity to all AIPlex components |
| [`aiplex identity`](./identity.md) | Workforce identity: let people sign in with their corporate identity provider |
| [`aiplex infra`](./infra.md) | Review, execute and inspect saved infrastructure plans |
| [`aiplex init`](./init.md) | Set up AIPlex on a GCP project |
| [`aiplex list`](./list.md) | List resources |
| [`aiplex llm`](./llm.md) | LLMPlex — manage model routes, providers, and costs |
| [`aiplex login`](./login.md) | Authenticate with your AIPlex instance |
| [`aiplex logout`](./logout.md) | Remove stored credentials for the current context |
| [`aiplex logs`](./logs.md) | Stream logs from a deployed instance |
| [`aiplex modules`](./modules.md) | Discover enabled Aiplex modules |
| [`aiplex native`](./native.md) | Inspect and review configured native cloud governance |
| [`aiplex platform`](./platform.md) | Manage AIPlex platform infrastructure |
| [`aiplex quickstart`](./quickstart.md) | Zero to running platform in one command |
| [`aiplex quota`](./quota.md) | Inspect managed quota execution |
| [`aiplex resources`](./resources.md) | Manage connected module resources (model-surface, agent-connection, tool-connection, network-profile, client-destination, deployment-intent, agent-binding) |
| [`aiplex runs`](./runs.md) | Inspect + operate on Tape-backed runs |
| [`aiplex skills`](./skills.md) | SkillsPlex — manage skill servers and invocations |
| [`aiplex status`](./status.md) | Show dashboard summary |
| [`aiplex tool-auth`](./tool-auth.md) | Authorize downstream tools for the verified principal and actor |
| [`aiplex tools`](./tools.md) | Configure MCP tool connection auth and tool policy |
| [`aiplex tui`](./tui.md) | Interactive terminal dashboard |
| [`aiplex undeploy`](./undeploy.md) | Terminate and remove a deployed instance |
| [`aiplex upgrade`](./upgrade.md) | Upgrade the AIPlex platform (alias for platform apply) |
| [`aiplex usage`](./usage.md) | Usage ledger — what each request cost in requests, tokens and dollars |
| [`aiplex validate`](./validate.md) | Validate an AIPlex manifest file |
| [`aiplex version`](./version.md) | Show CLI version and check for updates |
| [`aiplex who`](./who.md) | See every caller: how it authenticates, what it can reach and why, and its usage |
| [`aiplex whoami`](./whoami.md) | Show current context, account, and auth status |

## Root help

```text
AIPlex CLI manages three planes through a single gateway:
  MCPlex  — Agent ↔ Tool   (MCP servers)
  A2APlex — Agent ↔ Agent  (A2A delegation)
  LLMPlex — Agent ↔ Model  (LLM providers)

Get started:
  aiplex init             Set up AIPlex on a GCP project
  aiplex login            Authenticate with your AIPlex instance
  aiplex config show      View current configuration
  aiplex health           Check connectivity to all components

Usage:
  aiplex [command]

Available Commands:
  a2a           A2APlex — manage agent delegation and discovery
  access-points Access points: one URL that enforces identity, access, QoS and quota
  agents        Manage registered agents
  apply         Apply a declarative configuration file
  catalog       Browse the template catalog
  clients       Register developer clients and review connection plans
  completion    Generate shell completion scripts
  config        Manage CLI configuration and contexts
  connect       Print ready-to-paste configuration for pointing a client at an access point
  console       Launch the AIPlex console locally
  ctx           Switch context (shorthand for config use-context)
  deploy        Deploy an instance from a catalog template
  doctor        Diagnose common issues and suggest fixes
  get           Get details of a resource
  health        Check connectivity to all AIPlex components
  help          Help about any command
  identity      Workforce identity: let people sign in with their corporate identity provider
  infra         Review, execute and inspect saved infrastructure plans
  init          Set up AIPlex on a GCP project
  list          List resources
  llm           LLMPlex — manage model routes, providers, and costs
  login         Authenticate with your AIPlex instance
  logout        Remove stored credentials for the current context
  logs          Stream logs from a deployed instance
  modules       Discover enabled Aiplex modules
  native        Inspect and review configured native cloud governance
  platform      Manage AIPlex platform infrastructure
  quickstart    Zero to running platform in one command
  quota         Inspect managed quota execution
  resources     Manage connected module resources (model-surface, agent-connection, tool-connection, network-profile, client-destination, deployment-intent, agent-binding)
  runs          Inspect + operate on Tape-backed runs
  skills        SkillsPlex — manage skill servers and invocations
  status        Show dashboard summary
  tool-auth     Authorize downstream tools for the verified principal and actor
  tools         Configure MCP tool connection auth and tool policy
  tui           Interactive terminal dashboard
  undeploy      Terminate and remove a deployed instance
  upgrade       Upgrade the AIPlex platform (alias for platform apply)
  usage         Usage ledger — what each request cost in requests, tokens and dollars
  validate      Validate an AIPlex manifest file
  version       Show CLI version and check for updates
  who           See every caller: how it authenticates, what it can reach and why, and its usage
  whoami        Show current context, account, and auth status

Flags:
      --debug           Enable debug output
  -h, --help            help for aiplex
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex [command] --help" for more information about a command.
```

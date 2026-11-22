---
title: Getting started
description: Build AIPlex and try agents, models, tools and harnesses locally.
---

# Getting started

## Install

Build the CLI and API together from a source checkout with Git, Make and Go 1.25 or newer.

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
bin/aiplex dev --detach
```

Try these commands in the same terminal. The stack uses local mock services.

```bash
bin/aiplex a2a invoke local-agent --operation query
bin/aiplex dev try
bin/aiplex mcp connections call local-tools echo --args '{"text":"hello"}'
bin/aiplex connect local --client codex
```

The agent replies, the model returns a response and usage record, and the MCP tool echoes `hello`. The last command prints the settings Codex can use. Run `bin/aiplex dev stop` when finished. [Local walkthrough](./local-development.md)

To include the web Console, build with `make console && make build` (Node 22), then restart local development. The tagged-release workflow prepares archives, containers and a macOS Homebrew cask. The source build is the available installation path ahead of the first release.

## Choose your environment

| Environment | What you can do | Guide |
| --- | --- | --- |
| Local, in memory | Try the CLI, managed gateways, ledger and budgets. No provider credentials required. | [Local development](./local-development.md) · [Shared spending limit](../guides/quotaplex.md) |
| Local, with Docker | Exercise Firestore emulation and Ory services. | [Docker stack](./docker-stack.md) |
| Existing platform | Connect a coding client or deploy a tool from the catalog. | [Connect a client](./connect-client.md) · [Deploy a tool](./first-deploy.md) |
| Google Cloud | Provision the cluster and configure identity, routing and storage. | [Install the platform](./install-platform.md) · [Production qualification](../library/production-qualification.md) |

---
title: Getting started
description: "Start with one command, aiplex dev. Docker, cloud and existing-platform paths come after."
---

# Getting started

Start with `aiplex dev`. It runs AIPlex on your machine with a mock model and a mock MCP server and needs only Go. Move on to the Docker stack or a cloud install when you need them.

| You want to | Go to |
| --- | --- |
| Try AIPlex now, with no Docker, cloud account or API key | [Run it locally in a minute](./local-development.md) |
| Run the API against local emulators and Ory services | [Local stack with Docker](./docker-stack.md) |
| Provision a platform on GCP | [Install the platform](./install-platform.md) |
| Deploy a tool or apply a declarative file on an existing platform | [Deploy a tool](./first-deploy.md) |
| Point an IDE or CLI at an access point | [Connect a client](./connect-client.md) |

## Prerequisites

For `aiplex dev`:

- Go 1.25 or newer (the requirement in `go.mod` takes precedence over the older Go entry in `.mise.toml`).
- `curl`.

Only for the later paths:

- Node 22 to build the Console.
- Docker Compose for the local dependency stack.
- GCP project credentials for the platform install. `aiplex init` installs the deployment tools (Terraform, Helm, kubectl) that `.mise.toml` pins.

Clone the repository:

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
```

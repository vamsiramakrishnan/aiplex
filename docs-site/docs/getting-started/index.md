---
title: Getting started
description: "Choose a first path: run AIPlex locally, install the platform on GCP, or use an existing platform."
---

# Getting started

Start with one interaction and its identity policy, then add modules.

| You want to | Go to |
| --- | --- |
| Run the API and Console on your machine | [Local development](./local-development.md) |
| Provision a platform on GCP | [Install the platform](./install-platform.md) |
| Deploy a tool or apply a declarative file on an existing platform | [Deploy a tool](./first-deploy.md) |
| Point an IDE or CLI at an access point | [Connect a client](./connect-client.md) |

## Prerequisites

- Go 1.25 or newer (the requirement in `go.mod` takes precedence over the older Go entry in `.mise.toml`).
- Node 22 for the Console.
- Docker Compose for the local dependency stack.
- For the platform install: GCP project credentials. `aiplex init` installs the deployment tools (Terraform, Helm, kubectl) that `.mise.toml` pins.

Clone the repository:

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
```

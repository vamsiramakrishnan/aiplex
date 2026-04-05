---
sidebar_position: 2
title: Installation
description: Install the AIPlex CLI and set up your development environment.
---

# Installation

## CLI Installation

### Quick Install (Recommended)

```bash
curl -fsSL https://get.aiplex.dev | sh
```

This downloads a static binary (~50MB) that includes embedded Terraform, Helm, and kubectl. No external dependencies.

### Homebrew (macOS/Linux)

```bash
brew install vamsiramakrishnan/tap/aiplex
```

### Go Install

```bash
go install github.com/vamsiramakrishnan/aiplex/cmd/aiplex-cli@latest
```

Requires Go 1.24+.

### Verify Installation

```bash
aiplex --version
aiplex doctor
```

`aiplex doctor` checks your environment and reports any issues:

```
✓ CLI version 0.1.0
✓ GCP credentials found
✓ kubectl configured
✓ AIPlex API reachable at https://aiplex.example.com
✓ All checks passed
```

## Local Development

For contributing to AIPlex or running it locally:

### Prerequisites

- Go 1.24+
- Docker & Docker Compose
- Node.js 20+ (for the console)

### Clone and Build

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make deps    # Check all dependencies
make build   # Build API server and CLI
```

### Run Locally

```bash
# Start backing services (Firestore emulator, PostgreSQL, Ory Hydra/Kratos, OPA)
make docker-up

# Start the API server
make run-local

# In another terminal, start the console dev server
make console-dev
```

The local stack:

| Service | Port | Purpose |
|---------|------|---------|
| AIPlex API | 8080 | REST API |
| Console | 5173 | React dev server |
| Firestore Emulator | 8086 | Document database |
| PostgreSQL | 5432 | Ory Hydra/Kratos DB |
| Ory Hydra (public) | 4444 | OAuth 2.1 endpoints |
| Ory Hydra (admin) | 4445 | Admin API |
| Ory Kratos (public) | 4433 | Identity endpoints |
| Ory Kratos (admin) | 4434 | Admin API |
| OPA | 8181 | Policy engine |

### Environment Variables

Copy the example env file:

```bash
cp .env.example .env
```

Key variables:

```bash
# API Server
AIPLEX_HOST=0.0.0.0
AIPLEX_PORT=8080
AIPLEX_LOG_LEVEL=debug

# Auth
HYDRA_ADMIN_URL=http://localhost:4445

# Storage
FIRESTORE_EMULATOR_HOST=localhost:8086
GCP_PROJECT_ID=aiplex-dev

# Identity
TRUST_DOMAIN=aiplex-dev.svc.id.goog
```

### Running Tests

```bash
make test              # All tests
make test-coverage     # With coverage report
```

## Platform Setup (Production)

To set up AIPlex on GCP for production use:

```bash
aiplex login
aiplex platform setup
```

This provisions GKE Autopilot, Cloud Service Mesh, Firestore, AlloyDB, and all required infrastructure. See [Platform Setup](/docs/getting-started/platform-setup) for the full guide.

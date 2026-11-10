---
title: Local stack with Docker
description: Start the Firestore emulator, PostgreSQL, Ory Hydra and Kratos and OPA with Docker Compose, and run the API and Console against them.
---

# Local stack with Docker

Use this page when you need the API to run against the same kinds of services a deployment uses (Firestore emulator, PostgreSQL, Ory Hydra and Kratos, OPA), for example to work on the registry or identity code. For a first look at AIPlex, use [Run it locally in a minute](./local-development.md) instead: it needs only Go and has no Docker dependency.

This path runs the API in development mode against local emulators. Development mode uses development authentication and requires a loopback listener. Outside a cluster, Kubernetes deployment is a no-op, so local success does not establish cloud routing or provider access.

## Build

```bash
make build
export PATH="$PWD/bin:$PATH"
aiplex --help
```

`make build` writes `bin/aiplex-api` and `bin/aiplex`. `make console` builds the Console into `console/build/` (it runs `npm ci && npm run build` in `console/`).

## Start the dependency stack

```bash
make docker-up
```

`docker-compose.yml` starts the Firestore emulator (port 8086), PostgreSQL (5432), Ory Hydra (4444 public, 4445 admin), Ory Kratos (4433 public, 4434 admin) and OPA (8181).

## Run the API

The API reads its settings from environment variables (see [Configuration](../operations/configuration.md)). For development mode the host must be loopback:

```bash
export AIPLEX_DEV_MODE=true HOST=127.0.0.1
export FIRESTORE_EMULATOR_HOST=127.0.0.1:8086 GCP_PROJECT=aiplex-dev
export HYDRA_ADMIN_URL=http://127.0.0.1:4445 HYDRA_PUBLIC_URL=http://127.0.0.1:4444
export KRATOS_ADMIN_URL=http://127.0.0.1:4434 KRATOS_PUBLIC_URL=http://127.0.0.1:4433
make run-local
```

The API listens on port 8080 by default. Check it from another terminal:

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

`make run-local` also sets `AIPLEX_HOST` and `AIPLEX_PORT`; the API does not read those names. The listener is controlled by `HOST` and `PORT`, which is why `HOST=127.0.0.1` is exported above.

## Use the CLI against it

```bash
aiplex config set-context dev --url http://127.0.0.1:8080
aiplex config use-context dev
aiplex whoami
aiplex health
```

`aiplex tui` opens the terminal dashboard and `aiplex console` launches the web Console locally.

## Run the Console in development

In another terminal, from the repository root:

```bash
npm --prefix console ci
npm --prefix console run dev -- --host 127.0.0.1
```

## Stop and clean up

```bash
make docker-down    # stop the stack
make docker-clean   # stop and remove local data
```

## Other useful targets

`make test` runs the Go tests, `make lint` runs `go vet`, and `make help` lists every target. `./setup.sh` installs the pinned development tools with mise and runs `aiplex init`.

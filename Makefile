# AIPlex — Unified Control Plane for AI Agent Interactions
#
# Quick start (Go only; no Docker or cloud account):
#   make build && bin/aiplex dev   Local API + mock model + mock MCP server
#   make demo         Scripted end-to-end run of `aiplex dev` (CI-safe)
#   make e2e          End-to-end tests against the real binaries
#   make examples-test  Run the examples in examples/ against `aiplex dev`
#   make deps         Check the tools each workflow needs
#   make test         Run all tests
#
# Full local stack (Docker: Firestore emulator, Postgres, Hydra, Kratos, OPA):
#   make dev          Start local services + API
#
# GCP deployment:
#   make infra        Terraform apply
#   make deploy       Helm install
#   make verify       Health check + status

.PHONY: help build test lint run-local dev deps clean demo e2e e2e-race examples-test \
        infra infra-plan infra-destroy deploy deploy-dry verify \
        docker-up docker-down console

# ─── Configuration ──────────────────────────────────────

BINARY_API  := bin/aiplex-api
BINARY_CLI  := bin/aiplex
GO          := go
GOFLAGS     := -trimpath
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# ─── Help ───────────────────────────────────────────────

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ─── Build ──────────────────────────────────────────────

build: build-api build-cli ## Build all binaries

build-api: ## Build AIPlex API server
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_API) ./cmd/aiplex-api

build-cli: ## Build AIPlex CLI
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_CLI) ./cmd/aiplex-cli

install-cli: build-cli ## Install CLI to $GOPATH/bin
	cp $(BINARY_CLI) $(shell $(GO) env GOPATH)/bin/aiplex

# ─── Test ───────────────────────────────────────────────

test: ## Run all tests
	$(GO) test ./... -v -count=1

test-short: ## Run tests (short mode)
	$(GO) test ./... -short

test-coverage: ## Run tests with coverage report
	$(GO) test ./... -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

demo: ## Scripted first run of `aiplex dev`: chat, stream, MCP tool call, usage, connect (exits non-zero on failure)
	@bash tools/demo.sh

e2e: ## End-to-end tests: real aiplex-api + aiplex CLI against local mock upstreams
	$(GO) test -tags e2e ./tests/e2e/ -count=1 -v

e2e-race: ## End-to-end tests with aiplex-api built with the race detector
	E2E_RACE=1 $(GO) test -tags e2e ./tests/e2e/ -count=1 -v

examples-test: ## Run the examples in examples/ against `aiplex dev` (EXAMPLES_INSTALL_SDKS=1 installs the OpenAI SDKs temporarily)
	@bash examples/test.sh

e2e-aiplex-tape: ## Run the AIPlex+Tape end-to-end demo as an in-process smoke test
	$(GO) test ./tests/... -run "AIPlexTape" -v -count=1

# ─── Lint ───────────────────────────────────────────────

lint: ## Run Go vet
	$(GO) vet ./...

# ─── Run ────────────────────────────────────────────────

run-local: ## Run API server locally (port 8080)
	HOST=0.0.0.0 PORT=8080 LOG_LEVEL=debug \
		$(GO) run ./cmd/aiplex-api

dev: docker-up run-local ## Start local deps + API server

# ─── Dev with Tape (PR 11 item 14) ──────────────────────
#
# Brings up the full AIPlex ↔ Tape pipeline locally:
#   1. Builds tape-server from the sibling durable-agents checkout
#      (or skips with a clear error if it's not there).
#   2. Starts tape-server on 127.0.0.1:7878 in the background.
#   3. Starts the AIPlex API with TAPE_URL + TAPE_INGEST_TOKEN wired.
# The user runs the agent + sink separately (see
# examples/aiplex-tape-treasury/README.md). Ctrl-C kills both.
TAPE_REPO ?= ../durable-agents
TAPE_BIN  ?= $(TAPE_REPO)/tape/server/target/release/tape-server
TAPE_INGEST_TOKEN ?= dev-token-aiplex-tape

dev-tape: ## Start AIPlex API with tape-server alongside (full pipeline locally)
	@if [ ! -d "$(TAPE_REPO)" ]; then \
		echo "✗ Tape repo not at $(TAPE_REPO). Clone vamsiramakrishnan/durable-agents alongside this repo."; \
		exit 1; \
	fi
	@if [ ! -x "$(TAPE_BIN)" ]; then \
		echo "→ Building tape-server (one-time)..."; \
		cd $(TAPE_REPO)/tape/server && cargo build --release; \
	fi
	@echo "→ Starting tape-server on 127.0.0.1:7878..."
	@$(TAPE_BIN) --listen 127.0.0.1:7878 --store "sqlite:/tmp/aiplex-dev-tape.db" >/tmp/tape-server.log 2>&1 & \
		TAPE_PID=$$!; \
		echo "  tape-server PID=$$TAPE_PID (logs: /tmp/tape-server.log)"; \
		trap "echo '→ Stopping tape-server'; kill $$TAPE_PID 2>/dev/null" EXIT INT TERM; \
		sleep 1; \
		echo "→ Starting AIPlex API with TAPE_URL + TAPE_INGEST_TOKEN wired..."; \
		HOST=0.0.0.0 PORT=8080 LOG_LEVEL=debug \
		TAPE_URL=tape://127.0.0.1:7878 \
		TAPE_INGEST_TOKEN=$(TAPE_INGEST_TOKEN) \
		TAPE_INGEST_RPS=1000 \
		$(GO) run ./cmd/aiplex-api

# ─── Docker Compose (Local Dev) ─────────────────────────

docker-up: ## Start local development services
	docker compose up -d
	@echo ""
	@echo "Services running:"
	@echo "  Firestore: localhost:8086"
	@echo "  PostgreSQL: localhost:5432"
	@echo "  Hydra:      localhost:4444 (public), localhost:4445 (admin)"
	@echo "  Kratos:     localhost:4433 (public), localhost:4434 (admin)"
	@echo "  OPA:        localhost:8181"

docker-down: ## Stop local development services
	docker compose down

docker-clean: ## Stop and remove all local data
	docker compose down -v

# ─── Console (React) ────────────────────────────────────

console: ## Build React console
	cd console && npm ci && npm run build
	@echo "Console built: console/dist/ (run make build-cli to embed it in bin/aiplex)"

console-dev: ## Start React dev server
	cd console && npm start

# ─── Infrastructure (Terraform) ─────────────────────────

infra-plan: ## Preview infrastructure changes
	cd deploy/terraform && terraform init && terraform plan

infra: ## Apply infrastructure (GKE, AlloyDB, Firestore, etc.)
	cd deploy/terraform && terraform init && terraform apply

infra-destroy: ## Destroy all infrastructure (use with caution)
	@echo "This will destroy ALL GCP resources. Ctrl+C to abort."
	@sleep 5
	cd deploy/terraform && terraform destroy

# ─── Deploy (Helm) ──────────────────────────────────────

deploy-dry: ## Dry-run Helm install
	helm install aiplex deploy/helm/aiplex \
		--namespace aiplex-system --create-namespace \
		--dry-run --debug

deploy: ## Deploy AIPlex to GKE via Helm
	helm upgrade --install aiplex deploy/helm/aiplex \
		--namespace aiplex-system --create-namespace \
		--wait --timeout 10m

deploy-dev: ## Deploy with dev overlay values
	helm upgrade --install aiplex deploy/helm/aiplex \
		--namespace aiplex-system --create-namespace \
		--values deploy/helm/aiplex/values.yaml \
		--values deploy/helm/aiplex/values-dev.yaml \
		--wait

deploy-prod: ## Deploy with prod overlay values
	helm upgrade --install aiplex deploy/helm/aiplex \
		--namespace aiplex-system --create-namespace \
		--values deploy/helm/aiplex/values.yaml \
		--values deploy/helm/aiplex/values-prod.yaml \
		--wait --timeout 10m

undeploy: ## Remove AIPlex from GKE
	helm uninstall aiplex --namespace aiplex-system

# ─── Verify ─────────────────────────────────────────────

verify: build-cli ## Run health check + status
	./$(BINARY_CLI) health
	@echo ""
	./$(BINARY_CLI) status

# ─── Dependencies ───────────────────────────────────────

setup: ## Install tools + CLI, run aiplex init
	@chmod +x setup.sh && ./setup.sh

deps: ## Check the tools each workflow needs
	@need=$$(sed -n 's/^go \([0-9.]*\).*/\1/p' go.mod); \
	if command -v go >/dev/null 2>&1; then \
		have=$$(go env GOVERSION | sed 's/^go//'); \
		if [ "$$(printf '%s\n%s\n' "$$need" "$$have" | sort -V | head -1)" = "$$need" ]; then echo "  [ok]   go $$have (go.mod needs $$need)"; \
		else echo "  [FAIL] go $$have is older than $$need (go.mod); install a newer Go: https://go.dev/dl"; fi; \
	else echo "  [FAIL] go not found; install Go $$need+: https://go.dev/dl"; fi
	@echo "  Local (aiplex dev, make demo, make e2e): Go only."
	@for t in node:"Console build (make console)" docker:"make dev" terraform:"cloud infrastructure" helm:"cloud deploy" kubectl:"cloud deploy" gcloud:"cloud deploy"; do \
		name=$${t%%:*}; use=$${t#*:}; \
		if command -v $$name >/dev/null 2>&1; then echo "  [ok]   $$name ($$use)"; else echo "  [--]   $$name not found (only needed for $$use)"; fi; \
	done

# ─── Release ────────────────────────────────────────────

release-check: ## Validate .goreleaser.yaml
	goreleaser check

release-dry: ## Build release archives locally into dist/ (no publish)
	goreleaser release --snapshot --clean --skip=publish

release: ## Publish a release from the current tag (CI does this on a v* tag; requires GITHUB_TOKEN)
	goreleaser release --clean

# ─── Clean ──────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -rf bin/ aiplex-api aiplex-cli dist/ coverage.out coverage.html
	$(GO) clean -cache

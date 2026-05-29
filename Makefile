# AIPlex — Unified Control Plane for AI Agent Interactions
#
# Quick start:
#   make deps         Install tools
#   make dev          Start local services + API
#   make test         Run all tests
#   make build        Build binaries
#
# GCP deployment:
#   make infra        Terraform apply
#   make deploy       Helm install
#   make verify       Health check + status

.PHONY: help build test lint run-local dev deps clean \
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

e2e-aiplex-tape: ## Run the AIPlex+Tape end-to-end demo as an in-process smoke test
	$(GO) test ./tests/... -run "AIPlexTape" -v -count=1

# ─── Lint ───────────────────────────────────────────────

lint: ## Run Go vet
	$(GO) vet ./...

# ─── Run ────────────────────────────────────────────────

run-local: ## Run API server locally (port 8080)
	AIPLEX_HOST=0.0.0.0 AIPLEX_PORT=8080 LOG_LEVEL=debug \
		$(GO) run ./cmd/aiplex-api

dev: docker-up run-local ## Start local deps + API server

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
	@echo "Console built: console/build/"

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

deps: ## Check installed tools
	@command -v mise >/dev/null 2>&1 && mise ls --current || echo "Run: make setup"

# ─── Release ────────────────────────────────────────────

release-dry: ## Preview release (no publish)
	goreleaser build --snapshot --clean

release: ## Tag and release (requires GITHUB_TOKEN)
	goreleaser release --clean

# ─── Clean ──────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -rf bin/ aiplex-api aiplex-cli dist/ coverage.out coverage.html
	$(GO) clean -cache

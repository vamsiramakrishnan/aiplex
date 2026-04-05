---
sidebar_position: 100
title: Contributing
description: How to contribute to AIPlex.
---

# Contributing

AIPlex is open source under the Apache 2.0 license. Contributions are welcome.

## Development Setup

```bash
# Clone
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex

# Check dependencies
make deps

# Start local services
make docker-up

# Build and run
make build
make run-local

# In another terminal, start the console
make console-dev
```

See [Installation](/docs/getting-started/installation) for detailed local setup.

## Project Structure

```
aiplex/
├── cmd/
│   ├── aiplex-api/       # API server binary
│   └── aiplex-cli/       # CLI binary
├── internal/             # Core Go packages
│   ├── api/              # HTTP handlers
│   ├── auth/             # Hydra/Kratos clients, consent, token hook
│   ├── catalog/          # Catalog sources and aggregation
│   ├── deploy/           # Deploy engine, manifests, routes
│   ├── models/           # Domain types
│   └── registry/         # Firestore CRUD, subregistry
├── authz/                # Rust ext_authz service
├── console/              # React SPA
├── sdk/                  # Go SDK client
├── deploy/               # Infrastructure (Terraform, Helm, K8s, Ory)
├── policies/             # OPA/Rego policies
├── examples/             # Example YAML configurations
├── design/               # Architecture design documents
├── docs-site/            # This documentation (Docusaurus)
└── tests/                # Integration tests
```

## Running Tests

```bash
make test              # All Go tests
make test-coverage     # With coverage report
```

## Documentation

The documentation site is in `docs-site/` and built with Docusaurus:

```bash
cd docs-site
npm install
npm start              # Dev server at localhost:3000
npm run build          # Production build
```

## Code Style

- **Go**: `gofmt` + `golint`. Run `make lint`.
- **TypeScript/React**: ESLint + Prettier. Run `npm run lint` in `console/`.
- **Rust**: `cargo fmt` + `cargo clippy`.

## Pull Requests

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with tests
4. Run `make test` and `make lint`
5. Submit a PR with a clear description

## Areas for Contribution

- Catalog sources (new registry integrations)
- MCP server templates
- CLI improvements
- Console UI features
- Documentation improvements
- Test coverage

## License

Apache 2.0. See [LICENSE](https://github.com/vamsiramakrishnan/aiplex/blob/main/LICENSE).

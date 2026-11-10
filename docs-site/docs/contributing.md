---
title: Contributing
description: Build, test and contribute to AIPlex and this documentation site.
---

# Contributing

AIPlex is open source under the Apache 2.0 license.

## Set up

Follow [Local development](./getting-started/local-development.md) to build and run the API, CLI and Console.

## Repository layout

```text
cmd/           Go binaries: API server and CLI
internal/      Core packages: API, auth, catalog, deploy, registry
pkg/           Reusable libraries (modules, QuotaPlex, model API, identity)
authz/         Rust ext_authz service
console/       React application
sdk/           Go SDK
deploy/        Terraform, Helm, Kubernetes manifests, Ory config
policies/      Rego authorization policy
examples/      Example configuration files
design/        Architecture documents
docs/          Code-verified feature docs (rendered on this site)
docs-site/     This documentation site (Docusaurus)
```

## Tests

```bash
make test          # all Go tests
make test-short
make lint          # go vet
```

The Rust service has its own tests (`cargo test` in `authz/`), and the Console runs `npm --prefix console test`.

## Documentation

Feature documentation lives in `docs/*.md` and is the single source of truth. The site renders those files; do not copy them into `docs-site/`. Hand-written pages (overview, getting started, concepts, guides, operations) live in `docs-site/docs/`. See `docs-site/README.md` for the build, the link rewriting and how the CLI reference is generated.

## Pull requests

1. Create a branch from `main`.
2. Make your change with tests.
3. Run `make test` and `make lint`.
4. Open a pull request with a clear description.

## License

Apache 2.0. See [LICENSE](https://github.com/vamsiramakrishnan/aiplex/blob/main/LICENSE).

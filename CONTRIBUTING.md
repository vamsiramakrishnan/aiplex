# Contributing to AIPlex

Thanks for looking. AIPlex is pre-1.0 and maintained by one person, so small, focused changes with tests are the easiest to review.

By contributing you agree that your contribution is licensed under the [Apache License 2.0](LICENSE) and that you will follow the [code of conduct](CODE_OF_CONDUCT.md). Report security issues privately, as described in [SECURITY.md](SECURITY.md).

## Build and run

You need Go 1.25 or newer (the version in `go.mod` wins) and `curl`. Run `make deps` to see which other tools each workflow needs.

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build                      # bin/aiplex and bin/aiplex-api
export PATH="$PWD/bin:$PATH"
aiplex dev                      # local API, mock model and MCP server on 127.0.0.1:8080
```

In a second terminal, `eval "$(aiplex dev env)"` exports `AIPLEX_URL`, `AIPLEX_TOKEN` and the SDK variables. `aiplex dev` is unauthenticated and loopback-only; do not expose it.

## Test

```bash
go build ./... && go vet ./...
go test ./... -count=1          # unit and integration tests (CI adds -race)
make e2e                        # real aiplex-api and aiplex binaries against local mock upstreams
make demo                       # scripted first run of `aiplex dev`; exits non-zero on failure
make examples-test              # every example in examples/, against `aiplex dev`
```

`make e2e-race` builds the API with the race detector. Some tests (the Firestore emulator lane) skip unless `FIRESTORE_EMULATOR_HOST` is set; see [production qualification](docs/production-qualification.md) to run them.

### Console

```bash
npm --prefix console ci
npm --prefix console test
npm --prefix console run build   # tsc, then vite build into console/dist
```

Build the Console before `make build` if you want it embedded in the CLI (`aiplex dev` serves it when present).

### Rust authorization service

```bash
cd authz
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
```

### Documentation site

```bash
cd docs-site
npm ci
npm run build          # fails on broken links and anchors
npm run check:links
npm run typecheck
```

Feature documentation lives in `docs/*.md` and is the single source of truth; the site renders it. Hand-written pages are in `docs-site/docs/`. Do not copy `docs/` files into the site. See [docs-site/README.md](docs-site/README.md).

### Brand assets

Files under `brand/` are generated or checked by scripts; read [brand/README.md](brand/README.md) first. `python3 brand/scripts/validate.py` must pass.

## Conventions

- Fail closed: a missing policy, price, identity or upstream answer refuses the request.
- Unknown is never zero: record and show missing usage or cost as unknown.
- Secrets are references, never values, and must not reach logs, responses or the usage ledger.
- The tenant comes from the verified identity only.
- Every new API route needs an entry in `cmd/aiplex-api/route_policy.go`, with a test.
- Change behaviour and its guide in `docs/` together. State limits plainly; do not add performance claims without a recorded measurement.

[CLAUDE.md](CLAUDE.md) has the same orientation in short form.

## Commits and pull requests

The history uses short, imperative commit subjects with an optional area prefix, for example:

```text
Add make demo, make e2e and make e2e-race; make deps checks what each workflow needs
access points: report reference lookup outages as unavailable
brand: add a dark brand sheet
docs: add network and platform API reference
CLI: model surfaces, provider catalog, capacity, batches, usage units and modality
```

Use a body to explain why when it is not obvious. Keep a change, its tests and its docs in one commit or one pull request.

Pull requests are merged from a topic branch with a merge commit (`Merge pull request #N from ...`). To open one:

1. Branch from `main`.
2. Make the change with tests, and run the checks above for the parts you touched.
3. Fill in the pull request template: what changed, why, and how you verified it. Say what you did not verify.
4. Keep it small enough to review; open an issue first for a large change.

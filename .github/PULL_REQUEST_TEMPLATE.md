## What changed and why

<!-- One or two sentences. Link the issue if there is one. -->

## How you verified it

<!-- Commands you ran and what they showed. Say what you did not verify (for example, no live provider). -->

- [ ] `go build ./... && go vet ./...`
- [ ] `go test ./... -count=1` (and `make e2e` or `make demo` if the change affects `aiplex dev` or the CLI)
- [ ] Console, `authz/` or `docs-site/` checks, if you touched them (see CONTRIBUTING.md)

## Checklist

- [ ] New API routes have an entry in `cmd/aiplex-api/route_policy.go` and a test
- [ ] Docs under `docs/` are updated with the behaviour, including limits
- [ ] No secrets, tokens or customer data in code, tests, logs or fixtures
- [ ] Unknown values stay unknown (nothing defaults a missing count or price to zero)

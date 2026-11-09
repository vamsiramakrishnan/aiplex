# Standalone external module

This command demonstrates the public module contract without importing an
Aiplex `internal` package or requiring its API server, database, identity
provider, Kubernetes cluster, or any Plex. It has no quota engine.

From the repository root, start the independently deployed service:

```bash
go run ./examples/modules/echo
```

Probe its descriptor/readiness and invoke its bounded example API:

```bash
curl --fail http://127.0.0.1:9199/module/v1/descriptor
curl --fail http://127.0.0.1:9199/readyz
curl --fail -X POST --data 'hello' http://127.0.0.1:9199/echo
```

In another terminal, connect an Aiplex development host to the module:

```bash
export AIPLEX_DEV_MODE=true
export HOST=127.0.0.1
export AIPLEX_MODULE_MANIFEST=examples/modules/echo/modules.json
go run ./cmd/aiplex-api
```

The host validates the pinned descriptor, starts its remote lifecycle adapter,
and includes this module in discovery/readiness. Startup fails when the module
is unavailable or incompatible. Invoke `/echo` directly at the module endpoint;
the host does not automatically proxy or register external APIs. Remote modules
advertise no host API prefixes until an explicit trusted API adapter is supplied.

This example listens on loopback and uses unauthenticated HTTP for local
development. Production deployment needs an HTTPS endpoint, authenticated host
transport, and ingress/egress policy; remove `allow_insecure_localhost`. Module
discovery metadata never contains credentials.

The HTTP contract, body limit, and methods are smoke-tested with:

```bash
go test -race ./examples/modules/echo
```

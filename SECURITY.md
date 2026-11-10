# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately. Do not open a public issue or pull request for them.

Use GitHub's private vulnerability reporting for this repository: open the repository's **Security** tab and choose **Report a vulnerability** (a GitHub security advisory). Include:

- what you found and what an attacker could do with it,
- the version or commit, and how you ran AIPlex (`aiplex dev`, a cluster, which configuration),
- steps or a proof of concept that reproduce it.

The project is maintained by one person in their own time. Expect an acknowledgement within a few days and a status update after triage; I cannot promise a fix date. Reports are credited in the advisory unless you ask otherwise.

## Supported versions

AIPlex is pre-1.0. Security fixes go to the latest release and to `main`. There are no long-term-support branches.

## What is in scope

- The API (`cmd/aiplex-api`, `internal/`, `pkg/`): authentication and authorization bypass, cross-tenant access, route-policy gaps, leaks of credentials into logs, responses or the usage ledger, request forgery against upstreams, and unsafe handling of provider responses.
- The CLI (`cmd/aiplex-cli`), including `aiplex dev` (for example, development mode becoming reachable from a non-loopback address).
- The Rust authorization service (`authz/`) and the Rego policy (`policies/`).
- The Console (`console/`) and the Helm chart, Kubernetes and Terraform configuration under `deploy/` where they weaken a default.
- Release artifacts and install scripts (`install.sh`, `.goreleaser.yaml`, `.github/workflows/`).

Out of scope: vulnerabilities in third-party services AIPlex integrates with (report those upstream), findings that need a development-mode server to be deliberately exposed to a network, denial of service by a caller who is already authorized to send unlimited traffic, and issues in documentation examples that use placeholder values.

## Current limits of the security model

These are known and documented, not undisclosed vulnerabilities. Do not rely on AIPlex beyond them.

- **Development mode is unauthenticated and loopback-only.** `aiplex dev` and `AIPLEX_DEV_MODE=true` serve unauthenticated requests as an admin. The API refuses to start in development mode on a non-loopback address. Never expose, tunnel or proxy a development-mode server, and never enable it in a cluster.
- **Local state is in memory.** `aiplex dev` discards everything on stop and prices shown in it are placeholders.
- **Rate limits and capacity gates are per process.** The API's request rate limiter is an in-process token bucket. Provider-limit observations and the capacity gate are per replica, in memory, advisory and forgotten on restart. With several replicas, each learns only from its own traffic. Use the gateway's rate-limit service for shared limits.
- **Quota is opt-in and covers managed paths only.** QuotaPlex protects only the execution paths listed in the [execution coverage matrix](docs/quotaplex-execution-coverage.md). Envoy routes and direct provider calls do not pass through it. Production quota needs the shared durable store; a process-local store is accepted only in development mode.
- **Some caches are process-local.** The directory group cache and the membership revocation hook are not distributed; revocation is not propagated across replicas by an event stream.
- **Unknown values stay unknown.** Missing token counts, prices and usage are recorded as unknown, not zero, so budgets and cost totals can understate consumption on unknown exposure. See the [usage ledger](docs/usage-ledger.md).
- **No live-service validation is recorded.** The repository's tests use local mocks, fixtures built from provider documentation and the Firestore emulator. No run against live providers, managed Firestore or a production cluster, and no independent security review or penetration test, is recorded here.
- **Registration is not authorization to execute.** Registering a tool, skill or client does not by itself grant a caller use of it. Skill invocation reports are records, not proof that an action was authorized.

For the authorization model and configuration, see [Authorization policy](docs-site/docs/operations/authorization-policy.md) and the [production qualification](docs/production-qualification.md) notes.

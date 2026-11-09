# Plex cohesion bugbash

November 9, 2026. This pass starts from merged PR #10 and tests the contracts
between modules, native transports, the SDK and Console. It preserves the
accounting core, protocol ownership and opt-in production enforcement.

## Reproduced defects and fixes

| ID | Counterexample | Repair | Regression evidence |
| --- | --- | --- | --- |
| PB01 | A saved native `anthropic.messages` surface could not resolve as a ClientPlex destination; its facade rewrote requests into an uninstalled Vertex operation and returned 404 | Resolve the actual installed operation; native Messages retains its path/body/headers, while explicitly installed Vertex operations use translation | `TestBugbashMessagesFacadeUsesInstalledNativeMessagesOperation`; `TestBugbashClientPlanReachesProtectedNativeMessages` |
| PB02 | Vertex Messages translation decoded/re-encoded duplicate model, output-bound or nested role fields before native validation; an invalid stream type silently selected non-streaming execution | Reuse native JSON validation before normalization and validate the stream selector | `TestBugbashVertexFacadeRejectsAmbiguityBeforeReencoding`; `TestBugbashVertexFacadeAmbiguityCannotReserveQuota` |
| PB03 | Valid `Application/JSON` and `Text/Event-Stream` responses reached the provider but failed protected native observation/settlement | Parse MIME types with the standard parser; preserve original response headers and bytes | `TestBugbashManagedModelAcceptsCaseInsensitiveNativeMediaTypes` |
| PB04 | Failed consumer cleanup stopped its storage/identity dependencies; subsequent cleanup could never finish | Retain the failed consumer's transitive dependency graph, stop unrelated modules and retry pending cleanup in dependency order | `TestBugbashFailedConsumerCleanupRetainsTransitiveDependencies`; `TestBugbashCleanupGraphMatchesDependencyRetentionModel` |
| PB05 | SDK mutation responses 304/307/308 returned success without an acknowledged mutation | Treat only 2xx responses as success and retain error status | `TestBugbashSDKDoesNotAcknowledgeNonSuccessMutation` |
| PB06 | Late plan responses replaced newer plans or reappeared after New; a completed save reselected an old editor, and deleting one registration reset another registration's editor | Bind asynchronous results to the original UI intent; snapshot submitted editor state; always refresh saved inventory while preserving the current editor | Console `clients_bugbash.test.tsx` |
| PB07 | A JSON-RPC object containing both `method`/`params` and a matching result was accepted as terminal tool evidence in JSON and finite SSE, releasing held exposure | Reject request members in the response envelope while allowing ordinary nested result data; uncertain execution retains holds | `TestBugbashToolRequestShapeCannotAcknowledgeResponse`; `TestBugbashRPCResponseRejectsRequestMembers` |

PB01–PB07 were reproduced with failing tests before their repairs. Positive
cases remain explicit: native Messages HTTP/SSE, existing Vertex translation,
valid RPC result/error payloads, SDK CRUD/CAS through the mounted Chi module,
successful cleanup retries and existing Console revision-aware editing.

## Stronger coverage

- **Plan to actual execution:** resolve a tenant-owned destination, render the
  native client configuration, check grants, admit through QuotaPlex, invoke a
  local HTTP/SSE provider, settle one exchange, reject a replay and deny the next
  exhausted-budget call. Rendering a plan grants no execution authority.
- **Before side effects:** malformed/ambiguous translated requests acquire no
  quota reservation or provider credential. Incomplete or ambiguous tool replies
  remain unknown and retain spending/concurrency exposure.
- **Dependency model:** 64 deterministic generated eight-module DAGs, including
  present optional dependencies and multiple simultaneous cleanup failures, are
  compared with an independent retention-closure model. Successful cleanup must
  not repeat; a retry must retain every needed provider.
- **SDK through real routing:** the public Go SDK creates, reads, plans, updates
  with CAS, rejects stale revisions and deletes through the actual mounted
  ClientPlex routes. No test manually populates router parameters.
- **Protocol fuzzing:** accepted Vertex translations must retain unrelated native
  fields and exact numeric values, preserve the pinned target, and reject hidden
  duplicate fields. Accepted tool responses must identify the original RPC and
  cannot contain server-request authority.
- **UI races:** deferred responses are resolved in adversarial order, after
  starting a new registration, and after changing the selected editor. Tests
  inspect the displayed plan/editor rather than only mocked request arguments.

## Validation

Local results: **1,264 Go tests/subtests pass with the race detector**, **58
Console tests pass**, and both protocol fuzz targets pass their final 10-second
runs (28,702 client-translation executions and 164,137 tool-response executions).
The cleanup model covers 64 generated graphs. Go vet, API/CLI builds and the
Console production build pass. Nine Firestore emulator-only tests skip locally;
the dedicated CI lane supplies that separate check.

Full suite and focused fuzz commands:

```sh
go test -race ./... -count=1 -json
go vet ./...
go build -buildvcs=false -trimpath -o /tmp/aiplex-bugbash-api ./cmd/aiplex-api
go build -buildvcs=false -trimpath -o /tmp/aiplex-bugbash-cli ./cmd/aiplex-cli
go test ./internal/api -run '^$' -fuzz '^FuzzBugbashVertexMessagesTranslation$' -fuzztime=10s -parallel=4
go test ./pkg/toolexec -run '^$' -fuzz '^FuzzBugbashToolResponseIdentity$' -fuzztime=10s -parallel=4
cd console
npm test
npm run build
```

The complete Go pass includes the existing quota/recovery, A2A/Skills history,
native-governance, delegated OAuth, storage, CLI and configuration regressions.
The Console pass includes its existing quota, governance, session and SSE tests.
Emulator-only cases require the dedicated Firestore emulator CI lane and skip in
an ordinary local run without `FIRESTORE_EMULATOR_HOST`.

All 13 pre-existing README diagram/example blocks remain byte-for-byte intact.
The README now names all seven Plex/control modules, explains their ownership,
and includes a separate ClientPlex setup diagram beside the QuotaPlex execution
diagram. Internal documentation links resolve.

These are local mechanism/protocol tests, not native-client certification,
provider invoice equivalence, deployed routing coverage or production load/restore
evidence. No production enforcement, cloud deployment or provider writes were
enabled. The native provider fixtures run locally.

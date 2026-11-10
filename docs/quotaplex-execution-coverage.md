# QuotaPlex execution contracts and evidence

This page states the contracts of the managed QuotaPlex integration and the
evidence that covers them: model and tool tests over separate host/engine
instances sharing one transactional store, and cumulative usage replacement
inside an SSE final tail. It does not change defaults, enable a production policy
or deploy resources.

## Ownership and integration contracts

LLMPlex owns native model validation, protocols and usage normalization. MCPlex
owns tool execution and JSON-RPC finality. QuotaPlex owns funding, exposure,
admission, claims and settlement. The generic enforcement pipeline composes
admission receipts without interpreting provider JSON or prices.

| Boundary | Contract and implementation | Required facts |
| --- | --- | --- |
| Verified request | [membership resolution](../internal/api/quota_host_membership.go), [host preparation](../internal/api/quota_host_service.go) | Canonical tenant/principal/actor; authoritative, independently fresh memberships; no groups or funding authority inferred from payloads |
| Native adapter | [model preparer](../internal/api/quota_host_model.go), [tool preparer](../internal/api/quota_tool.go) | Exact saved surface/connection revision, native operation and payload; credential origin, billing/region and current grants |
| Accounting attempt | `QuotaInvocation` → `quotaplex.Demand` | Subject-scoped idempotency key, separate logical operation ID, immutable native payload/configuration digest and trusted exposure bounds |
| Admission | [bound provider](../pkg/quotaplex/enforcement.go) through [pipeline](../pkg/enforcement/pipeline.go) | One eligible payer; all matched budgets, admitted-demand rates and concurrency; optional atomic scheduler admission |
| Dispatch | [execution.Lease](../pkg/execution/execution.go), [quota lease](../internal/api/quota_host_lease.go) | Current memberships, grants, configuration and provider-bound evidence rechecked immediately before a one-shot claim; no provider I/O on denial |
| Usage | Native model producer or validated terminal tool RPC → `execution.Observation` | Trusted source; native resource/model proof; explicit cumulative/terminal facts; no EOF/cancellation estimates |
| Cumulative updates | [execution.CumulativeUsage](../pkg/execution/usage.go) | One producer/attempt; monotone known counters; omissions remain unknown; repeats do not add; atomic invalid-update rejection; at most 64 distinct counters across the stream |
| Terminal acknowledgement | [terminal outbox](../pkg/quotaplex/outbox.go) and [recovery](../pkg/quotaplex/recovery.go) | Immutable claim/configuration/producer evidence, original prices and idempotent durable settlement; acknowledgement before releasing final data |

`CumulativeUsage` is shared transport/host validation, not another ledger. Native
adapters must normalize their own counters and convert supported deltas. It does
not infer finality, fill missing counters with zero, invent prices or release a
permit. The native model transport validates each cumulative update even after
it starts withholding final frames; the quota host independently validates its
protected normalized observations. Request-count profiles acquire no new token
enforcement from advisory token telemetry.

## Execution coverage matrix

Every protected entry still requires an exact operator binding and the relevant
identity, meter, price and provider-limit evidence. A protocol adapter alone is
not deployment verification.

| Path or operation | Enforced dimensions in its supported profile | Native semantics and remaining boundary |
| --- | --- | --- |
| Managed Gemini generation, HTTP or SSE | Requests; strict text input/cache/output/thought exposure and configured exact money; admitted-demand rates; concurrency | Native bytes, early SSE frames and terminal framing preserved. Strict final tail requires complete usage/model proof and acknowledgement. Media/tools require additional profiles. |
| Managed OpenAI/Azure Chat/Responses, Anthropic Messages/Vertex/Bedrock InvokeModel, Bedrock Converse, Vertex OpenAI-compatible chat, declared OpenAI-compatible servers and model-backed Interactions (Vertex and Developer API) | Requests; strict text profiles with separate cache-read and cache-write (scalar or per-TTL) meters, exact configured money; admitted-demand rates; concurrency | Provider-specific cache/reasoning composition remains native. Background/media and server-tool billing have no token guarantee (server tools fail closed per counter). See [profile matrix](model-metering-profiles.md). |
| Embeddings (OpenAI, Azure, Gemini Developer API, Vertex embedding predict) | Requests; `embedding-input` input meter and exact money | One attested input ceiling; no output. |
| MCP stateless JSON and bounded finite SSE `tools/call` | Requests/calls, configured fixed call tariff, admitted rates and concurrency | Exact JSON-RPC ID/body, native result/error and supported notifications retained. Terminal RPC is acknowledged before delivery. Rows/bytes/duration and opaque tool internals remain unmetered. |
| Async job submission/read/cancellation under request-count | Managed HTTP exchange count, explicit exchange tariff/rate and exchange concurrency | Job ID/ownership retained. `done:false` is preserved; HTTP completion does not settle job tokens, lifetime, storage or internal work. Those dimensions remain advisory/unsupported. |
| WebSocket sessions, session metering (default) | Request-count: one admitted request and one concurrency permit per session, bounded by `websocket.max_session_seconds` | Token-profile leases are refused before credentials/dial. Usage is advisory; one settlement at session end, labelled `lower_bound`/`ambiguous`/`partial` without complete evidence. No token ceiling. |
| WebSocket sessions, token metering (`websocket.metering: "token"`) | Realtime/Responses: every `response.create` admitted, settled exactly on its terminal event or retained; Live: renewable `token_allowance` windows settled on snapshot deltas | Denials are native error events (Realtime/Responses, session stays open) or goAway + policy close (Live). Live worst-case exposure beyond holds: one allowance per mapped meter. See [WebSocket sessions](model-metering-profiles.md#websocket-sessions). |
| Direct provider, deployed Envoy or external/native client routes | No automatic quota interception | Remain outside the proven protected boundary. Inventory/reporting is advisory; a managed-path test cannot establish enforcement on these routes. |

Request/s and token-exposure/s limit **admitted demand**, not provider-generated
tokens/s. Accepted rate debits are not refunded when actual usage is smaller.
Concurrency includes retained unknown work. Optional scheduling composes queue
selection, quota admission and attested capacity debit atomically, then rechecks
capacity at dispatch. Neither configured credits nor local tests prove provider
minimum throughput, reserved TPS, stream pacing or preemption.

## Failure and retry matrix

| Event | Required outcome | Regression evidence |
| --- | --- | --- |
| Same key after dispatch, including a different client facade | Conflict; no second provider call | `TestManagedQuotaRetriesAndFallbackKeepSeparateCharges` |
| Same key with changed payload/configuration | Conflict; no replacement attempt | Retry/fallback test above; `TestManagedProtocolHeadersArePinnedToAttempt`; `TestQuotaHostIdempotencyBindsPayloadAndSeparatesTransportCorrelation` |
| Fresh billable retry or fallback | New attempt and charge; retain the same subject-scoped logical operation ID when supplied | `TestManagedQuotaRetriesAndFallbackKeepSeparateCharges` |
| Fallback after an uncertain stream | Settle only fallback's trusted usage; retain original exposure/permits and causal linkage | `TestManagedQuotaUnknownStreamAndFallbackRetainSeparateExposure` |
| Membership, permission, configuration or bound expires/changes before dispatch | No provider I/O; abort only definitely undispatched admission | `TestQuotaHostDirectoryMembershipRechecksBeforeDispatchAndSeparatesActor`; `TestQuotaHostModelRefreshesConfigurationAndAccessBeforeDispatch`; `TestManagedToolRevokedDispatchGrantReleasesOnlyUndispatchedHold`; `TestQuotaHostReprovesTokenExposureBeforeDispatch` |
| Missing authoritative human/agent membership | Reject before reservation and credentials; no inferred empty membership | `TestManagedQuotaMissingAuthoritativeMembershipCannotReachEitherPlane`; `TestQuotaHostRejectsUnprovenIdentityAndActorBeforeReservation` |
| Independent delegated actor consumes a model then invokes a tool | Preserve human/actor membership separately; enforce the same actor cap on both planes | `TestManagedQuotaIndependentActorCapSpansModelAndTool` |
| Model or tool tries the other plane/replica after exhausting shared capacity | Same budget/rate/concurrency denial; no second provider call | `TestManagedQuotaSharedModelToolLimitsAcrossReplicas` (both plane orders for all three controls) |
| Cumulative usage decreases inside withheld terminal frames | Reject contradictory tail; no terminal settlement/delivery; retain unknown exposure | `TestManagedQuotaSSETailCannotReplaceHigherCumulativeUsage`; `TestControlledNativeCumulativeTailCannotRegress` |
| Repeated equal cumulative final counters | Preserve native bytes; settle once, without summing repetitions | `TestManagedQuotaSSERepeatedCumulativeFinalDoesNotDoubleCharge` |
| Truncated stream or cancellation after dispatch | Keep unknown exposure and permits; no manufactured terminal usage | `TestQuotaHostModelIncompleteSSEAndCancellationRetainExposure`; `TestManagedToolIncompleteExecutionRetainsSharedExposure`; `TestManagedTokenProfilesSettleCacheWritesExactly` (interrupted Anthropic SSE, ConverseStream) |
| Cache writes (Anthropic per-TTL/automatic, OpenAI 30m/12h, Bedrock Converse/ConverseStream/InvokeModel) | Settle each disjoint meter and money exactly; replay is a conflict and charges nothing more; positive writes without a meter fail closed | `TestManagedTokenProfilesSettleCacheWritesExactly`; `TestCacheWriteBindingsMustMapOneCompleteOption`; `TestStrictProfilesAcceptCacheControlsOnlyWhenWritesAreMetered` |
| Vertex OpenAI-compatible chat, Gemini JSON-array streams, Developer API generation/Interactions, embeddings, declared compatible servers | Settle exactly where the native totals prove composition; otherwise fail closed | `TestManagedGoogleCompositionProfilesSettle`; `TestManagedDeveloperAPIAndEmbeddingProfilesSettle`; `TestCompatibleServerTokenProfileRequiresDeclaredSemantics` |
| Provider rejects the request before processing (429/529/4xx per provider) | Native status, body and `Retry-After` relayed; one request settled, zero tokens, holds and permit released; 5xx stays unknown | `TestStrictProfileProviderRejectionReleasesHold` |
| Token-metered WebSocket sessions | Each response/window settled before its terminal event is relayed; denial is a native error and the session stays open; disconnect retains the open hold; Live renewal denial closes with goAway | `TestManagedRealtimeAdmitsEachResponseAndRefusesNatively`; `TestManagedRealtimeDisconnectRetainsResponseHold`; `TestManagedLiveAllowanceRenewsSettlesAndClosesOnDenial`; `TestTokenSessionRefusesUnadmittedResponses`; harness token-session replay |
| Native tool business error / valid finite SSE | Consume one call; preserve result/error/framing after durable acknowledgement | `TestManagedToolNativeFinalityIsAcknowledgedBeforeDelivery` |
| Async submission succeeds with `done:false` | Count submission only and pin native resource; do not invent job usage | `TestManagedQuotaAsyncSubmissionCountsExchangeWithoutInventingJobUsage` |
| Terminal commit/acknowledgement lost; worker restarts | Recover original evidence/prices; never redispatch or double charge | `TestTerminalEvidenceCommitAmbiguityAndCancelledClient`; `TestTerminalSettlementAfterWorkerAckFailureIsNotChargedTwice`; `TestQuotaEmulatorReplicasOutboxRestartAndOriginalPrices` |
| Capacity evidence expires after queue admission | Block dispatch; configured weights do not establish provider throughput | `TestQuotaScheduledHostCapacityExpiryIsRecheckedBeforeDispatch`; `TestQuotaSchedulingManagedNativeToolSettlesAndBlocksSecondProviderDispatch` |

Host execution tests are in
[quota_execution_contract_test.go](../internal/api/quota_execution_contract_test.go),
[quota_tool_contract_test.go](../internal/api/quota_tool_contract_test.go),
[model host tests](../internal/api/quota_host_model_test.go) and
[provider host tests](../internal/api/quota_host_provider_test.go).
Provider transport regressions are in
[cumulative_stream_test.go](../pkg/modelapi/cumulative_stream_test.go).
Scheduler integration is in
[quota_scheduler_test.go](../cmd/aiplex-api/quota_scheduler_test.go).
Accounting recovery remains covered by the existing quota core tests.

These tests cover request-bound identity, actual dispatch and bypass exclusion,
trusted terminal usage and recovery, shared model and tool controls, and admitted
capacity composition (see [requirements and limits](quotaplex-prerequisites.md)).
Deployment-wide routing, native async and Live billing, and verified provider
throughput are not covered.

## Test evidence

The Go tests run with the race detector in CI. Emulator-only tests skip unless
the Firestore emulator is configured.

For an SSE final tail whose cumulative usage counters are replaced by lower or
contradictory values, the attempt is left `unknown`, the tail is withheld and
every budget hold is retained. Equal repeated final counters complete
byte-for-byte and charge once.

Reproduce the complete Go validation from the repository root:

```sh
go test -race ./... -count=1 -json
go vet ./...
go build -buildvcs=false -trimpath -o /tmp/aiplex-quota-api ./cmd/aiplex-api
go build -buildvcs=false -trimpath -o /tmp/aiplex-quota-cli ./cmd/aiplex-cli
```

Focused execution evidence:

```sh
go test -race ./pkg/execution ./pkg/modelapi ./pkg/toolexec ./internal/api \
  ./cmd/aiplex-api -count=1
```

The cross-plane regression uses the reference transactional store and distinct
host/engine instances; it is not a production multi-replica load test. The
ordinary Go suite skips emulator-only tests when `FIRESTORE_EMULATOR_HOST` is
unset. The existing dedicated emulator CI lane and
[production qualification guide](production-qualification.md) describe that
separate qualification. No live provider invoice, cloud deployment, production
enforcement or provider capacity reservation is produced by these tests.

### Media, unit and batch operations

| Path or operation | Enforced dimensions in its supported profile | Native semantics and remaining boundary |
| --- | --- | --- |
| OpenAI image generation/edit, audio transcription/translation (JSON, SSE, multipart) | `media-units`: requests plus generated images (bound = request `n`), input-audio milliseconds and tokens (bound = trusted authority); settled from the terminal response; or request-count | Multipart spooled to disk and relayed byte-for-byte; terminal SSE frames held until acknowledged; a response missing a bound unit fails closed (unknown). Speech is request-count only (no usage documented). See [unit profiles](model-metering-profiles.md#unit-and-media-profiles). |
| OpenAI Live voice sessions (WebSocket) | Request-count only, as other sessions | Voice seconds observed as `voice_session_milliseconds` (ledger), final only with `session.closed`; no seconds ceiling is claimed. |
| Provider batches (OpenAI Batch + Files, Anthropic Message Batches, Gemini Developer API batch) | `batch-usage`: admitted once at create with a trusted bound; settled exactly once from complete per-item results fetched through the gateway; or request-count per exchange | HTTP completion of create does not settle the job: the attempt stays unknown (`batch_results_pending`) with its exposure until results settle it; partial, interrupted, cross-check-mismatched or repeated fetches settle nothing more. Vertex batch output (Cloud Storage/BigQuery) is not fetched and cannot settle. See [provider batches](model-metering-profiles.md#provider-batches). |
| Gemini Developer API predictLongRunning (video) | Request-count (asynchronous) | Neither usage nor duration is reported; video seconds stay unknown. |

| Event | Required outcome | Regression evidence |
| --- | --- | --- |
| Batch results fetched twice, concurrently, or after a partial/interrupted fetch | One settlement and one ledger usage record; partial or cut bodies settle nothing | `TestAnthropicMessageBatchSettlesOnceFromCompleteResults`; `TestManagedMessageBatchSettlesOnceFromResults` |
| Batch settlement cannot be acknowledged | Final results line withheld; record not settled | `TestBatchResultsWithholdFinalLineUntilAcknowledged` |
| OpenAI batch object usage disagrees with output lines | Not settled; record flagged | `TestOpenAIBatchSettlesFromOutputAndErrorFiles` |
| Image or duration response settles its unit meter | Exact units and money; ledger not partial | `TestManagedImageGenerationSettlesGeneratedImages`; `TestManagedTranscriptionSettlesAudioDuration` |

# QuotaPlex execution contracts and evidence

The managed integration is already part of merged PR #9. This follow-up qualifies
the existing integration, fixes cumulative usage replacement inside an SSE final
tail, and adds model/tool tests over separate host/engine instances sharing one
transactional store. It does not replace the accounting core, change defaults,
enable a production policy or deploy resources.

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
| Managed OpenAI Chat/Responses, Anthropic Messages/Vertex and model-backed Interactions | Requests; supported strict text profiles and exact configured money; admitted-demand rates; concurrency | Provider-specific cache/reasoning composition remains native. Unsupported background/media/server-tool/cache-write billing has no token guarantee. See [profile matrix](model-metering-profiles.md). |
| MCP stateless JSON and bounded finite SSE `tools/call` | Requests/calls, configured fixed call tariff, admitted rates and concurrency | Exact JSON-RPC ID/body, native result/error and supported notifications retained. Terminal RPC is acknowledged before delivery. Rows/bytes/duration and opaque tool internals remain unmetered. |
| Async job submission/read/cancellation under request-count | Managed HTTP exchange count, explicit exchange tariff/rate and exchange concurrency | Job ID/ownership retained. `done:false` is preserved; HTTP completion does not settle job tokens, lifetime, storage or internal work. Those dimensions remain advisory/unsupported. |
| Live WebSocket | Existing authorized native transport when quota is disabled | A protected Live invocation is refused before upstream credentials/upgrade until a renewal/usage adapter exists. No token or lifetime ceiling is claimed. |
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
| Truncated stream or cancellation after dispatch | Keep unknown exposure and permits; no manufactured terminal usage | `TestQuotaHostModelIncompleteSSEAndCancellationRetainExposure`; `TestManagedToolIncompleteExecutionRetainsSharedExposure` |
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

This maps Q03 to request-bound identity, Q06/Q15 to actual dispatch and bypass
exclusion, Q11 to trusted terminal usage/recovery, Q19/Q22 to shared model/tool
controls, and Q21 to admitted capacity composition. Deployment-wide routing,
native async/Live billing and verified provider throughput remain separate gates.

## Test evidence

Validated on November 9, 2026: **1,219 Go tests/subtests pass with the race
detector**, including **32 new tests/subtests in 15 new top-level tests**. All
37 tested packages pass; nine emulator-only tests skip in this local run.
`go vet ./...` and both API/CLI builds pass. Local builds use `-buildvcs=false`
to omit VCS stamping in the linked workspace checkout.

The initial new SSE-tail regression failed in both variants against the merged
implementation: the attempt was `settled` using lower replacement counters and
the contradictory final tail was delivered. With the fix, both variants leave
the attempt `unknown`, withhold the tail and retain every budget hold. Equal
repeated final counters still complete byte-for-byte and charge once.

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

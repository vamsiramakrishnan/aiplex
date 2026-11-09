# Production qualification

The implementation has repeatable mechanism tests, a real-emulator CI lane and a
local billing-evidence comparator. None of these supplies provider credentials,
provisions a cloud project, certifies native billing equivalence or grants a
provider throughput reservation. Qualify the exact deployment/profile combination
before enabling hard limits.

## Automated evidence

| Check | What runs | Boundary |
| --- | --- | --- |
| Repository tests | Go race tests, SDK/CLI checks, Console tests/build, schemas, Rust authorization, Helm | Hermetic contracts and actual local HTTP/SSE/SDK fixtures |
| Firestore emulator CI | Seven existing registry CRUD cases plus quota/configuration qualification tests | Native Firestore SDK transactions against the official emulator, without cloud credentials |
| Concurrent quota replicas | Two clients/engines compete for one budget, monetary cap, rate bucket and concurrency limit | Exactly six admissions at the configured burst; claim race has one winner |
| Restart and outbox | Persist unknown attempt + native terminal evidence, replace clients/engine, upgrade prices, run recovery | Original prices and outstanding holds survive; terminal replay does not charge twice |
| Configuration CAS | Concurrent legacy configuration writes, tombstone recreation, dependency generation check | One CAS winner; stale writers cannot overwrite a new incarnation |
| Transaction rollback | Stage a quota record, return an error, read through another client | Failed callback leaves no committed record |
| Billing comparison | Strict local JSONL validation and exact per-attempt comparison | Compares supplied evidence only; source normalization/provenance remains explicit |

`FIRESTORE_EMULATOR_HOST` is absent in the ordinary Go job, so emulator-only tests
skip there. The dedicated **Firestore emulator** job sets it and runs those tests
against a bounded-lifetime local service. CI saves its test JSONL and emulator log
for seven days. The SDK is pinned to `592.0.0`; that release requires Java 25 for
the emulator, so the lane installs Temurin 25. No Google authentication action or
cloud write is needed.

To repeat locally with a compatible Java installation and the pinned Google Cloud
CLI, start the service in one terminal:

```sh
gcloud components install cloud-firestore-emulator
gcloud emulators firestore start --host-port=127.0.0.1:8787 --project=aiplex-emulator
```

Then run the same bounded test selection in another:

```sh
FIRESTORE_EMULATOR_HOST=127.0.0.1:8787 go test -race ./internal/registry \
  -count=1 -timeout=4m \
  -run '^(TestFirestore(Instance|Agent|Template|DeployHistory|UserScopes|LLMRouteConfig|RoleBinding)|TestQuotaEmulator.*)$'
```

The new qualification tests use fresh random emulator project IDs and independent
SDK clients. Closing/reopening those clients tests process replacement while the
emulator retains its data. It does **not** test restoration of a managed Firestore
backup, production IAM, composite-index enforcement, regional latency or live
service contention behavior.

## Admission cost

Repeat the reference-backend measurements on a controlled machine:

```sh
go test ./pkg/quotaplex -run '^$' -bench 'BenchmarkQuota' \
  -benchtime=100x -benchmem -count=5
```

The three cases measure compiled policy resolution, admission, and the complete
admit/claim/settle path. They include real accounting allocations/JSON snapshots;
no provider or directory calls occur inside these transactions. The memory
backend's retained history grows during each run. Results are CPU/allocation
baselines, not Firestore latency, production throughput or an admission SLA.
For deployment measurements record p50/p95/p99 admission latency, retries,
transaction aborts and recovery lag separately from provider TTFT and output rate.
Use the intended tenant/rule fanout and replica count; report denied and unknown
attempts rather than excluding them from the sample.

## Compare billing evidence

Normalize independently obtained provider evidence and QuotaPlex settlement into
one JSON object per attempt. Both files use the same documented meter mapping and
currency scale:

```json
{"tenant_id":"example","attempt_id":"attempt-1","model_version":"resolved-model-v1","counters":{"uncached_input_tokens":7,"output_tokens":2},"money":{"USD":{"scale":6,"amount":11}},"evidence_ref":"reviewed-provider-receipt"}
```

Counters are nonnegative int64 quantities. Currency amounts are nonnegative
integers in the declared scale: `11` at scale `6` means USD `0.000011`. A model
version is mandatory. Use a separate record/mapping for credits or other invoice
adjustments; they are not raw inference usage. The tool rejects duplicate fields,
duplicate tenant/attempt identities, floats, scientific notation, booleans used as
quantities, overflows, empty files and oversized input.

```sh
python3 tools/qualification/compare_billing.py \
  --quota quota-settlements.jsonl --provider provider-evidence.jsonl
```

Exit `0` means all records, counters, model versions and currency amounts match;
`1` means a comparison difference; `2` means invalid evidence. The report contains
bounded identifiers/reason codes, not prompts, tokens or raw provider payloads.
`--money-tolerance N` permits only an explicitly declared absolute monetary
difference in each currency's existing scale. It never tolerates missing attempts,
changed versions, raw counter differences, currencies or scales. Default is exact.

A provider export may aggregate calls, apply volume contracts, round at another
boundary, or arrive later than settlement. Establish that mapping and its timing
before using this gate. The comparator does not manufacture per-request native
billing records, inspect prices, verify an `evidence_ref`, or establish source
authenticity. Retain the original export, normalization revision, contract/price
reference, collection interval and file hashes with the comparison report.

## Deployment and recovery drill

1. **Fix the tested generation.** Record application/image revision, active quota
   policy and host manifest digests, membership authority/key version, provider
   bounds/prices, enabled execution profiles and actual route attachments.
2. **Exercise the target identities.** Use real workforce and autonomous/delegated
   agent principals. Test source-qualified overlapping groups, revocation within
   the configured freshness bound, credential renewal and cross-tenant refusal.
   A directory adapter fixture is not a live IdP qualification.
3. **Prove traffic coverage.** Follow an actual Antigravity/Codex/Claude or native
   API call through the intended gateway. Confirm receipt correlation and native
   credential destination. Test that a configured protected route cannot reach an
   unmetered direct backend. Native provider availability, enterprise entitlement
   and licensed gateway sign-in are deployment-specific evidence.
4. **Exercise uncertainty.** Interrupt a response after dispatch, stop the API,
   expire an undispatched admission, rotate a membership/price generation, and
   restart on the same backend. Known terminal evidence must settle once; unknown
   work must retain exposure and must not resubmit a native write.
5. **Drill backup restoration in an isolated target.** Stop new protected
   dispatch, record/fence the active generation, capture the managed database
   backup/export and the independent provider invocation journal, and restore to
   a separate database. Restore all quota kinds together: policies/price books,
   attempts/claims, holds/rates/permits, evidence/work indexes, loans, scheduler
   state, configuration generations and administrative receipts. Never restore
   balances alone or reset scheduler/bucket state to recover capacity.
6. **Reconcile the restoration boundary.** Identify native work that may have
   committed after the backup point. Retain its exposure and use trusted native
   observations or reviewed reconciliation; a restored absence is not proof that
   the provider did no work. Keep dispatch disabled until deduplication and
   accounting checks are complete. Test with a non-production target first.
7. **Shadow, then enforce.** Compare independently collected native evidence with
   the ledger over each supported billing profile. Set explicit bounds for
   admission latency, transaction errors, pending-evidence age and unresolved
   holds. Enable a small covered cohort before increasing traffic.

Application rollback must preserve the durable accounting generation and must
understand its stored schema. A stale binary failing readiness is preferable to
restarting with empty balances or replaying unknown work. Roll forward with a
compatible fix when an older binary cannot safely interpret current state.

## Sources

- [Firestore emulator](https://docs.cloud.google.com/firestore/native/docs/emulator)
- [Cloud CLI 592.0.0 release notes](https://docs.cloud.google.com/sdk/docs/release-notes#59200_2026-11-03)
- [setup-gcloud inputs](https://github.com/google-github-actions/setup-gcloud#inputs)
- [Managed QuotaPlex integration](quotaplex-integration.md)
- [Quota administration and reconciliation](quotaplex-administration.md)

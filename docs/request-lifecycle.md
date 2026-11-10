# Request lifecycle: order of checks

This is the one place that states the order in which AIPlex checks a request.
The guides for [request hooks](request-hooks.md), [policy rules on tool
calls](tool-policy-rules.md), [reliability and routing](reliability-and-routing.md)
and [budgets](budgets.md) link here instead of restating it. The order was read
from the code paths named under each list. Where a guide and this page differ,
the code wins and the guide should be fixed.

A check that refuses stops the request. Every refusal before the quota lease
reserves nothing and calls no provider. Every response that reaches the caller
passes the response hooks first, and every dispatched attempt is settled.

## Models

Code: `pkg/modelapi` `Gateway.ServeHTTP` and `prepareDispatch`
(`pkg/modelapi/gateway.go`, `pkg/modelapi/hooks.go`, `pkg/modelapi/execution.go`).

1. **Identity and access point.** The caller is verified; the tenant comes from
   that identity. Access-point admission (subjects, the access point's model
   list) applies before any model work.
2. **Model resolution.** The client-facing model name is resolved to a surface
   and native model; resource references in the body are checked for ownership.
3. **Grants.** The operation grant and the model grant (`llm:model:{model}`,
   `llm:surface:{surface}:{operation}`) are checked.
4. **Capacity admission** (`Admit`). Capacity gates refuse before any hook runs.
5. **Request hooks.** Redaction, size limits and sidecar hooks may transform the
   body or refuse it.
6. **Quota and budget lease** (QuotaPlex). Admission reserves the request's
   exposure against quota rules and against every budget it draws from. The lease
   is taken on the body that will be sent, after the transform in step 5.
7. **Cache lookup** (only when the model sets `cache`). A hit serves the stored
   answer and skips steps 8 and 9; it still settles the lease, with zero money.
8. **Per-backend attempt.** The credential is obtained and the request is
   dispatched. With routing, steps 3, 4 (capacity), 6 and 8 run again for each
   attempt on its backend; step 5 runs once, on the first attempt, and later
   attempts reuse the hooked body with only the model name rewritten.
9. **Response hooks**, on the response that is delivered.
10. **Settlement and ledger.** The lease settles with the observed usage (or as
    uncertain); the usage record is written.

Which wire formats response hooks inspect, and how streams are buffered, is
described in [request hooks](request-hooks.md#what-is-inspected-per-wire-format).
A WebSocket session checks its operation and runs request hooks once at setup
(`sessionHooks`); each exchange then takes its own lease.

## Tools

Code: `pkg/toolexec` `Gateway.ServeHTTP` (`pkg/toolexec/gateway.go`) and the
handler wiring in `internal/api/tool_connection_auth.go`,
`internal/api/tool_policy_rules.go` and `internal/api/quota_tool.go`.

1. **Transport and identity.** POST, JSON, the verified request context, and the
   connection named in the context. `tools/list` takes a separate path that
   filters the listing by the same allow, deny, scope and rule checks (see
   [policy rules](tool-policy-rules.md#toolslist)).
2. **Protocol.** The JSON-RPC `tools/call` is parsed; the tool must be in
   `execution.allowed_tools`.
3. **Connection lists.** `tools.allow` and `tools.deny`.
4. **Scope.** The caller's `mcp:tools:{name}` grant.
5. **`read_only`**, when set: the tool must be annotated `readOnlyHint: true`
   (an unknown annotation refuses).
6. **Policy rules.** The connection's rules, then the access point's rules, for
   calls through an access point. Every rule runs; a rule that cannot be
   evaluated refuses.
7. **Request hooks** (`InspectRequest`). A transformed body is parsed again so
   the lease and the dispatch describe the bytes sent.
8. **Quota lease** (`Prepare`), with the `Idempotency-Key` and funding headers.
9. **Credential.** The upstream credential is checked; delegated OAuth grants are
   rechecked before dispatch.
10. **Upstream dispatch.** One attempt. A transport retry is made only when the
    tool is read-only; the retry re-sends the same patched body and does not run
    rules or hooks again.
11. **Response hooks**, then the terminal response is delivered.
12. **Ledger.** The lease is finished with the outcome; a tool call that does not
    establish terminal usage is recorded as uncertain.

## Where the code and the guides were compared

- Request hooks, reliability and tool rules agree with the order above.
- Budget admission is part of the same quota lease (step 6 for models); it is not
  a separate step. A cache hit draws request-count budgets once and money budgets
  not at all (see [budgets](budgets.md#reservation-settlement-and-the-overshoot-bound)).
- The tool path has no capacity admission step; capacity gates apply to models
  only.

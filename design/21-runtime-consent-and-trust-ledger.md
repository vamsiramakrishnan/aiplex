# 21 — Runtime Consent & Verifiable Trust Ledger

> **Status:** Proposed. Two runtime features that the [Capability Mesh](18-capability-mesh.md) makes cheap to ship: just-in-time consent (the Auth0 / WorkOS pattern) and a signed-receipt audit chain (the sigstore / Rekor pattern).
> **Replaces:** the static-only consent model in [doc 02](02-auth-keycloak.md), and the "trust the logs" audit model in [doc 10](10-observability.md).

---

## Part 1 — Just-in-Time Consent

### Problem

Today, consent is static and ahead-of-time. The user logs in, sees a checklist of scopes the agent wants, approves, and gets a token. After that, the agent is silent — until it needs a scope it doesn't have, at which point the request 403s and the user has to start a fresh login flow with a different scope set.

This breaks down in real agent flows:

- Agent works for an hour and discovers it needs one new tool — full re-consent.
- Agent encounters a `regulated` data class that requires step-up — no path to do it inline.
- Agent needs temporary elevation for a specific call (e.g., delete vs. read) — can't grant without re-issuing the whole token.

The bar is **Auth0 GenAI / WorkOS AuthKit for Agents**: inline elevation, async approval, scoped to a single capability or call.

### Design

```
agent calls cap://tool/grade@v1
   │
   ▼
gateway → ext_authz (OPA) → MISS (no matching cap claim)
   │
   ▼
gateway returns 401 with a structured challenge:

   HTTP/1.1 401 Unauthorized
   WWW-Authenticate: AIPlex realm="aiplex",
                     capability="cap://tool/grade@v1",
                     action="call",
                     elevation_url="https://aiplex.example.com/auth/elevate",
                     elevation_id="el_01HX9P2Y..."

   Content-Type: application/json
   {
     "error": "capability_required",
     "capability": "cap://tool/grade@v1",
     "action": "call",
     "elevation_id": "el_01HX9P2Y...",
     "expires_at": "2026-05-09T12:05:00Z"
   }
   │
   ▼
agent (or SDK) initiates step-up
   │
   ▼
POST /auth/elevate { elevation_id, justification?, target_principal? }
   │
   ▼
AIPlex API
   ├── Decides delivery channel:
   │     - User present in Console: push notification + modal
   │     - User offline: WebPush + email + Slack/Teams (configurable)
   │     - Operator-only: PagerDuty / approval queue
   ├── Records pending elevation in Firestore
   └── Returns 202 with poll_url and ws_url
   │
   ▼
user approves (or denies) in Console
   │
   ▼
AIPlex API → Hydra: issue refreshed token with added cap claim
   │
   ▼
agent retries original request with new token → 200
```

### The challenge format (RFC 9470 + extensions)

The `WWW-Authenticate: AIPlex …` header follows the OAuth Step Up Authentication Challenge Protocol (RFC 9470) with AIPlex-specific parameters:

| Parameter        | Required | Meaning                                                            |
|------------------|----------|--------------------------------------------------------------------|
| `realm`          | yes      | `"aiplex"`                                                         |
| `capability`     | yes      | The cap URI being requested                                        |
| `action`         | yes      | The action on the capability                                       |
| `acr_values`     | no       | Required ACR (e.g. `urn:aiplex:acr:human-present`)                 |
| `max_age`        | no       | Max acceptable age of the user's auth, in seconds                  |
| `elevation_url`  | yes      | Where to start step-up                                             |
| `elevation_id`   | yes      | Idempotency token; same id == same pending request                 |

### Elevation API

```
POST /auth/elevate
{
  "elevation_id": "el_01HX9P2Y...",
  "justification": "Grading a quiz Alice submitted at 11:42",
  "target_principal": "vamsi@example.com",     // optional, for delegated requests
  "duration_seconds": 600                      // optional, capped server-side
}

→ 202 Accepted
{
  "status": "pending",
  "poll_url": "/auth/elevate/el_01HX9P2Y.../status",
  "ws_url":   "wss://aiplex.example.com/auth/elevate/el_01HX9P2Y.../stream",
  "delivery": ["console", "webpush", "email"],
  "expires_at": "2026-05-09T12:05:00Z"
}

→ 200 OK   (when approved)
{
  "status": "approved",
  "token": "<new JWT with added cap>",
  "added_caps": [{"uri":"cap://tool/grade@v1","actions":["call"], "exp": 1714902000}]
}

→ 200 OK   (when denied)
{
  "status": "denied",
  "reason": "user_declined",
  "denied_at": "2026-05-09T12:03:18Z"
}
```

### Console approval UI

When the user is online, the Console subscribes to elevation events via SSE/WebSocket. An incoming elevation pops a modal:

```
┌────────────────────────────────────────────────────┐
│  tutor-agent is asking for                          │
│                                                     │
│   ▸ cap://tool/grade@v1   action: call              │
│                                                     │
│  Reason given:                                      │
│    "Grading a quiz Alice submitted at 11:42"        │
│                                                     │
│  This grant will:                                   │
│   • Last 10 minutes                                 │
│   • Apply to this call only? [✓]                   │
│   • Be recorded in your audit log                   │
│                                                     │
│  [Approve]  [Deny]  [Approve & remember 24h]        │
└────────────────────────────────────────────────────┘
```

### Async / offline approval

If the user is offline at request time:

1. `POST /auth/elevate` accepts the request and returns 202 with a long-poll URL.
2. AIPlex sends a WebPush/email/Slack/Teams notification with a deep link.
3. The agent SDK long-polls or backs off; tool-call execution is suspended.
4. User approves on phone → token issued → SDK retries.

For headless / cron-driven flows, an organization can pre-configure a **delegate approver** (e.g., the user's manager, or a security operator queue). The capability advertises its delivery preferences in `attrs.consent_delivery`.

### Constraint-only step-up

An elevation can be **for an existing capability with stricter constraints**, not just a new capability:

```json
{
  "capability": "cap://memory/students/alice/profile@v1",
  "action": "delete",                    // already in claim.actions, but…
  "constraints": {
    "key_pattern": "lesson-*",            // …user must approve broader pattern
    "duration_seconds": 60
  }
}
```

This is the "you can read; let me delete this one specific thing" pattern. Without it, agents either get over-permission ("you can delete anything ever") or under-permission ("hard-stop"). With it, every consequential action has a tight, auditable consent trail.

### Pre-authorized auto-approvals

Users can register policies in the Console:

> *"Auto-approve elevations for `cap://memory/students/{my_user}/scratch@v1` with action `read|write` for 1 hour, max 10 times per day, only from `tutor-agent`."*

Stored as a policy in `internal/auth/elevation_rules.go`. Server-evaluated. Logged separately as **policy-approved** in the receipt (so audits can distinguish human-approved from policy-approved).

### Fallbacks & failure modes

| Condition                          | Behavior                                                |
|------------------------------------|---------------------------------------------------------|
| User unreachable for 5 min         | 401 with `error: elevation_timeout`; agent backs off    |
| User denies                        | 403 with `error: elevation_denied`; agent halts the task |
| Token issuance fails (Hydra down)  | 503; SDK retries with backoff                           |
| Elevation request rate > limit     | 429; prevents notification flooding                     |
| Multiple agents elevate same cap   | Single notification; first approval applies to all      |

---

## Part 2 — Verifiable Trust Ledger

### Problem

Today, audit is "trust the logs." Cloud Logging captures every request, but:

- An admin with log-write permission can rewrite history.
- There's no cryptographic chain.
- No third party can verify "this agent did this thing at this time" without trusting the operator.
- Cross-system audits (gateway log + Hydra log + provider log) require manual correlation.

The bar is **sigstore / Rekor**: signed, append-only, externally verifiable. The Capability Mesh makes this cheap because every action is one shape — a capability invocation.

### Design

Every capability invocation produces a **Receipt** — a structured, signed record of the call.

```json
{
  "id": "rcpt_01HX9P5Z3R...",
  "ts": "2026-05-09T12:05:01.234Z",
  "prev_hash": "sha256:...",                    // chain link
  "principals": {
    "user":     "vamsi@example.com",
    "agent":    "spiffe://aiplex-prod/.../sa/tutor-agent",
    "provider": "spiffe://aiplex-prod/.../sa/knowledge-base-xyz"
  },
  "capability": {
    "uri":    "cap://tool/search_curriculum@v1",
    "action": "call",
    "schema_hash": "sha256:..."
  },
  "request": {
    "hash":   "sha256:...",                     // SHA-256 of the canonicalised request body
    "size":   1234,
    "trace_id": "0af7651916cd43dd8448eb211c80319c"
  },
  "response": {
    "hash":   "sha256:...",
    "size":   8721,
    "status": 200,
    "latency_ms": 142
  },
  "policy": {
    "decision": "allow",
    "matched_cap": { "uri": "cap://tool/search_curriculum@v1", "actions": ["call"] },
    "constraints_evaluated": [
      { "key": "rate_per_min", "limit": 30, "remaining": 17 }
    ],
    "elevation_id": null
  },
  "signatures": {
    "gateway":  { "alg": "ES256", "kid": "gw-2026-q2", "sig": "..." },
    "provider": { "alg": "ES256", "kid": "kb-xyz",     "sig": "..." }
  },
  "ledger": {
    "log_id":       "aiplex-prod-receipts",
    "log_index":    9384721,
    "inclusion_proof": "...",                   // optional, when sigstore-anchored
    "anchor": {
      "kind": "rekor",
      "url":  "https://rekor.aiplex.dev",
      "tree_id": 1
    }
  }
}
```

### Three signatures, three identities

| Signer    | Identity              | What it attests                                                  |
|-----------|-----------------------|------------------------------------------------------------------|
| Gateway   | Envoy SPIFFE ID       | "I observed this request, ran ext_authz, decision was X"         |
| Provider  | Backend SPIFFE ID     | "I executed this and produced this response"                     |
| Agent     | Agent SPIFFE ID (opt) | "I requested this call with this body"                           |

Multi-party signatures mean **no single component can fabricate a receipt**. Tampering at the gateway is detected by mismatched provider sig, and vice versa.

### The chain

Receipts form a hash chain per stream (`projects/.../logs/aiplex.cap.<kind>.<name>.<ver>`):

```
rcpt_N.prev_hash = SHA256(canonicalize(rcpt_N-1))
```

A reader given any receipt can walk backwards to verify continuity. Drops or rewrites break the chain.

### Storage tiers

| Tier              | Purpose                           | Cost  | Retention      |
|-------------------|-----------------------------------|-------|----------------|
| **BigQuery**      | Queryable history, dashboards     | $     | 90 days hot    |
| **GCS Coldline**  | Long-term retention, cheap reads  | $     | 7 years        |
| **Sigstore Rekor**| External transparency log         | free  | indefinite     |

Tier 1 is required (it's where the Console reads from). Tier 2 is automatic. Tier 3 is opt-in for organizations that need third-party verifiability (regulated industries, multi-org collaborations).

### Verification

```bash
# Verify a single receipt
aiplex audit verify rcpt_01HX9P5Z3R...
# ✓ Gateway signature valid (kid=gw-2026-q2)
# ✓ Provider signature valid (kid=kb-xyz)
# ✓ Schema hash matches registered schema for cap://tool/search_curriculum@v1
# ✓ Chain link to rcpt_01HX9P5Y9Q... verified
# ✓ Rekor inclusion proof verified (log_index=9384721)

# Verify a range
aiplex audit verify --capability cap://memory/students/alice/profile@v1 --since 24h
# Walked 142 receipts. All chain links valid. No tampering detected.

# Verify continuously (monitoring)
aiplex audit watch --capability cap://* --alert-on-break webhook=...
```

The verifier is a separate Go binary so it can run **off the AIPlex cluster** — auditing the auditor.

### Cost model

Receipt generation: ~5KB per receipt, two ECDSA signatures.

- 1M tool calls/day → 5GB/day of receipts → ~150GB/month BigQuery → trivial.
- ECDSA sign at gateway: ~50µs CPU per receipt — negligible vs. tool-call latency.
- Rekor anchoring is batched (1 anchor per minute) — amortized cost is near-zero.

### Integration with elevations

Every elevation produces a receipt of its own (`cap://meta/elevation@v1`):

```json
{
  "capability": { "uri": "cap://meta/elevation@v1", "action": "approve" },
  "principals": {
    "user":     "vamsi@example.com",
    "agent":    "spiffe://.../sa/tutor-agent",
    "approver": "vamsi@example.com",          // human in the loop
    "delivery": "console"
  },
  "elevation": {
    "id":              "el_01HX9P2Y...",
    "target_capability": "cap://tool/grade@v1",
    "added_actions":  ["call"],
    "duration":       600,
    "policy_approved": false
  }
}
```

So the audit trail captures **both** "the agent did X" and "the user approved X for the agent at Y." Auditors can reconstruct a full session, including all human decisions.

---

## What This Buys

| Concern                                            | Before                           | After                                         |
|----------------------------------------------------|----------------------------------|-----------------------------------------------|
| New scope mid-session                              | Full re-login                    | Inline 401 + 5s elevation                     |
| Step-up for sensitive data                         | Not possible                     | Constraint-driven step-up                     |
| Audit "did the user actually approve X?"           | Best effort, plain text logs     | Cryptographic receipt chain                   |
| Cross-org verifiability                            | "Trust our logs"                 | Rekor inclusion proof                         |
| Detect log tampering                               | After-the-fact, often never      | Next receipt's chain verification fails       |
| Compliance evidence (SOC2, HIPAA)                  | Manual log export                | `aiplex audit export --since 90d`             |

---

## Open Questions

> **Open:** Default delivery channel for elevations when user is offline. Decision: WebPush primary, email fallback. Slack/Teams opt-in via integration.

> **Open:** Rekor: self-host vs. sigstore.dev public log. Decision: ship a self-hosted Rekor instance in `deploy/helm/aiplex` for organizations that want their own; offer sigstore.dev anchoring as a managed-service add-on.

> **Open:** Should agents sign their requests too (third signature)? Decision: yes for SPIFFE-identified agents (free), opt-in for IDE/CLI agents (requires SDK integration).

> **Open:** Receipt for failed (denied) calls. Decision: yes — denied calls are the most interesting for audit. Receipt records `policy.decision: "deny"` with reasons.

---

## See Also

- [18 — Capability Mesh](18-capability-mesh.md)
- [02 — Auth: Hydra + Kratos](02-auth-keycloak.md) — what static consent looks like today
- [10 — Observability](10-observability.md) — what plain-log audit looks like today
- [22 — Roadmap](22-roadmap-100x.md) — when this lands

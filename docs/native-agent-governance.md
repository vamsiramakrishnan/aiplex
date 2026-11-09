# Native agent governance in Aiplex

Native identity and registry control planes belong to Identity, alongside existing
WIF resolution. A2APlex owns agent invocation and can link a remote runtime to an
Identity-owned `agent-binding`. QuotaPlex can later use the same verified caller
and membership snapshots without reimplementing cloud identities.

| Provider adapter | Native behavior | Support boundary |
| --- | --- | --- |
| `google-agent-registry` | Service/Binding lifecycle, agent/MCP/endpoint/skill discovery, application IAM | Registry metadata and access policy remain separate |
| `google-iap` | Registered-destination egress IAM get/set, real policy ETags | Gateway routing/attachment must exist separately |
| `google-auth-manager` | Auth-provider lifecycle and policy library; typed delegated credential retrieve/finalize | Credential-sensitive library only; public governance routes reject it |
| `aws-agentcore-identity` | Workload identities, provider metadata/lifecycle, vault configuration; trusted workload/user credential broker library | Signed STS verifies account before any native RPC; secret-bearing operations are library-only |
| `entra-agent-id` | Blueprint/child identity lifecycle, sponsors, enable/disable, assigned access inspection | Explicit Microsoft Graph beta contract; approved SDK-sidecar broker handles autonomous/OBO flows |

Bindings keep the agent, delegated user, sponsor, registry native resource name,
discovery URN and runtime reference separate. These links are **operator-declared**;
recording them does not prove provider-attested parentage or active network
enforcement. Each provider independently validates its authoritative namespace,
and Entra checks actual blueprint/child relationships before operations. Sponsor
groups grant no implicit agent permissions. Existing trusted role bindings and
membership snapshots remain the source of Aiplex group authorization.

## Configure providers

Extend the operator-owned `AIPLEX_CONNECTIONS_FILE`. Each provider references an
existing tenant-scoped credential profile and an independently allowed endpoint:

```json
{
  "network": {
    "allowed_origins": ["https://agentregistry.googleapis.com"]
  },
  "identities": [{
    "id": "governance-adc",
    "tenant_id": "example",
    "type": "gcp-adc",
    "allowed_origins": ["https://agentregistry.googleapis.com"],
    "scopes": ["https://www.googleapis.com/auth/cloud-platform"]
  }],
  "governance_providers": [{
    "id": "gcp-registry",
    "tenant_id": "example",
    "type": "google-agent-registry",
    "endpoint": "https://agentregistry.googleapis.com",
    "identity_ref": "governance-adc",
    "allow_apply": false,
    "options": {"project": "example-governance", "location": "global"}
  }]
}
```

`tenant_id` at the provider level is Aiplex's trusted storage namespace. Entra's
`options.tenant_id`, AWS's `options.account_id`, and Google's `options.project`
identify native authorities. These identifiers are not interchangeable.

For IAP, select `google-iap`, `https://iap.googleapis.com`, and a numeric project
number. Configure exact `allowed_agent_principals` and `allowed_roles` for changed
grants. Existing unmanaged policy entries must remain unchanged.

For AWS, select `aws-agentcore-identity` with the regional
`https://bedrock-agentcore-control.REGION.amazonaws.com` origin, `region`, an
`aws-default` identity, and native `account_id`, `region`, and exact resource-name
allowlists in `options`. Both the global network policy and credential profile
must allow `https://sts.REGION.amazonaws.com` for account attestation (China uses
the corresponding `.com.cn` suffix). Native signing and STS use the same cached
credential provider. The adapter does not invent an identity-directory create or
disable API. See [AWS contracts](aws-agent-governance.md).

For Entra, select `entra-agent-id` and `https://graph.microsoft.com`. Configure an
`azure-default` identity with explicit `https://graph.microsoft.com/.default`
scope, or an approved bearer credential reference. Native options pin the Entra
tenant, explicit `api_version: beta`, and blueprint object/application-ID pairs.
The host treats Graph access tokens as opaque. Before the first operation for a
credential, it calls `GET /v1.0/organization?$select=id` using that same credential
and verifies the native tenant. App-only credentials require
`Organization.Read.All` in addition to the Agent ID operation permissions. A
changed credential repeats this check. See [Entra contracts](entra-agent-governance.md).

Provider configuration fingerprints include endpoint, native options, credential
profile metadata and network policy. Secret bytes are excluded so credentials can
rotate. A profile fingerprint alone does not prove a credential's native principal;
AWS account and Graph organization checks establish their native authority.
Google ADC/WIF IAM grants and runtime credential acquisition remain operator-owned.

## Read, review and apply

The authenticated API is under `/api/v1/iam/native`:

| API | Behavior |
| --- | --- |
| `GET /providers` | Configured operation catalog; does not assert cloud readiness |
| `GET/PUT/DELETE /bindings/{id}` | Revisioned `agent-binding`; PUT uses `If-Match` |
| `POST /bindings/{id}/read` | Explicit native read plus durable observation |
| `POST /bindings/{id}/plan` | Review a desired operation already saved in the binding |
| `GET /plans/{id}` | Public plan metadata and digest, never the stored operation envelope |
| `POST /plans/{id}/apply` | Execute the exact saved plan once with matching revision/digest |
| `GET /receipts/{id}` | Inspect success, failure, pending or unknown outcome |

Grant `aiplex:governance:providers:read` for provider discovery, and
`aiplex:governance:{read,plan,apply}:BINDING_ID` for the corresponding actions.
Listing bindings requires `aiplex:governance:read:bindings`. Binding writes and
plan/apply also retain the host's administrative control-plane role checks.
Native read has its own action permission even though it uses POST.

An `AgentBinding` selects a configured driver, exact allowed operation/resource
pairs and saved desired operations. The provider's pure schema/namespace validator
and secret-field filter run before storage. Credential operations cannot be
granted or planned through this API. Unknown fields, caller credentials, arbitrary
endpoints and apply-time payload substitutions are rejected.

For a native policy update, first read the current policy under the binding. Save
the reviewed desired policy with its exact ETag at a new binding revision; read
authorization and fresh observation proofs remain bound to the same caller,
actor, native identity coordinates and provider configuration. Plan with the
current binding revision and desired operation ID. Apply sends only the returned
plan revision and approval digest, and requires both host `allow_apply` and the
explicit apply grant. Enabling a provider does not automatically execute anything.

The SDK exposes `ListNativeProviders`, `ReadNativeBinding`, `PlanNativeBinding`,
`ApplyNativePlan`, `GetNativePlan`, and `GetNativeReceipt`. CLI equivalents are
under `aiplex native governance`; `aiplex resources` manages `agent-binding` specs.

For A2APlex, optional `governance_binding_ref` links a connection to the declared
binding. Its registry `runtime_ref` must equal the connection's native resource
ID. Missing, cross-tenant or mismatched references prevent invocation. The link
does not replace runtime authentication or imply a gateway is attached.

## Execution and deployment limits

Plans pin binding revision, operation/payload/ETag, provider configuration, caller,
actor and expiry. A durable CAS claim precedes the write. Lost responses, timeouts,
panics or ambiguous server failures remain `unknown`; retrying never blindly
executes the same plan again. Receipts contain hashes/status, not native secrets.
Native async operations require subsequent observation before declaring readiness.

IAM uses real provider ETags. Google Registry and AWS identity APIs lacking atomic
preconditions require explicit `allow_non_atomic_mutations` and fresh resource
fingerprints. That preflight detects drift but does not eliminate external writer
races. It is never described as provider CAS. See [controller semantics](native-governance-controller.md).

Credential brokers are privileged library interfaces, not token-vending HTTP
routes. Entra uses the approved Auth SDK sidecar; Google binds delegated users to
Auth Manager retrieve/finalize; AWS requires trusted workload/user token proofs.
The Google outbound mTLS adapter verifies injected native ID tokens and rotating
certificates. Runtime-native token acquisition must use a documented credential
source; ordinary ADC is not converted into certificate-bound agent credentials.
See [Google contracts](google-agent-governance.md).

Native network attachment, management boundaries, gateway deployment, production
consent callbacks and fleet-wide scheduling remain deployment concerns. Anvil's
pinned provisioning plans can supply missing constructs through the existing
deployment interface. No live cloud apply or enterprise sign-in occurred during
these local protocol tests. QuotaPlex allocation/accounting remains separate.
See [QuotaPlex prerequisites](quotaplex-prerequisites.md) for the accounting,
metering and integration work required before enabling token or monetary limits.

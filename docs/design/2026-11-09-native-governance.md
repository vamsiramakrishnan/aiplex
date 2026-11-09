# Native agent governance

Status: independently reviewed; implementation follows the conditions below.

The connected-module adapters govern invocation. This extension connects the
provider identity and registry control planes without representing metadata as
observed enforcement.

## Ownership

- Identity owns provider-native principal references, credential/delegation
  adapters, and a shared governance controller. A sponsor is accountable for an
  agent; sponsor groups never implicitly grant that agent permissions.
- Registry references retain native resource names, discovery identifiers, and
  runtime references separately. Provider identifiers are never merged by name
  or email. Native authority tenancy differs from an Aiplex tenant namespace.
- Provider drivers own allowlisted native API operations and validate native
  account/project/tenant/blueprint boundaries. Credential operations remain
  library-only and cannot pass through the generic governance HTTP API.
- Networking owns provisioning intent. Anvil remains an optional provisioner;
  provider identity and delegation semantics remain typed Aiplex contracts.

## Review conditions

1. Authorize every read, plan and apply before credentials or native API access.
2. Bind plans to the saved binding revision, exact operation/payload/ETag,
   principal/actor, expiry, and host provider configuration fingerprint including
   origin and credential-profile metadata. No request-body payload override at apply.
3. Persist an execution claim before an external write. Concurrent calls use CAS;
   ambiguous outcomes stay unknown and are never automatically replayed.
4. Require real native preconditions where available. Google registry mutations
   lack atomic provider ETags; explicitly opt in to non-atomic writes with a stale
   observation check and document out-of-band races. IAM uses its real policy ETag.
5. Token exchanges, API keys and auth-provider secrets never enter public
   inventory, plan records, receipts, errors or audit logs.
6. Pin API endpoints and credential destinations through the existing network
   transport. Drivers expose validated native operations, not arbitrary URLs.
7. Apply is disabled unless the host enables it and verifies the exact approval.
   Readiness describes configured adapters and observed results separately.

## Acceptance

Protocol tests use fake native services with actual HTTP and SDK signing. Cover
tenant/blueprint/account boundaries, stale plans, provider-config changes,
concurrent apply, cancellation, ambiguous writes, native ETags, delegation subject
binding, and credential redaction. No live cloud resources are changed by the
implementation tests. QuotaPlex remains separate until these foundations pass.

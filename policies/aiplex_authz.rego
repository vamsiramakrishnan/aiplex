package aiplex.authz

import rego.v1

# Capability Mesh policy — single rule across all kinds.
#
# The capability resolver (Envoy ext_proc filter) inspects the incoming request
# and writes the matched capability URI + action into filter metadata under
# `aiplex.cap`. OPA reads that metadata and verifies the JWT carries a `caps`
# claim entry that grants the action on that URI.
#
# Discovery actions (initialize, *_list, ping, health) do not require a cap.

default allow := false

# ── Token verification ──
token := payload if {
	[valid, _, payload] := io.jwt.decode_verify(
		input.attributes.request.http.headers.authorization,
		{"iss": "https://aiplex.example.com/auth/realms/aiplex"}
	)
	valid
}

# Extract resolver-populated metadata.
requested_uri := input.attributes.metadata_context.filter_metadata["aiplex.cap"].uri
requested_action := input.attributes.metadata_context.filter_metadata["aiplex.cap"].action

# A cap claim from the token grants (uri, action).
cap_grants(c, uri, action) if {
	c.uri == uri
	action in c.actions
}

# Empty actions in the cap claim means "all kind-default actions" — but we
# require the resolver to have populated metadata, so default actions are
# enforced upstream by the resolver, not here.
matching_cap := c if {
	some c in token.caps
	cap_grants(c, requested_uri, requested_action)
}

allow if {
	matching_cap
}

# ── Discovery: no cap required, just authentication ──
discovery_actions := {"initialize", "discover", "describe", "ping", "health"}

allow if {
	requested_action in discovery_actions
	token  # ensure the request was authenticated
}

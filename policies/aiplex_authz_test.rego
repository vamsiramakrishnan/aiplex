package aiplex.authz_test

import rego.v1
import data.aiplex.authz

# Mock JWT token construction for tests. Real OPA decodes via
# io.jwt.decode_verify with the issuer's JWKs; for unit tests we
# stub the `claims` and exercise the allow rules.

# Helper: an input with the given scope string + path + method.
mk_input(scope, path, method) := {
    "attributes": {"request": {"http": {
        "headers": {"authorization": "Bearer fake"},
        "path": path,
        "method": method,
        "body": "{}",
    }}}
}

# ── Runs read ─────────────────────────────────────────────────────────────

test_runs_read_allowed if {
    authz.allow with input as mk_input("aiplex:runs:read", "/api/v1/runs", "GET")
        with authz.token as ["", "", {"scope": "aiplex:runs:read"}]
}

test_runs_read_subroute_allowed if {
    authz.allow with input as mk_input("aiplex:runs:read", "/api/v1/runs/r-1/events", "GET")
        with authz.token as ["", "", {"scope": "aiplex:runs:read"}]
}

test_runs_read_denied_without_scope if {
    not authz.allow with input as mk_input("", "/api/v1/runs", "GET")
        with authz.token as ["", "", {"scope": ""}]
}

# ── Runs operator actions ────────────────────────────────────────────────

test_runs_redrive_allowed_with_scope if {
    authz.allow with input as mk_input("aiplex:runs:redrive", "/api/v1/runs/r-1/redrive", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:redrive"}]
}

test_runs_reconcile_allowed_with_scope if {
    authz.allow with input as mk_input("aiplex:runs:reconcile", "/api/v1/runs/r-1/reconcile", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:reconcile"}]
}

test_runs_cancel_allowed_with_scope if {
    authz.allow with input as mk_input("aiplex:runs:cancel", "/api/v1/runs/r-1/cancel", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:cancel"}]
}

test_runs_signal_allowed_with_scope if {
    authz.allow with input as mk_input("aiplex:runs:signal", "/api/v1/runs/r-1/signal", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:signal"}]
}

test_runs_compensate_allowed_with_scope if {
    authz.allow with input as mk_input("aiplex:runs:compensate", "/api/v1/runs/r-1/compensate", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:compensate"}]
}

# ── Denials ───────────────────────────────────────────────────────────────

test_runs_redrive_denied_without_scope if {
    not authz.allow with input as mk_input("aiplex:runs:read", "/api/v1/runs/r-1/redrive", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:read"}]
}

test_runs_redrive_denied_with_wrong_action_scope if {
    not authz.allow with input as mk_input("aiplex:runs:cancel", "/api/v1/runs/r-1/redrive", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:cancel"}]
}

test_runs_unknown_action_denied if {
    not authz.allow with input as mk_input("aiplex:runs:redrive", "/api/v1/runs/r-1/delete", "POST")
        with authz.token as ["", "", {"scope": "aiplex:runs:redrive aiplex:runs:cancel"}]
}

"""Admin operations: budgets, model surfaces, tool connections, hook policies, usage config."""

from __future__ import annotations

from typing import Any

import pytest

from aiplex import (
    AccessPointRecord,
    AccessPointSkill,
    AccessPointSpec,
    AiplexError,
    Budget,
    BudgetLimit,
    BudgetMembers,
    BudgetPreview,
    BudgetPreviewRequest,
    BudgetPreviewSubject,
    BudgetRecord,
    BudgetWindow,
    Client,
    Feedback,
    GroupRef,
    Hook,
    HookPolicy,
    HookPolicyRecord,
    HookPolicyTest,
    HookPolicyTestResult,
    HookScope,
    ModelCapacityPolicy,
    ModelProvider,
    ModelSurface,
    ModelSurfaceAuth,
    ModelSurfaceBlock,
    ModelSurfaceSpec,
    RedactConfig,
    RedactRule,
    SurfaceCapacity,
    ToolAuth,
    ToolConnection,
    ToolConnectionSpec,
    ToolExecution,
    ToolPolicy,
    ToolRule,
    UsageConfig,
)
from conftest import FakeAPI

CONFLICT = {"code": "REVISION_CONFLICT", "message": "revision mismatch"}
NOT_FOUND = {"code": "NOT_FOUND", "message": "resource not found"}

BUDGET_SPEC: dict[str, Any] = {
    "id": "team",
    "display_name": "Platform",
    "parent": "org",
    "limit": {"unit": "cost", "currency": "USD", "amount": "250.00"},
    "window": {"kind": "calendar", "unit": "month", "time_zone": "UTC"},
    "members": {
        "groups": [{"source": "oidc", "id": "eng"}],
        "principals": ["p1"],
        "access_points": ["local"],
    },
    "on_unknown_cost": "deny",
    "thresholds": [50, 80, 100],
}


def budget_record(revision: int = 3, spec: dict[str, Any] | None = None) -> dict[str, Any]:
    return {"tenant_id": "t", "kind": "budget", "id": "team", "revision": revision, "spec": spec or BUDGET_SPEC}


# ---- budgets ----


def test_list_and_get_budget(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/budgets", json=[budget_record()])
    recs = client.list_budgets()
    assert isinstance(recs[0], BudgetRecord)
    assert recs[0].revision == 3
    b = recs[0].spec
    assert isinstance(b, Budget)
    assert b.limit == BudgetLimit(unit="cost", currency="USD", amount="250.00", raw=b.limit.raw)
    assert b.window.time_zone == "UTC" and b.window.hours is None
    assert b.members.groups == [GroupRef(source="oidc", id="eng", raw={"source": "oidc", "id": "eng"})]
    assert b.thresholds == [50, 80, 100]

    fake_api.route("GET", "/api/v1/budgets/team", json=budget_record(revision=4))
    assert client.get_budget("team").revision == 4
    with pytest.raises(ValueError):
        client.get_budget("")

    fake_api.route("GET", "/api/v1/budgets/missing", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc:
        client.get_budget("missing")
    assert exc.value.status_code == 404


def test_list_budgets_rejects_non_array(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/budgets", json={"a": 1})
    with pytest.raises(AiplexError) as exc:
        client.list_budgets()
    assert exc.value.code == "decode_error"


def test_put_budget_sends_revision_and_wire_shape(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("PUT", "/api/v1/budgets/team", json=budget_record(revision=1))
    spec = Budget(
        id="team",
        limit=BudgetLimit(unit="requests", amount="1000"),
        window=BudgetWindow(kind="tumbling", hours=24),
        members=BudgetMembers(principals=["p1"]),
    )
    rec = client.put_budget(spec)
    req = fake_api.requests[-1]
    assert req.headers["if-match"] == "0"
    assert req.body == {
        "id": "team",
        "limit": {"unit": "requests", "amount": "1000"},
        "window": {"kind": "tumbling", "hours": 24},
        "members": {"principals": ["p1"]},
    }
    assert rec.revision == 1
    client.put_budget(spec, revision=5)
    assert fake_api.requests[-1].headers["if-match"] == "5"
    with pytest.raises(ValueError):
        client.put_budget(spec, revision=-1)
    with pytest.raises(ValueError):
        client.put_budget({"id": "x"})  # type: ignore[arg-type]


def test_put_budget_conflict(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("PUT", "/api/v1/budgets/team", status=409, json=CONFLICT)
    spec = Budget(id="team", limit=BudgetLimit(unit="tokens", amount="1"), window=BudgetWindow(kind="calendar", unit="day", time_zone="UTC"))
    with pytest.raises(AiplexError) as exc:
        client.put_budget(spec, revision=2)
    assert exc.value.is_revision_conflict


def test_budget_roundtrip_keeps_unknown_fields() -> None:
    wire = {**BUDGET_SPEC, "future_field": {"a": 1}}
    b = Budget.from_wire(wire)
    assert b.to_wire() == wire


def test_budget_include_tool_calls_is_absent_or_explicit() -> None:
    # Absent stays None (not False); an explicit value round-trips.
    assert Budget.from_wire(BUDGET_SPEC).include_tool_calls is None
    on = Budget.from_wire({**BUDGET_SPEC, "include_tool_calls": True})
    assert on.include_tool_calls is True and on.to_wire()["include_tool_calls"] is True
    off = Budget.from_wire({**BUDGET_SPEC, "include_tool_calls": False})
    assert off.include_tool_calls is False
    assert "include_tool_calls" not in Budget.from_wire(BUDGET_SPEC).to_wire()


def test_delete_budget(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("DELETE", "/api/v1/budgets/team", status=204)
    assert client.delete_budget("team", 3) is None
    req = fake_api.requests[-1]
    assert req.method == "DELETE" and req.headers["if-match"] == "3"
    with pytest.raises(ValueError):
        client.delete_budget("team", 0)
    fake_api.route("DELETE", "/api/v1/budgets/stale", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc:
        client.delete_budget("stale", 1)
    assert exc.value.is_revision_conflict
    fake_api.route("DELETE", "/api/v1/budgets/gone", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc2:
        client.delete_budget("gone", 1)
    assert exc2.value.status_code == 404


def test_preview_budget(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "POST",
        "/api/v1/budgets/preview",
        json={
            "hypothetical": True,
            "plane": "llmplex",
            "access_point": "local",
            "draws": [
                {
                    "id": "team",
                    "direct": True,
                    "reasons": ["principal"],
                    "unit": "cost",
                    "currency": "USD",
                    "limit": "250.00",
                    "window": {"kind": "calendar", "unit": "month", "time_zone": "UTC"},
                    "draft": True,
                }
            ],
            "skipped": [{"id": "tok", "reason": "unit tokens does not count this plane"}],
            "draft": {"valid": False, "field": "limit.amount", "error": "bad"},
            "notes": ["n"],
        },
    )
    request = BudgetPreviewRequest(
        subject=BudgetPreviewSubject(principal="p1", groups=[GroupRef(source="oidc", id="eng")]),
        access_point="local",
        plane="llmplex",
        draft=Budget.from_wire(BUDGET_SPEC),
    )
    out = client.preview_budget(request)
    sent = fake_api.requests[-1].body
    assert sent["subject"] == {"principal": "p1", "groups": [{"source": "oidc", "id": "eng"}]}
    assert sent["draft"]["id"] == "team" and sent["plane"] == "llmplex"
    assert isinstance(out, BudgetPreview)
    assert out.hypothetical is True and out.draws[0].direct is True and out.draws[0].window.unit == "month"
    assert out.skipped[0].reason.startswith("unit")
    assert out.draft is not None and out.draft.valid is False and out.draft.field == "limit.amount"
    assert out.project is None
    with pytest.raises(ValueError):
        client.preview_budget(None)  # type: ignore[arg-type]


def test_preview_budget_forbidden(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("POST", "/api/v1/budgets/preview", status=403, json={"code": "FORBIDDEN", "message": "no"})
    with pytest.raises(AiplexError) as exc:
        client.preview_budget(BudgetPreviewRequest(subject=BudgetPreviewSubject()))
    assert exc.value.status_code == 403
    assert fake_api.requests[-1].body == {"subject": {}}


# ---- model surfaces ----

SURFACE_SPEC: dict[str, Any] = {
    "surface": {
        "id": "oa",
        "protocol": "openai",
        "origin": "https://api.openai.com",
        "models": ["gpt-x"],
        "provider": {"id": "openai", "stream_usage": "include", "novel": 1},
        "websocket": {"metering": "session", "max_session_seconds": 60},
    },
    "auth": {"type": "bearer", "in": "header", "name": "Authorization", "prefix": "Bearer ", "secret_ref": "openai-key"},
    "capacity_policy": {"cooldown_enabled": True, "headroom_fraction": 0.1},
    "newer_server_field": True,
}


def surface_record(revision: int = 2) -> dict[str, Any]:
    return {"tenant_id": "t", "kind": "model-surface", "id": "oa", "revision": revision, "spec": SURFACE_SPEC}


def test_put_model_surface(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("PUT", "/api/v1/llm/surfaces/oa", json=surface_record(1))
    spec = ModelSurfaceSpec(
        surface=ModelSurfaceBlock(
            id="oa",
            protocol="openai",
            origin="https://api.openai.com",
            provider=ModelProvider(id="openai"),
        ),
        auth=ModelSurfaceAuth(type="bearer", in_="header", name="Authorization", secret_ref="openai-key"),
        capacity_policy=ModelCapacityPolicy(cooldown_enabled=True),
    )
    rec = client.put_model_surface("oa", spec)
    req = fake_api.requests[-1]
    assert req.headers["if-match"] == "0"
    assert req.body == {
        "surface": {"id": "oa", "protocol": "openai", "origin": "https://api.openai.com", "provider": {"id": "openai"}},
        "auth": {"type": "bearer", "in": "header", "name": "Authorization", "secret_ref": "openai-key"},
        "capacity_policy": {"cooldown_enabled": True},
    }
    assert isinstance(rec, ModelSurface)
    assert rec.spec is not None and rec.spec.auth is not None and rec.spec.auth.secret_ref == "openai-key"
    assert rec.spec.auth.in_ == "header"
    # Fields this SDK does not model survive a read-modify-write.
    assert rec.spec.to_wire() == SURFACE_SPEC
    client.put_model_surface("oa", spec, revision=4)
    assert fake_api.requests[-1].headers["if-match"] == "4"
    with pytest.raises(ValueError):
        client.put_model_surface("", spec)
    with pytest.raises(ValueError):
        client.put_model_surface("oa", {"surface": {}})  # type: ignore[arg-type]


def test_put_model_surface_conflict_and_unknown(fake_api: FakeAPI, client: Client) -> None:
    spec = ModelSurfaceSpec(surface=ModelSurfaceBlock(id="oa", protocol="openai", origin="https://x"))
    fake_api.route("PUT", "/api/v1/llm/surfaces/oa", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc:
        client.put_model_surface("oa", spec, revision=9)
    assert exc.value.is_revision_conflict


def test_delete_model_surface(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("DELETE", "/api/v1/llm/surfaces/oa", status=204)
    assert client.delete_model_surface("oa", 2) is None
    assert fake_api.requests[-1].headers["if-match"] == "2"
    with pytest.raises(ValueError):
        client.delete_model_surface("oa", 0)
    fake_api.route("DELETE", "/api/v1/llm/surfaces/nope", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc:
        client.delete_model_surface("nope", 1)
    assert exc.value.status_code == 404
    fake_api.route("DELETE", "/api/v1/llm/surfaces/stale", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc2:
        client.delete_model_surface("stale", 1)
    assert exc2.value.is_revision_conflict


def test_get_model_surface_capacity(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/llm/surfaces/oa/capacity",
        json={
            "surface_id": "oa",
            "observed": True,
            "scope": "replica",
            "limits": {
                "provider": "openai",
                "observed_at": "2026-11-13T10:00:00Z",
                "http_status": 429,
                "classification": "rate_limited",
                "requests": {"limit": 100, "remaining": 0, "reset_at": "2026-11-13T10:01:00Z"},
                "tokens": {"limit": 1000},
            },
            "capacity_policy": {
                "enabled": True,
                "policy": {"cooldown_enabled": True, "max_cooldown_seconds": 30},
                "cooldown": {"phase": "cooling", "retry_after_ms": 500},
            },
        },
    )
    cap = client.get_model_surface_capacity("oa")
    assert isinstance(cap, SurfaceCapacity)
    assert cap.observed is True and cap.scope == "replica"
    assert cap.limits is not None
    assert cap.limits.requests is not None and cap.limits.requests.remaining == 0
    # Unknown members stay None, never zero.
    assert cap.limits.tokens is not None and cap.limits.tokens.remaining is None
    assert cap.limits.retry_after_ms is None and cap.limits.input_tokens is None
    assert cap.capacity_policy.enabled is True
    assert cap.capacity_policy.cooldown is not None and cap.capacity_policy.cooldown.phase == "cooling"
    assert cap.capacity_policy.policy is not None and cap.capacity_policy.policy.min_weight is None


def test_get_model_surface_capacity_unobserved_and_errors(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/llm/surfaces/oa/capacity",
        json={"surface_id": "oa", "observed": False, "scope": "replica", "capacity_policy": {"enabled": False}},
    )
    cap = client.get_model_surface_capacity("oa")
    assert cap.limits is None and cap.capacity_policy.policy is None
    fake_api.route("GET", "/api/v1/llm/surfaces/nope/capacity", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError):
        client.get_model_surface_capacity("nope")
    with pytest.raises(ValueError):
        client.get_model_surface_capacity("")


# ---- tool connections ----

TOOL_SPEC: dict[str, Any] = {
    "id": "gh",
    "resource_url": "https://tools.example/mcp",
    "execution": {"endpoint": "https://tools.example/mcp", "identity_ref": "", "credential_mode": "connection-auth", "other": 1},
    "auth": {"type": "bearer", "secret_ref": "gh-token"},
    "tools": {
        "allow": ["read_*"],
        "read_only": True,
        "rules": [{"id": "r1", "when": "tool == 'x'", "effect": "deny", "message": "no"}],
    },
}


def tool_record(revision: int = 2) -> dict[str, Any]:
    return {"tenant_id": "t", "kind": "tool-connection", "id": "gh", "revision": revision, "spec": TOOL_SPEC}


def test_put_tool_connection(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("PUT", "/api/v1/mcp/connections/gh", json=tool_record(1))
    spec = ToolConnectionSpec(
        resource_url="https://tools.example/mcp",
        execution=ToolExecution(endpoint="https://tools.example/mcp", credential_mode="connection-auth"),
        auth=ToolAuth(type="bearer", secret_ref="gh-token"),
        tools=ToolPolicy(allow=["read_*"], rules=[ToolRule(id="r1", when="true", effect="deny")]),
    )
    rec = client.put_tool_connection("gh", spec)
    req = fake_api.requests[-1]
    assert req.headers["if-match"] == "0"
    assert req.body == {
        "id": "gh",
        "resource_url": "https://tools.example/mcp",
        "execution": {"endpoint": "https://tools.example/mcp", "credential_mode": "connection-auth"},
        "auth": {"type": "bearer", "secret_ref": "gh-token"},
        "tools": {"allow": ["read_*"], "rules": [{"id": "r1", "when": "true", "effect": "deny"}]},
    }
    assert isinstance(rec, ToolConnection)
    assert rec.revision == 1 and rec.spec is not None
    assert rec.spec.auth is not None and rec.spec.auth.secret_ref == "gh-token"
    assert rec.spec.tools is not None and rec.spec.tools.read_only is True and rec.spec.tools.rules[0].effect == "deny"
    assert rec.spec.to_wire() == TOOL_SPEC
    client.put_tool_connection("gh", spec, revision=2)
    assert fake_api.requests[-1].headers["if-match"] == "2"


def test_put_tool_connection_validation_and_errors(fake_api: FakeAPI, client: Client) -> None:
    with pytest.raises(ValueError):
        client.put_tool_connection("gh", ToolConnectionSpec(id="other"))
    with pytest.raises(ValueError):
        client.put_tool_connection("gh", {"id": "gh"})  # type: ignore[arg-type]
    with pytest.raises(ValueError):
        client.put_tool_connection("gh", ToolConnectionSpec(), revision=-1)
    fake_api.route("PUT", "/api/v1/mcp/connections/gh", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc:
        client.put_tool_connection("gh", ToolConnectionSpec(), revision=3)
    assert exc.value.is_revision_conflict


def test_delete_tool_connection(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("DELETE", "/api/v1/mcp/connections/gh", status=204)
    assert client.delete_tool_connection("gh", 2) is None
    assert fake_api.requests[-1].headers["if-match"] == "2"
    with pytest.raises(ValueError):
        client.delete_tool_connection("gh", 0)
    fake_api.route("DELETE", "/api/v1/mcp/connections/nope", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc:
        client.delete_tool_connection("nope", 1)
    assert exc.value.status_code == 404
    fake_api.route("DELETE", "/api/v1/mcp/connections/stale", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc2:
        client.delete_tool_connection("stale", 1)
    assert exc2.value.is_revision_conflict


# ---- hook policies ----

HOOK_SPEC: dict[str, Any] = {
    "id": "pii",
    "description": "mask pii",
    "scope": {"kind": "tenant"},
    "planes": ["llmplex"],
    "streaming": "buffer",
    "hooks": [
        {
            "id": "mask",
            "type": "redact",
            "phases": ["request", "response"],
            "timeout_ms": 500,
            "redact": {"rules": [{"id": "email", "builtin": "email", "action": "mask", "placeholder": "[EMAIL]"}]},
        },
        {"id": "sidecar", "type": "http", "phases": ["request"], "http": {"endpoint": "https://hook.example/x", "auth_secret_ref": "hook-key", "fail_open": False}},
    ],
}


def hook_record(revision: int = 2) -> dict[str, Any]:
    return {"id": "pii", "revision": revision, "spec": HOOK_SPEC}


def test_list_and_get_hook_policies(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/hooks/policies/", json=[hook_record()])
    recs = client.list_hook_policies()
    assert isinstance(recs[0], HookPolicyRecord) and recs[0].revision == 2
    p = recs[0].policy
    assert isinstance(p, HookPolicy)
    assert p.scope == HookScope(kind="tenant", id=None, raw={"kind": "tenant"})
    assert p.hooks[0].redact is not None and p.hooks[0].redact.rules[0].placeholder == "[EMAIL]"
    assert p.hooks[1].http is not None and p.hooks[1].http.auth_secret_ref == "hook-key"
    assert p.hooks[1].http.fail_open is False
    assert p.hooks[1].timeout_ms is None
    assert p.to_wire() == HOOK_SPEC

    fake_api.route("GET", "/api/v1/hooks/policies/pii", json=hook_record(5))
    assert client.get_hook_policy("pii").revision == 5
    fake_api.route("GET", "/api/v1/hooks/policies/nope", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc:
        client.get_hook_policy("nope")
    assert exc.value.status_code == 404
    with pytest.raises(ValueError):
        client.get_hook_policy("")


def test_put_hook_policy(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("PUT", "/api/v1/hooks/policies/pii", json=hook_record(1))
    policy = HookPolicy(
        id="pii",
        scope=HookScope(kind="tenant"),
        hooks=[
            Hook(
                id="mask",
                type="redact",
                phases=["request"],
                redact=RedactConfig(rules=[RedactRule(id="email", builtin="email", action="mask")]),
            )
        ],
    )
    rec = client.put_hook_policy(policy)
    req = fake_api.requests[-1]
    assert req.headers["if-match"] == "0"
    assert req.body == {
        "id": "pii",
        "scope": {"kind": "tenant"},
        "hooks": [
            {
                "id": "mask",
                "type": "redact",
                "phases": ["request"],
                "redact": {"rules": [{"id": "email", "builtin": "email", "action": "mask"}]},
            }
        ],
    }
    assert rec.revision == 1
    client.put_hook_policy(policy, revision=6)
    assert fake_api.requests[-1].headers["if-match"] == "6"
    with pytest.raises(ValueError):
        client.put_hook_policy(policy, revision=-2)
    with pytest.raises(ValueError):
        client.put_hook_policy({"id": "pii"})  # type: ignore[arg-type]


def test_put_hook_policy_conflict_and_server_refusal(fake_api: FakeAPI, client: Client) -> None:
    policy = HookPolicy(id="pii", scope=HookScope(kind="tenant"), hooks=[])
    fake_api.route("PUT", "/api/v1/hooks/policies/pii", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc:
        client.put_hook_policy(policy, revision=1)
    assert exc.value.is_revision_conflict
    fake_api.route("PUT", "/api/v1/hooks/policies/bad", status=400, json={"code": "INVALID", "message": "hooks must list 1 to 16 hooks"})
    with pytest.raises(AiplexError) as exc2:
        client.put_hook_policy(HookPolicy(id="bad", scope=HookScope(kind="tenant"), hooks=[]))
    assert exc2.value.status_code == 400
    # An empty hook list is sent as an empty list so the server refuses it.
    assert fake_api.requests[-1].body["hooks"] == []


def test_delete_hook_policy(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("DELETE", "/api/v1/hooks/policies/pii", status=204)
    assert client.delete_hook_policy("pii", 2) is None
    assert fake_api.requests[-1].headers["if-match"] == "2"
    with pytest.raises(ValueError):
        client.delete_hook_policy("pii", 0)
    fake_api.route("DELETE", "/api/v1/hooks/policies/nope", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError) as exc:
        client.delete_hook_policy("nope", 1)
    assert exc.value.status_code == 404
    fake_api.route("DELETE", "/api/v1/hooks/policies/stale", status=409, json=CONFLICT)
    with pytest.raises(AiplexError) as exc2:
        client.delete_hook_policy("stale", 1)
    assert exc2.value.is_revision_conflict


def test_test_hook_policy(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "POST",
        "/api/v1/hooks/policies/pii/test",
        json={
            "policy": "pii",
            "phase": "request",
            "decision": "transform",
            "texts": ["mail [EMAIL]"],
            "results": [
                {"id": "mask", "type": "redact", "phase": "request", "decision": "transform", "rules": [{"id": "email", "count": 1}], "duration_ms": 0}
            ],
            "uninspected_parts": 0,
        },
    )
    out = client.test_hook_policy("pii", HookPolicyTest(texts=["mail a@b.co"], phase="request"))
    assert fake_api.requests[-1].body == {"phase": "request", "texts": ["mail a@b.co"]}
    assert isinstance(out, HookPolicyTestResult)
    assert out.decision == "transform" and out.texts == ["mail [EMAIL]"]
    assert out.results[0].rules[0].count == 1 and out.results[0].duration_ms == 0
    assert out.results[0].fail_open_used is None
    assert out.uninspected_parts == 0 and out.code is None

    fake_api.route(
        "POST",
        "/api/v1/hooks/policies/deny/test",
        json={"policy": "deny", "phase": "request", "decision": "deny", "code": "HOOK_DENIED", "message": "no", "results": []},
    )
    refused = client.test_hook_policy("deny", HookPolicyTest(texts=["x"]))
    # Absent counts are unknown, never zero.
    assert refused.code == "HOOK_DENIED" and refused.texts == [] and refused.uninspected_parts is None
    fake_api.route("POST", "/api/v1/hooks/policies/nope/test", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError):
        client.test_hook_policy("nope", HookPolicyTest(texts=["x"]))
    with pytest.raises(ValueError):
        client.test_hook_policy("pii", None)  # type: ignore[arg-type]


# ---- usage configuration and feedback ----


def test_get_usage_config(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/usage/config",
        json={"trace_url_template": "https://t/{trace_id}", "feedback": True, "feedback_comments": False, "tag_keys_allowed": ["team"]},
    )
    fake_api.route("GET", "/api/v1/usage/config", status=401, json={"code": "UNAUTHENTICATED", "message": "no"})
    cfg = client.get_usage_config()
    assert isinstance(cfg, UsageConfig)
    assert cfg.trace_url_template == "https://t/{trace_id}"
    assert cfg.feedback is True and cfg.feedback_comments is False and cfg.tag_keys_allowed == ["team"]
    with pytest.raises(AiplexError):
        client.get_usage_config()


def test_list_usage_feedback(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/usage/records/ul_1/feedback",
        json={
            "record_id": "ul_1",
            "feedback": [
                {"id": "f2", "record_id": "ul_1", "by": "p", "created_at": "2026-11-13T10:00:00Z", "score": 0, "label": "ok"},
                {"id": "f1", "record_id": "ul_1", "by": "p", "created_at": "2026-11-13T09:00:00Z", "score": -0.5},
            ],
        },
    )
    items = client.list_usage_feedback("ul_1")
    assert all(isinstance(i, Feedback) for i in items)
    assert [i.id for i in items] == ["f2", "f1"]
    assert items[0].score == 0.0 and items[1].label is None

    fake_api.route("GET", "/api/v1/usage/records/empty/feedback", json={"record_id": "empty", "feedback": None})
    assert client.list_usage_feedback("empty") == []
    fake_api.route("GET", "/api/v1/usage/records/nope/feedback", status=404, json=NOT_FOUND)
    with pytest.raises(AiplexError):
        client.list_usage_feedback("nope")
    with pytest.raises(ValueError):
        client.list_usage_feedback("")
    fake_api.route("GET", "/api/v1/usage/records/bad/feedback", json=[1])
    with pytest.raises(AiplexError) as exc:
        client.list_usage_feedback("bad")
    assert exc.value.code == "decode_error"


# ---- access point skills ----


def test_access_point_skill_typed() -> None:
    spec = AccessPointSpec.from_wire({"id": "ap", "caller_kinds": ["agent"], "skills": [{"instance_ref": "s1"}]})
    assert spec.skills == [AccessPointSkill(instance_ref="s1", raw={"instance_ref": "s1"})]
    assert spec.to_wire()["skills"] == [{"instance_ref": "s1"}]
    built = AccessPointSpec(id="ap", skills=[AccessPointSkill(instance_ref="s2")])
    assert built.to_wire()["skills"] == [{"instance_ref": "s2"}]
    # Missing instance_ref is unknown (None), not an empty string.
    assert AccessPointSkill.from_wire({}).instance_ref is None
    rec = AccessPointRecord.from_wire({"id": "ap", "revision": 1, "spec": {"id": "ap", "skills": [{"instance_ref": "s1"}]}})
    assert rec.spec.skills[0].instance_ref == "s1"


def test_tool_execution_bounds_are_typed_and_round_trip() -> None:
    wire = {
        "id": "gh",
        "execution": {
            "endpoint": "https://tools.example/mcp",
            "credential_mode": "connection-auth",
            "max_response_bytes": 1048576,
            "max_duration_seconds": 30,
        },
    }
    spec = ToolConnectionSpec.from_wire(wire)
    assert spec.execution is not None
    assert (spec.execution.max_response_bytes, spec.execution.max_duration_seconds) == (1048576, 30)
    assert spec.to_wire() == wire
    # A bound the record does not carry is None, and is not written back.
    unset = ToolConnectionSpec.from_wire({"id": "gh", "execution": {"endpoint": "https://tools.example/mcp"}})
    assert unset.execution is not None
    assert unset.execution.max_response_bytes is None and unset.execution.max_duration_seconds is None
    assert "max_response_bytes" not in unset.to_wire()["execution"]
    nulled = ToolExecution.from_wire({"max_response_bytes": None, "max_duration_seconds": None})
    assert nulled.max_response_bytes is None and nulled.max_duration_seconds is None
    zero = ToolExecution.from_wire({"max_response_bytes": 0, "max_duration_seconds": 0})
    assert (zero.max_response_bytes, zero.max_duration_seconds) == (0, 0)


def test_tool_connection_read_modify_write_keeps_execution_bounds(fake_api: FakeAPI, client: Client) -> None:
    record = tool_record(3)
    record["spec"] = {
        **TOOL_SPEC,
        "execution": {**TOOL_SPEC["execution"], "max_response_bytes": 4096, "max_duration_seconds": 10},
    }
    fake_api.route("GET", "/api/v1/mcp/connections/gh", json=record)
    fake_api.route("PUT", "/api/v1/mcp/connections/gh", json=record)
    current = client.get_tool_connection("gh")
    assert current.spec is not None
    wire = current.spec.to_wire()
    wire["tools"] = {"allow": ["write_*"], "read_only": False}
    client.put_tool_connection("gh", ToolConnectionSpec.from_wire(wire), revision=current.revision)
    body = fake_api.requests[-1].body
    assert body["execution"]["max_response_bytes"] == 4096
    assert body["execution"]["max_duration_seconds"] == 10
    assert body["execution"]["other"] == 1

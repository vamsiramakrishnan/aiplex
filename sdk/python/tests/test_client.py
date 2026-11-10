from __future__ import annotations

import socket
from decimal import Decimal
from typing import Any

import pytest

from aiplex import (
    AccessPointModel,
    AccessPointSpec,
    AccessPointTool,
    AiplexError,
    Client,
    ModelPrice,
    ModelPriceRate,
    UsageFilter,
)
from conftest import FakeAPI

WHOAMI_SAMPLE: dict[str, Any] = {
    "configuration_revision": 2,
    "identity": {
        "principal_id": "principal-020d277e494066b68e8581cd77984817925485a3bdf7d9e0c995f871a626963c",
        "subject": "developer",
        "email": "developer@localhost",
        "display_name": "Local developer",
        "groups": ["aiplex-local-admins"],
        "domain": "",
        "provider": "http://127.0.0.1:35789",
        "pool_id": "aiplex-local",
        "is_workforce": True,
    },
    "roles": ["admin"],
    "scopes": ["a2a:*", "aiplex:*", "llm:*", "mcp:*", "skill:*"],
    "group_scopes": ["a2a:*", "aiplex:*", "llm:*", "mcp:*", "skill:*"],
    "matched_bindings": ["local-admin"],
}

ACCESS_POINT_SAMPLE: dict[str, Any] = {
    "tenant_id": "development",
    "kind": "access-point",
    "id": "local",
    "revision": 1,
    "spec": {
        "caller_kinds": ["user", "agent", "client"],
        "display_name": "Local development",
        "id": "local",
        "models": [
            {"name": "mock-gpt", "native_model": "mock-gpt", "surface_ref": "local-models"},
            {"name": "mock-claude", "native_model": "mock-claude", "surface_ref": "local-messages"},
        ],
        "subjects": {},
        "tools": [{"connection_ref": "local-tools"}],
    },
}

CONNECT_SAMPLE: dict[str, Any] = {
    "access_point_id": "local",
    "client": "claude-code",
    "base_urls": {
        "anthropic": "http://127.0.0.1:19190/ap/local",
        "openai": "http://127.0.0.1:19190/ap/local/v1",
    },
    "endpoints": [],
    "models": [{"name": "mock-gpt", "protocols": ["openai-chat-completions"]}],
    "mcp_url": "http://127.0.0.1:19190/ap/local/mcp",
    "mcp_catalog_url": "http://127.0.0.1:19190/ap/local/v0.1/servers",
    "auth": {"scheme": "bearer", "token_env": "AIPLEX_TOKEN"},
    "snippets": [
        {
            "title": "Claude Code",
            "language": "json",
            "content": "{}",
        }
    ],
    "notes": ["Rendered by ClientPlex"],
}

USAGE_RECORD_SAMPLE: dict[str, Any] = {
    "id": "ul_f22b1f5ea41015a35d9f95ab8ec99b06",
    "tenant_id": "development",
    "started_at": "2026-11-10T10:23:00.712680814Z",
    "finished_at": "2026-11-10T10:23:00.718759131Z",
    "latency_ms": 6,
    "plane": "llmplex",
    "provider": "openai-compatible",
    "resource_id": "local-models",
    "operation": "compat.chat-completions",
    "model_id": "mock-gpt",
    "model_version": "mock-gpt",
    "principal_id": "principal-020d",
    "principal_display": "developer@localhost",
    "access_point_id": "local",
    "caller_kind": "user",
    "request_id": "req-14",
    "trace_id": "b8f1efc0972f3351b4518609d0dbdd32",
    "span_id": "2563ef6ddc4ac930",
    "streaming": False,
    "status": "ok",
    "http_status": 200,
    "source": "managed",
    "requests": 1,
    "usage": {
        "input_tokens": 1,
        "output_tokens": 9,
        "cache_read_tokens": None,
        "cache_write_tokens": None,
        "reasoning_tokens": None,
        "total_tokens": 10,
    },
    "cost": {
        "basis": "estimated",
        "currency": "USD",
        "amount": "6",
        "scale": 6,
        "amount_decimal": "0.000006",
    },
}

PRICE_SAMPLE: dict[str, Any] = {
    "tenant_id": "development",
    "kind": "model-price",
    "id": "mock-gpt",
    "revision": 1,
    "spec": {
        "currency": "USD",
        "id": "mock-gpt",
        "meters": {
            "input_tokens": {"denominator": 100, "numerator": 15},
            "output_tokens": {"denominator": 100, "numerator": 60},
        },
        "model_id": "mock-gpt",
        "scale": 6,
    },
}

SURFACE_SAMPLE: dict[str, Any] = {
    "tenant_id": "development",
    "kind": "model-surface",
    "id": "local-models",
    "revision": 1,
    "spec": {
        "auth": {"type": "none"},
        "surface": {
            "id": "local-models",
            "models": ["mock-gpt"],
            "operations": [],
            "origin": "http://127.0.0.1:37417",
            "protocol": "openai-chat",
            "provider": {
                "id": "openai-compatible",
                "stream_usage": "inject",
                "usage_semantics": "openai",
            },
        },
    },
}

TOOL_CONN_SAMPLE: dict[str, Any] = {
    "tenant_id": "development",
    "kind": "tool-connection",
    "id": "local-tools",
    "revision": 1,
    "spec": {
        "auth": {"type": "none"},
        "execution": {
            "allowed_tools": ["echo", "add", "reset_counter"],
            "credential_mode": "connection-auth",
            "endpoint": "http://127.0.0.1:35367",
            "id": "local-tools",
            "mode": "stateless-json",
            "protocol_version": "2025-06-18",
        },
        "id": "local-tools",
        "resource_url": "http://127.0.0.1:35367",
    },
}


def test_configuration_env_and_validation(monkeypatch: pytest.MonkeyPatch, fake_api: FakeAPI) -> None:
    monkeypatch.delenv("AIPLEX_URL", raising=False)
    monkeypatch.delenv("AIPLEX_TOKEN", raising=False)

    with pytest.raises(ValueError, match="base_url"):
        Client(token="tok")

    with pytest.raises(ValueError, match="token"):
        Client(base_url="http://127.0.0.1:9999")

    monkeypatch.setenv("AIPLEX_URL", f"{fake_api.url}///")
    monkeypatch.setenv("AIPLEX_TOKEN", "env-secret-token")

    fake_api.route("GET", "/api/v1/iam/whoami", json=WHOAMI_SAMPLE)
    with Client() as c:
        assert repr(c) == f"Client(base_url={fake_api.url!r}, token=<redacted>)"
        c.whoami()

    assert fake_api.requests[0].headers["authorization"] == "Bearer env-secret-token"
    assert fake_api.requests[0].path == "/api/v1/iam/whoami"


def test_auth_and_headers(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/iam/whoami", json=WHOAMI_SAMPLE)
    client.whoami()
    req = fake_api.requests[0]
    assert req.headers["authorization"] == "Bearer test-token-123"
    assert req.headers["accept"] == "application/json"
    assert req.headers["user-agent"] == "aiplex-sdk-python/0.1.0"


def test_secrecy_redaction_and_transport_error(fake_api: FakeAPI, client: Client) -> None:
    assert "test-token-123" not in repr(client)
    assert "test-token-123" not in str(client)
    assert "token=<redacted>" in repr(client)

    fake_api.route(
        "GET",
        "/api/v1/iam/whoami",
        status=401,
        json={
            "code": "INVALID_TOKEN_test-token-123",
            "message": "bad token test-token-123 provided",
            "request_id": "req-test-token-123",
            "trace_id": "trace-test-token-123",
            "details": {"leaked": "test-token-123"},
        },
    )
    with pytest.raises(AiplexError) as exc_info:
        client.whoami()

    err = exc_info.value
    assert "test-token-123" not in str(err)
    assert "test-token-123" not in repr(err)
    assert err.message == "bad token [redacted] provided"
    assert err.code == "INVALID_TOKEN_[redacted]"
    assert err.request_id == "req-[redacted]"
    assert err.trace_id == "trace-[redacted]"
    assert err.details == {"leaked": "[redacted]"}

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.bind(("127.0.0.1", 0))
    closed_port = sock.getsockname()[1]
    sock.close()

    with Client(base_url=f"http://127.0.0.1:{closed_port}", token="test-token-123", timeout=1.0) as bad_client:
        with pytest.raises(AiplexError) as transport_exc:
            bad_client.whoami()

    terr = transport_exc.value
    assert terr.status_code is None
    assert terr.code == "transport_error"
    assert "request failed:" in terr.message
    assert "test-token-123" not in str(terr)
    assert "test-token-123" not in repr(terr)


def test_error_envelope_and_non_json_and_conflict(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/access-points/does-not-exist",
        status=404,
        json={
            "code": "NOT_FOUND",
            "message": "resource not found",
            "request_id": "req-33",
            "trace_id": "5adfc2f17def1fdc9fd7bfd077aa553b",
            "details": {"id": "does-not-exist"},
        },
    )
    with pytest.raises(AiplexError) as exc_404:
        client.get_access_point("does-not-exist")
    e404 = exc_404.value
    assert e404.status_code == 404
    assert e404.code == "NOT_FOUND"
    assert e404.message == "resource not found"
    assert e404.request_id == "req-33"
    assert e404.trace_id == "5adfc2f17def1fdc9fd7bfd077aa553b"
    assert e404.details == {"id": "does-not-exist"}
    assert not e404.is_revision_conflict
    assert str(e404) == "aiplex: NOT_FOUND (404): resource not found"

    fake_api.route(
        "GET",
        "/api/v1/iam/whoami",
        status=502,
        raw_body=b"<html>Bad Gateway</html>",
        content_type="text/html",
    )
    with pytest.raises(AiplexError) as exc_502:
        client.whoami()
    e502 = exc_502.value
    assert e502.status_code == 502
    assert e502.code == "unknown"
    assert "Bad Gateway" in e502.message

    fake_api.route(
        "DELETE",
        "/api/v1/access-points/local",
        status=409,
        json={"code": "REVISION_CONFLICT", "message": "revision mismatch"},
    )
    with pytest.raises(AiplexError) as exc_409:
        client.delete_access_point("local", revision=2)
    assert exc_409.value.is_revision_conflict is True


def test_whoami(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/iam/whoami", json=WHOAMI_SAMPLE)
    res = client.whoami()
    assert res.configuration_revision == 2
    assert res.identity is not None
    assert res.identity.principal_id == "principal-020d277e494066b68e8581cd77984817925485a3bdf7d9e0c995f871a626963c"
    assert res.identity.email == "developer@localhost"
    assert res.identity.is_workforce is True
    assert res.roles == ["admin"]
    assert "llm:*" in res.scopes
    assert res.matched_bindings == ["local-admin"]


def test_access_points_crud(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/access-points/", json=[ACCESS_POINT_SAMPLE])
    records = client.list_access_points()
    assert fake_api.requests[-1].path == "/api/v1/access-points/"
    assert len(records) == 1
    assert records[0].id == "local"
    assert records[0].revision == 1
    assert records[0].spec.display_name == "Local development"
    assert records[0].spec.models[0].name == "mock-gpt"
    assert records[0].spec.tools[0].connection_ref == "local-tools"

    fake_api.route("GET", "/api/v1/access-points/local", json=ACCESS_POINT_SAMPLE)
    rec = client.get_access_point("local")
    assert rec.id == "local"

    spec = AccessPointSpec(
        id="local",
        display_name="Local development",
        caller_kinds=["user", "agent"],
        models=[AccessPointModel(name="mock-gpt", surface_ref="local-models", native_model="mock-gpt")],
        tools=[AccessPointTool(connection_ref="local-tools")],
    )
    fake_api.route("PUT", "/api/v1/access-points/local", json=ACCESS_POINT_SAMPLE)
    client.put_access_point(spec, revision=0)
    put_req0 = fake_api.requests[-1]
    assert put_req0.headers["if-match"] == "0"
    assert put_req0.body["id"] == "local"
    assert put_req0.body["display_name"] == "Local development"

    fake_api.route("PUT", "/api/v1/access-points/local", json=ACCESS_POINT_SAMPLE)
    client.put_access_point(spec, revision=7)
    put_req7 = fake_api.requests[-1]
    assert put_req7.headers["if-match"] == "7"

    with pytest.raises(ValueError):
        client.delete_access_point("local", revision=0)
    assert len(fake_api.requests) == 4

    fake_api.route("DELETE", "/api/v1/access-points/local", status=204)
    assert client.delete_access_point("local", revision=3) is None
    del_req = fake_api.requests[-1]
    assert del_req.headers["if-match"] == "3"


def test_connect_access_point(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/overview/access-points/local/connect", json=CONNECT_SAMPLE)
    cfg = client.connect_access_point("local")
    assert fake_api.requests[-1].path == "/api/v1/overview/access-points/local/connect"
    assert fake_api.requests[-1].query == {}
    assert cfg.access_point_id == "local"
    assert cfg.base_urls["openai"] == "http://127.0.0.1:19190/ap/local/v1"

    fake_api.route("GET", "/api/v1/overview/access-points/local/connect", json=CONNECT_SAMPLE)
    client.connect_access_point("local", client="claude-code")
    assert fake_api.requests[-1].query == {"client": "claude-code"}


def test_usage_records_and_unknown_is_not_zero(fake_api: FakeAPI, client: Client) -> None:
    with pytest.raises(ValueError):
        client.list_usage_records(limit=0)
    with pytest.raises(ValueError):
        client.list_usage_records(limit=1001)
    assert len(fake_api.requests) == 0

    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [USAGE_RECORD_SAMPLE]},
    )
    flt = UsageFilter(
        plane="llmplex",
        model_id="mock-gpt",
        batch=False,
        since="2026-11-10T00:00:00Z",
    )
    page = client.list_usage_records(flt, limit=10, cursor="cur-1")
    req = fake_api.requests[-1]
    assert req.query == {
        "plane": "llmplex",
        "model_id": "mock-gpt",
        "batch": "false",
        "since": "2026-11-10T00:00:00Z",
        "limit": "10",
        "cursor": "cur-1",
    }
    assert page.next_cursor is None
    assert len(page.records) == 1
    r = page.records[0]
    assert r.id == "ul_f22b1f5ea41015a35d9f95ab8ec99b06"
    assert r.usage is not None
    assert r.usage.input_tokens == 1
    assert r.usage.output_tokens == 9
    assert r.usage.cache_read_tokens is None
    assert r.usage.cache_write_tokens is None
    assert r.usage.reasoning_tokens is None
    assert r.usage.total_tokens == 10
    assert r.cost is not None
    assert r.cost.basis == "estimated"
    assert r.cost.currency == "USD"
    assert r.cost.amount_minor == 6
    assert r.cost.scale == 6
    assert r.cost.amount == Decimal("0.000006")

    unknown_record = {
        "id": "ul_unknown",
        "status": "unknown",
        "usage": {
            "input_tokens": None,
            "output_tokens": None,
            "cache_read_tokens": None,
            "cache_write_tokens": None,
            "reasoning_tokens": None,
            "total_tokens": None,
        },
        "cost": {"basis": "unknown"},
    }
    fake_api.route("GET", "/api/v1/usage/records/ul_unknown", json=unknown_record)
    ur = client.get_usage_record("ul_unknown")
    assert ur.usage is not None
    assert ur.usage.input_tokens is None
    assert ur.usage.output_tokens is None
    assert ur.usage.total_tokens is None
    assert ur.cost is not None
    assert ur.cost.basis == "unknown"
    assert ur.cost.amount is None
    assert ur.cost.amount_minor is None
    assert ur.cost.scale is None
    assert ur.cost.currency is None


def test_usage_summary_and_validation(fake_api: FakeAPI, client: Client) -> None:
    with pytest.raises(ValueError):
        client.usage_summary(group_by=["bogus"])
    with pytest.raises(ValueError):
        client.usage_summary(group_by=["model_id", "model_id"])
    with pytest.raises(ValueError):
        client.usage_summary(bucket="week")
    assert len(fake_api.requests) == 0

    summary_payload = {
        "group_by": ["model_id", "plane"],
        "groups": [
            {
                "key": {"model_id": "mock-gpt", "plane": "llmplex"},
                "records": 1,
                "requests": 1,
                "errors": 0,
                "denied": 0,
                "unknown_status": 0,
                "tokens": {"input_tokens": 1, "output_tokens": 9},
                "tokens_unknown": {"cache_read_tokens": 1},
                "unknown_usage_records": 0,
                "cost": [
                    {
                        "currency": "USD",
                        "basis": "estimated",
                        "amount": "6",
                        "scale": 6,
                        "records": 1,
                        "amount_decimal": "0.000006",
                    }
                ],
                "unknown_cost_records": 0,
            }
        ],
        "records_scanned": 1,
        "truncated": False,
    }
    fake_api.route("GET", "/api/v1/usage/summary", json=summary_payload)
    summary = client.usage_summary(group_by=["model_id", "", "plane"], bucket="hour")
    req = fake_api.requests[-1]
    assert req.query == {"group_by": "model_id,plane", "bucket": "hour"}
    assert summary.records_scanned == 1
    assert summary.truncated is False
    grp = summary.groups[0]
    assert grp.tokens["input_tokens"] == 1
    assert grp.tokens.get("total_tokens") is None
    assert grp.tokens_unknown.get("reasoning_tokens") is None
    assert grp.cost[0].amount == Decimal("0.000006")


def test_prices_crud(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/usage/prices/", json=[PRICE_SAMPLE])
    prices = client.list_prices()
    assert fake_api.requests[-1].path == "/api/v1/usage/prices/"
    assert len(prices) == 1
    assert prices[0].id == "mock-gpt"
    assert prices[0].price.meters["input_tokens"].numerator == 15
    assert prices[0].price.meters["input_tokens"].denominator == 100

    null_price_sample = {
        "id": "null-price",
        "revision": 1,
        "spec": {
            "id": "null-price",
            "meters": {"input_tokens": {"numerator": None, "denominator": None}},
        },
    }
    fake_api.route("GET", "/api/v1/usage/prices/null-price", json=null_price_sample)
    np = client.get_price("null-price")
    assert np.price.meters["input_tokens"].numerator is None
    assert np.price.meters["input_tokens"].denominator is None

    price_spec = ModelPrice(
        id="mock-gpt",
        model_id="mock-gpt",
        currency="USD",
        scale=6,
        meters={"input_tokens": ModelPriceRate(numerator=15, denominator=100)},
    )
    fake_api.route("PUT", "/api/v1/usage/prices/mock-gpt", json=PRICE_SAMPLE)
    client.put_price(price_spec, revision=2)
    put_req = fake_api.requests[-1]
    assert put_req.headers["if-match"] == "2"
    assert put_req.body == {
        "id": "mock-gpt",
        "model_id": "mock-gpt",
        "currency": "USD",
        "scale": 6,
        "meters": {"input_tokens": {"numerator": 15, "denominator": 100}},
    }

    with pytest.raises(ValueError):
        client.delete_price("mock-gpt", revision=0)

    fake_api.route("DELETE", "/api/v1/usage/prices/mock-gpt", status=204)
    client.delete_price("mock-gpt", revision=2)
    assert fake_api.requests[-1].headers["if-match"] == "2"


def test_surfaces_and_tool_connections(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/llm/surfaces", json=[SURFACE_SAMPLE])
    surfaces = client.list_model_surfaces()
    assert len(surfaces) == 1
    assert surfaces[0].id == "local-models"
    assert surfaces[0].protocol == "openai-chat"
    assert surfaces[0].provider_id == "openai-compatible"
    assert surfaces[0].models == ["mock-gpt"]

    fake_api.route("GET", "/api/v1/llm/surfaces/local-models", json=SURFACE_SAMPLE)
    surf = client.get_model_surface("local-models")
    assert surf.protocol == "openai-chat"

    fake_api.route("GET", "/api/v1/mcp/connections", json=[TOOL_CONN_SAMPLE])
    conns = client.list_tool_connections()
    assert len(conns) == 1
    assert conns[0].id == "local-tools"
    assert conns[0].resource_url == "http://127.0.0.1:35367"
    assert conns[0].auth_type == "none"
    assert conns[0].allowed_tools == ["echo", "add", "reset_counter"]

    fake_api.route("GET", "/api/v1/mcp/connections/local-tools", json=TOOL_CONN_SAMPLE)
    conn = client.get_tool_connection("local-tools")
    assert conn.resource_url == "http://127.0.0.1:35367"


def test_overview_endpoints(fake_api: FakeAPI, client: Client) -> None:
    with pytest.raises(ValueError):
        client.list_overview_subjects("admin")
    assert len(fake_api.requests) == 0

    fake_api.route(
        "GET",
        "/api/v1/overview/subjects",
        json={
            "kind": "user",
            "since": "2026-11-09T10:23:13Z",
            "subjects": [
                {
                    "kind": "user",
                    "id": "principal-1",
                    "display_name": "dev@localhost",
                    "access_points": ["local"],
                }
            ],
            "usage_available": True,
            "usage_truncated": False,
        },
    )
    spage = client.list_overview_subjects("user", limit=5)
    assert fake_api.requests[-1].query == {"kind": "user", "limit": "5"}
    assert len(spage.subjects) == 1
    assert spage.subjects[0].id == "principal-1"

    fake_api.route(
        "GET",
        "/api/v1/overview/subjects/user/principal-1",
        json={"kind": "user", "id": "principal-1", "usage": {"requests": 3, "records": 3}},
    )
    sdetail = client.get_overview_subject("user", "principal-1")
    assert fake_api.requests[-1].path == "/api/v1/overview/subjects/user/principal-1"
    assert sdetail.usage is not None
    assert sdetail.usage.requests == 3

    spiffe_id = "spiffe://example.org/ns/default/sa/agent-1"
    fake_api.route(
        "GET",
        "/api/v1/overview/subjects/agent",
        json={"kind": "agent", "id": spiffe_id},
    )
    adetail = client.get_overview_subject("agent", spiffe_id, since="2026-11-09T00:00:00Z")
    assert fake_api.requests[-1].path == "/api/v1/overview/subjects/agent"
    assert fake_api.requests[-1].query == {"id": spiffe_id, "since": "2026-11-09T00:00:00Z"}
    assert adetail.id == spiffe_id

    fake_api.route(
        "GET",
        "/api/v1/overview/access-points",
        json={
            "access_points": [
                {
                    "id": "local",
                    "display_name": "Local development",
                    "caller_kinds": ["user"],
                    "counts": {"models": 4, "tools": 1, "skills": 0},
                    "quota_coverage": {"state": "not_required", "detail": "none"},
                }
            ]
        },
    )
    ap_page = client.list_overview_access_points(limit=1, cursor="c1")
    assert fake_api.requests[-1].query == {"limit": "1", "cursor": "c1"}
    assert ap_page.access_points[0].counts["models"] == 4
    assert ap_page.access_points[0].quota_coverage_state == "not_required"

    fake_api.route(
        "GET",
        "/api/v1/overview/access-points/local",
        json={
            "access_point": {"id": "local", "display_name": "Local development"},
            "revision": 1,
            "since": "2026-11-09T10:23:13Z",
            "usage": {"available": True, "truncated": False, "total": {"requests": 1, "records": 1}},
        },
    )
    ap_detail = client.get_overview_access_point("local", since="2026-11-09T10:23:13Z")
    assert fake_api.requests[-1].query == {"since": "2026-11-09T10:23:13Z"}
    assert ap_detail.id == "local"
    assert ap_detail.usage_available is True
    assert ap_detail.usage is not None
    assert ap_detail.usage.requests == 1


def test_decode_error_on_wrong_wire_type(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/usage/records/ul_bad",
        json={"id": "ul_bad", "usage": "not-an-object"},
    )
    with pytest.raises(AiplexError) as exc_info:
        client.get_usage_record("ul_bad")
    assert exc_info.value.code == "decode_error"
    assert exc_info.value.status_code is None

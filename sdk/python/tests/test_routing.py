from __future__ import annotations

from decimal import Decimal

from aiplex import AccessPointModel, ResponseCacheConfig, RoutingBackend, UsageRecord, UsageSummaryGroup


def test_access_point_model_backends_round_trip() -> None:
    wire = {
        "name": "gpt",
        "backends": [{"surface_ref": "openai", "native_model": "gpt-4o-mini"}, {"surface_ref": "azure", "native_model": "gpt-4o-mini", "weight": 0, "priority": 1}],
        "routing": {"strategy": "priority", "retry": {}},
        "cache": {"ttl": "10m"},
    }
    m = AccessPointModel.from_wire(wire)
    assert m.surface_ref is None
    assert m.backends[1] == RoutingBackend(surface_ref="azure", native_model="gpt-4o-mini", weight=0, priority=1)
    assert m.cache == ResponseCacheConfig(ttl="10m")
    assert m.to_wire() == wire


def test_routed_record_and_cache_hit() -> None:
    r = UsageRecord.from_wire(
        {
            "id": "ul_1",
            "source": "managed",
            "usage": {"input_tokens": 0, "output_tokens": 0},
            "cost": {"basis": "cached", "amount": "0", "scale": 0, "amount_decimal": "0"},
            "attempts": [
                {"surface_ref": "a", "native_model": "m", "outcome": "unavailable", "upstream_status": 503, "retry_after_ms": 1000, "duration_ms": 4, "breaker_state": "closed", "reason": "priority"},
                {"surface_ref": "b", "native_model": "m", "outcome": "cache_hit", "duration_ms": 1},
            ],
            "backend": {"surface_ref": "b", "native_model": "m"},
            "routing_strategy": "priority",
            "cache": {"status": "hit", "scope": "principal"},
            "cached_response_usage": {"input_tokens": 12, "output_tokens": 30},
        }
    )
    assert [a.outcome for a in r.attempts] == ["unavailable", "cache_hit"]
    assert r.attempts[0].retry_after_ms == 1000
    assert r.backend is not None and r.backend.surface_ref == "b"
    assert r.cache is not None and r.cache.status == "hit"
    assert r.cost is not None and r.cost.basis == "cached" and r.cost.amount == Decimal("0")
    assert r.cached_response_usage is not None and r.cached_response_usage.input_tokens == 12
    assert r.usage is not None and r.usage.input_tokens == 0


def test_unrouted_record_has_no_routing_fields() -> None:
    r = UsageRecord.from_wire({"id": "ul_2", "source": "managed", "cost": {"basis": "unknown"}})
    assert r.attempts == [] and r.backend is None and r.cache is None and r.cached_response_usage is None


def test_summary_group_counts_cached_records() -> None:
    g = UsageSummaryGroup.from_wire({"key": {}, "records": 2, "cached_response_records": 1, "unknown_cost_records": 1})
    assert g.cached_response_records == 1

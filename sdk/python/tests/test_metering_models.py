from __future__ import annotations

from decimal import Decimal

from aiplex import (
    Cost,
    ModelPrice,
    ModelPriceTier,
    UsageCounters,
    UsageFilter,
    UsageRecord,
)


def test_cost_exact_price_id_and_backfill_stay_strings_not_floats() -> None:
    c = Cost.from_wire(
        {
            "basis": "estimated",
            "currency": "USD",
            "amount": "10",
            "scale": 6,
            "amount_decimal": "0.000010",
            "exact": "37/4",
            "price_id": "p1",
            "backfilled_at": "2026-11-10T00:00:00Z",
        }
    )
    assert c.exact == "37/4"
    assert c.price_id == "p1"
    assert c.backfilled_at == "2026-11-10T00:00:00Z"
    assert isinstance(c.amount, Decimal) and not isinstance(c.amount, float)
    # An unknown cost has none of them: unknown stays None, never zero.
    u = Cost.from_wire({"basis": "unknown"})
    assert (u.exact, u.price_id, u.backfilled_at, u.amount, u.amount_minor) == (None,) * 5
    r = Cost.from_wire({"basis": "reported", "currency": "USD", "amount": "12346", "scale": 6})
    assert r.basis == "reported" and r.exact is None and r.amount == Decimal("0.012346")


def test_usage_record_service_tier_server_tools_and_new_counters() -> None:
    rec = UsageRecord.from_wire(
        {
            "id": "ul_1",
            "service_tier": "flex",
            "server_tools": {"web_search": 2, "google_search": 1},
            "usage": {"input_tokens": 5, "cache_write_1h_tokens": 4, "tool_use_prompt_tokens": 9},
            "cost": {"basis": "unknown"},
        }
    )
    assert rec.service_tier == "flex"
    assert rec.server_tools == {"web_search": 2, "google_search": 1}
    assert rec.usage is not None
    assert rec.usage.cache_write_1h_tokens == 4 and rec.usage.tool_use_prompt_tokens == 9
    assert rec.usage.output_tokens is None  # not reported: None, not 0
    bare = UsageRecord.from_wire({"id": "ul_2", "usage": {"input_tokens": 1}})
    assert bare.service_tier is None and bare.server_tools == {}
    assert bare.usage == UsageCounters.from_wire({"input_tokens": 1})
    assert bare.usage.cache_write_1h_tokens is None


def test_price_tiers_and_service_tier_round_trip_exactly() -> None:
    wire = {
        "id": "p",
        "model_id": "m",
        "currency": "USD",
        "scale": 6,
        "service_tier": "priority",
        "meters": {"input_tokens": {"numerator": 1, "denominator": 4}},
        "tiers": [{"above_input_tokens": 100000, "meters": {"input_tokens": {"numerator": 1, "denominator": 2}}}],
    }
    price = ModelPrice.from_wire(wire)
    assert price.service_tier == "priority"
    assert price.tiers[0].above_input_tokens == 100000
    assert price.tiers[0].meters["input_tokens"].denominator == 2
    out = price.to_wire()
    assert out["service_tier"] == "priority"
    assert out["tiers"] == wire["tiers"]
    assert isinstance(price.tiers[0], ModelPriceTier)
    plain = ModelPrice.from_wire({"id": "q", "meters": {}}).to_wire()
    assert "tiers" not in plain and "service_tier" not in plain


def test_usage_filter_cost_basis_query() -> None:
    assert UsageFilter(cost_basis="reported").to_query() == {"cost_basis": "reported"}
    assert "cost_basis" not in UsageFilter().to_query()

from __future__ import annotations

from decimal import Decimal

from aiplex import (
    AccessPointModel,
    AccessPointSpec,
    Cost,
    CostTotal,
    ModelPrice,
    ModelPriceRecord,
    UsageSummary,
)


def test_cost_and_cost_total_decimal_parsing() -> None:
    c1 = Cost.from_wire(
        {
            "basis": "estimated",
            "currency": "USD",
            "amount": "6",
            "scale": 6,
            "amount_decimal": "0.000006",
            "extra_field": "kept",
        }
    )
    assert isinstance(c1.amount, Decimal)
    assert not isinstance(c1.amount, float)
    assert c1.amount == Decimal("0.000006")
    assert c1.amount_minor == 6
    assert c1.scale == 6
    assert c1.currency == "USD"
    assert c1.basis == "estimated"
    assert c1.raw["extra_field"] == "kept"

    c2 = Cost.from_wire(
        {
            "basis": "settled",
            "currency": "USD",
            "amount": "1250",
            "scale": 4,
        }
    )
    assert isinstance(c2.amount, Decimal)
    assert c2.amount == Decimal("0.1250")
    assert c2.amount_minor == 1250
    assert c2.scale == 4

    c_unknown = Cost.from_wire({"basis": "unknown"})
    assert c_unknown.basis == "unknown"
    assert c_unknown.amount is None
    assert c_unknown.amount_minor is None
    assert c_unknown.scale is None
    assert c_unknown.currency is None

    ct = CostTotal.from_wire(
        {
            "currency": "USD",
            "basis": "estimated",
            "amount": "20",
            "scale": 6,
            "records": 3,
        }
    )
    assert isinstance(ct.amount, Decimal)
    assert ct.amount == Decimal("0.000020")
    assert ct.amount_minor == 20
    assert ct.records == 3


def test_usage_summary_tokens_and_tokens_unknown() -> None:
    s = UsageSummary.from_wire(
        {
            "group_by": ["model_id", "batch"],
            "groups": [
                {
                    "key": {"model_id": "mock-gpt", "batch": False},
                    "records": 2,
                    "requests": 2,
                    "errors": 0,
                    "denied": 0,
                    "unknown_status": 0,
                    "tokens": {"input_tokens": 5, "output_tokens": 29},
                    "tokens_unknown": {"cache_read_tokens": 2, "reasoning_tokens": 2},
                    "unknown_usage_records": 0,
                    "cost": [
                        {
                            "currency": "USD",
                            "basis": "estimated",
                            "amount": "20",
                            "scale": 6,
                            "records": 2,
                            "amount_decimal": "0.000020",
                        }
                    ],
                    "unknown_cost_records": 0,
                }
            ],
            "records_scanned": 2,
            "truncated": False,
        }
    )
    assert len(s.groups) == 1
    grp = s.groups[0]
    assert grp.key == {"model_id": "mock-gpt", "batch": "false"}
    assert grp.tokens == {"input_tokens": 5, "output_tokens": 29}
    assert grp.tokens.get("total_tokens") is None
    assert grp.tokens.get("cache_read_tokens") is None
    assert grp.tokens_unknown == {"cache_read_tokens": 2, "reasoning_tokens": 2}
    assert grp.tokens_unknown.get("input_tokens") is None
    assert isinstance(grp.cost[0].amount, Decimal)


def test_access_point_spec_to_wire_omits_none_fields() -> None:
    spec = AccessPointSpec(
        id="ap-test",
        caller_kinds=["user"],
        models=[AccessPointModel(name="m1", surface_ref="s1")],
    )
    wire = spec.to_wire()
    assert wire == {
        "id": "ap-test",
        "caller_kinds": ["user"],
        "models": [{"name": "m1", "surface_ref": "s1"}],
    }
    assert "display_name" not in wire
    assert "subjects" not in wire
    assert "qos" not in wire
    assert "tools" not in wire
    assert "skills" not in wire


def test_model_price_round_trip() -> None:
    raw_record = {
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
    rec = ModelPriceRecord.from_wire(raw_record)
    assert rec.id == "mock-gpt"
    assert rec.revision == 1
    assert rec.price.to_wire() == raw_record["spec"]
    round_tripped = ModelPrice.from_wire(rec.price.to_wire())
    assert round_tripped.to_wire() == raw_record["spec"]

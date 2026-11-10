from __future__ import annotations

from decimal import Decimal

import pytest

from aiplex import Client, UsageFilter, UsageRecord, UsageSummary
from conftest import FakeAPI

REPORTED = {
    "id": "ul_r",
    "tenant_id": "t",
    "started_at": "2026-11-10T12:00:00Z",
    "finished_at": "2026-11-10T12:00:01Z",
    "latency_ms": 900,
    "plane": "llmplex",
    "model_id": "claude-sonnet-4-5",
    "status": "ok",
    "source": "reported",
    "harness": "claude-code",
    "harness_version": "2.1.322",
    "session_id": "s1",
    "trace_id": "0af7651916cd43dd8448eb211c80319c",
    "span_id": "b7ad6b7169203331",
    "tags": {"experiment": "arm-a"},
    "reported_attributes": {"request_id": "req_1"},
    "hooks": {"decision": "allow", "results": []},
    "requests": 1,
    "usage": {"input_tokens": 130, "output_tokens": None, "cache_read_tokens": 30,
              "cache_write_tokens": 0, "reasoning_tokens": None, "total_tokens": None},
    "cost": {"basis": "reported", "currency": "USD", "amount": "4200", "scale": 6, "amount_decimal": "0.004200"},
    "feedback": {"id": "fb_1", "record_id": "ul_r", "by": "alice", "created_at": "2026-11-10T12:05:00Z", "score": -0.5, "label": "wrong"},
}


def test_reported_record_fields() -> None:
    r = UsageRecord.from_wire(REPORTED)
    assert r.source == "reported" and r.harness == "claude-code" and r.harness_version == "2.1.322"
    assert r.session_id == "s1" and r.tags == {"experiment": "arm-a"}
    assert r.reported_attributes == {"request_id": "req_1"}
    assert r.hooks == {"decision": "allow", "results": []}
    assert r.cost is not None and r.cost.basis == "reported" and r.cost.amount == Decimal("0.004200")
    # Unknown stays None; a reported zero stays 0.
    assert r.usage is not None and r.usage.output_tokens is None and r.usage.cache_write_tokens == 0
    assert r.feedback is not None and r.feedback.score == -0.5 and r.feedback.label == "wrong"


def test_absent_fields_are_none_or_empty_never_zero() -> None:
    r = UsageRecord.from_wire({"id": "ul_m", "source": "managed", "cost": {"basis": "unknown"}})
    assert r.harness is None and r.feedback is None and r.hooks is None
    assert r.tags == {} and r.reported_attributes == {}
    assert r.cost is not None and r.cost.amount is None


def test_summary_source_split_and_feedback() -> None:
    s = UsageSummary.from_wire(
        {
            "group_by": ["model_id", "source"],
            "source_split": True,
            "excluded_correlated_reported": 2,
            "notes": ["Managed and reported records are separate groups."],
            "records_scanned": 4,
            "truncated": False,
            "groups": [
                {"key": {"source": "managed"}, "records": 3, "feedback": {"records": 2, "without_feedback": 1, "positive": 1, "negative": 0, "neutral": 1, "average_score": 0.5}},
                {"key": {"source": "reported"}, "records": 1, "feedback": {"records": 0, "without_feedback": 1, "average_score": None}},
                {"key": {"source": "x"}, "records": 1},
            ],
        }
    )
    assert s.source_split is True and s.excluded_correlated_reported == 2 and len(s.notes) == 1
    assert s.groups[0].feedback is not None and s.groups[0].feedback.average_score == 0.5
    assert s.groups[1].feedback is not None and s.groups[1].feedback.average_score is None
    assert s.groups[1].feedback.without_feedback == 1
    assert s.groups[2].feedback is None
    empty = UsageSummary.from_wire({"groups": []})
    assert empty.source_split is None and empty.excluded_correlated_reported is None and empty.notes == []


def test_filter_query_has_harness_hook_decision_and_tags() -> None:
    q = UsageFilter(harness="codex", hook_decision="deny", tags={"experiment": "arm-a"}).to_query()
    assert q == {"harness": "codex", "hook_decision": "deny", "tag.experiment": "arm-a"}


def test_summary_request_parameters(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/usage/summary", json={"groups": []})
    client.usage_summary(group_by=["tag:experiment", "harness", "hook_decision"], merge_sources=True, feedback=True)
    req = fake_api.requests[-1]
    assert req.query["group_by"] == "tag:experiment,harness,hook_decision"
    assert req.query["merge_sources"] == "true" and req.query["feedback"] == "true"
    with pytest.raises(ValueError):
        client.usage_summary(group_by=["tag:Bad Key"])


def test_add_usage_feedback(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "POST", "/api/v1/usage/records/ul_r/feedback",
        json={"id": "fb_2", "record_id": "ul_r", "by": "alice", "created_at": "2026-11-10T12:06:00Z", "score": 1},
        status=201,
    )
    saved = client.add_usage_feedback("ul_r", 1, label="helpful")
    assert saved.id == "fb_2" and saved.score == 1.0 and saved.label is None
    assert fake_api.requests[-1].body == {"score": 1, "label": "helpful"}
    for bad in (2, -1.5, True):
        with pytest.raises(ValueError):
            client.add_usage_feedback("ul_r", bad)  # type: ignore[arg-type]

from __future__ import annotations

import pytest

from aiplex import BudgetStatus, Client, UsageFilter, UsageRecord
from conftest import FakeAPI

MEMBER_STATUS = {
    "id": "team",
    "display_name": "Platform",
    "unit": "cost",
    "currency": "USD",
    "limit": "250.000000",
    "spent": "12.500000",
    "reserved": "1.000000",
    "remaining": "236.500000",
    "window": {"start": "2026-11-01T00:00:00Z", "end": "2026-12-01T00:00:00Z"},
}


def test_budget_status_member_and_operator_views(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/budgets/team/status", json=MEMBER_STATUS)
    st = client.get_budget_status("team")
    assert isinstance(st, BudgetStatus)
    assert (st.spent, st.remaining, st.window_end) == ("12.500000", "236.500000", "2026-12-01T00:00:00Z")
    # Operator-only fields are absent for a member: unknown, never zero.
    assert st.unknown_cost_requests is None and st.children == []
    fake_api.route(
        "GET",
        "/api/v1/budgets/org/status",
        json={**MEMBER_STATUS, "unknown_cost_requests": 3, "children": [{"id": "c"}]},
    )
    op = client.get_budget_status("org")
    assert op.unknown_cost_requests == 3 and op.children == [{"id": "c"}]
    with pytest.raises(ValueError):
        client.get_budget_status("")


def test_budget_filter_and_record_field() -> None:
    assert UsageFilter(budget_id="team").to_query() == {"budget_id": "team"}
    rec = UsageRecord.from_wire({"id": "ul_1", "budget_ids": ["org", "team"]})
    assert rec.budget_ids == ["org", "team"]
    assert UsageRecord.from_wire({"id": "ul_2"}).budget_ids == []

from __future__ import annotations

import itertools
import os

import pytest

from aiplex import Client

pytestmark = pytest.mark.skipif(
    not (os.environ.get("AIPLEX_URL") and os.environ.get("AIPLEX_TOKEN")),
    reason="AIPLEX_URL and AIPLEX_TOKEN environment variables are required for integration tests",
)


def test_live_stack_read_operations() -> None:
    with Client() as client:
        who = client.whoami()
        assert who.identity is not None
        assert who.identity.principal_id

        aps = client.list_access_points()
        assert isinstance(aps, list)
        local_aps = [ap for ap in aps if ap.id == "local"]
        if local_aps:
            assert local_aps[0].revision >= 1

        local_ap = client.get_access_point("local")
        assert local_ap.id == "local"
        assert local_ap.revision >= 1

        conn = client.connect_access_point("local", client="claude-code")
        assert "openai" in conn.base_urls

        rec_page = client.list_usage_records(limit=5)
        for rec in rec_page.records:
            assert rec.id is not None
            assert rec.id.startswith("ul_")

        summary = client.usage_summary(group_by=["model_id"])
        assert isinstance(summary.records_scanned, int)

        prices = client.list_prices()
        assert isinstance(prices, list)

        surfaces = client.list_model_surfaces()
        assert isinstance(surfaces, list)

        tool_conns = client.list_tool_connections()
        assert isinstance(tool_conns, list)

        ov_ap = client.get_overview_access_point("local")
        assert ov_ap.id == "local"

        iter_items = list(itertools.islice(client.iter_usage_records(limit=2), 4))
        for item in iter_items:
            assert item.id is not None
            assert item.id.startswith("ul_")

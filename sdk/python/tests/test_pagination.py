from __future__ import annotations

import pytest

from aiplex import AiplexError, Client, UsageFilter
from conftest import FakeAPI


def test_iter_usage_records_three_pages(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [{"id": "ul_1"}], "next_cursor": "cur_1"},
    )
    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [{"id": "ul_2"}], "next_cursor": "cur_2"},
    )
    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [{"id": "ul_3"}]},
    )

    items = list(client.iter_usage_records(UsageFilter(plane="llmplex"), limit=1))
    assert [r.id for r in items] == ["ul_1", "ul_2", "ul_3"]
    assert len(fake_api.requests) == 3
    assert fake_api.requests[0].query == {"plane": "llmplex", "limit": "1"}
    assert fake_api.requests[1].query == {"plane": "llmplex", "limit": "1", "cursor": "cur_1"}
    assert fake_api.requests[2].query == {"plane": "llmplex", "limit": "1", "cursor": "cur_2"}


def test_iter_usage_records_stalled_cursor(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [{"id": "ul_1"}], "next_cursor": "same_cur"},
    )
    fake_api.route(
        "GET",
        "/api/v1/usage/records",
        json={"records": [{"id": "ul_2"}], "next_cursor": "same_cur"},
    )

    it = client.iter_usage_records(limit=1)
    assert next(it).id == "ul_1"
    assert next(it).id == "ul_2"
    with pytest.raises(AiplexError) as exc_info:
        next(it)
    assert exc_info.value.code == "pagination_stalled"
    assert exc_info.value.status_code is None


def test_iter_overview_subjects_and_access_points(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route(
        "GET",
        "/api/v1/overview/subjects",
        json={"kind": "user", "subjects": [{"id": "u1"}], "next_cursor": "sc1"},
    )
    fake_api.route(
        "GET",
        "/api/v1/overview/subjects",
        json={"kind": "user", "subjects": [{"id": "u2"}]},
    )

    subjects = list(client.iter_overview_subjects("user", limit=1))
    assert [s.id for s in subjects] == ["u1", "u2"]
    assert fake_api.requests[0].query == {"kind": "user", "limit": "1"}
    assert fake_api.requests[1].query == {"kind": "user", "limit": "1", "cursor": "sc1"}

    fake_api.route(
        "GET",
        "/api/v1/overview/access-points",
        json={"access_points": [{"id": "ap1"}], "next_cursor": "apc1"},
    )
    fake_api.route(
        "GET",
        "/api/v1/overview/access-points",
        json={"access_points": [{"id": "ap2"}]},
    )

    aps = list(client.iter_overview_access_points(limit=1))
    assert [a.id for a in aps] == ["ap1", "ap2"]
    assert fake_api.requests[2].query == {"limit": "1"}
    assert fake_api.requests[3].query == {"limit": "1", "cursor": "apc1"}


def test_iter_empty_first_page(fake_api: FakeAPI, client: Client) -> None:
    fake_api.route("GET", "/api/v1/usage/records", json={"records": []})
    assert list(client.iter_usage_records()) == []
    assert len(fake_api.requests) == 1

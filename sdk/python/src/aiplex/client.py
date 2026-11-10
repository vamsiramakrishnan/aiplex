"""Synchronous HTTP client for the AIPlex control-plane API."""

from __future__ import annotations

import json
import os
from collections.abc import Iterator, Sequence
from typing import Any
from urllib.parse import quote

import httpx

from .errors import AiplexError, redact_value
from .models import (
    AccessPointRecord,
    AccessPointSpec,
    ConnectConfig,
    ModelPrice,
    ModelPriceRecord,
    ModelSurface,
    OverviewAccessPointDetail,
    OverviewAccessPointPage,
    OverviewAccessPointSummary,
    OverviewSubject,
    OverviewSubjectDetail,
    OverviewSubjectPage,
    ToolConnection,
    UsageFilter,
    UsageRecord,
    UsageRecordPage,
    UsageSummary,
    Whoami,
)

_USER_AGENT = "aiplex-sdk-python/0.1.0"

_VALID_OVERVIEW_KINDS = frozenset({"agent", "user", "client"})

_VALID_GROUP_BY = frozenset(
    {
        "plane",
        "provider",
        "model_id",
        "tool_name",
        "actor_id",
        "principal_id",
        "client_id",
        "access_point_id",
        "caller_kind",
        "status",
        "source",
        "error_class",
        "usage_bound",
        "batch",
    }
)

_VALID_BUCKETS = frozenset({"hour", "day"})


def _quote_segment(value: str, name: str = "id") -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{name} must be a non-empty string")
    return quote(value, safe="")


def _validate_positive_revision(revision: int) -> None:
    if isinstance(revision, bool) or not isinstance(revision, int) or revision <= 0:
        raise ValueError("revision must be a positive integer")


def _validate_non_negative_revision(revision: int) -> None:
    if isinstance(revision, bool) or not isinstance(revision, int) or revision < 0:
        raise ValueError("revision must be a non-negative integer")


def _validate_usage_limit(limit: int | None) -> None:
    if limit is None:
        return
    if isinstance(limit, bool) or not isinstance(limit, int) or limit < 1 or limit > 1000:
        raise ValueError("limit must be between 1 and 1000")


def _validate_overview_limit(limit: int | None) -> None:
    if limit is None:
        return
    if isinstance(limit, bool) or not isinstance(limit, int) or limit < 1:
        raise ValueError("limit must be a positive integer")


def _validate_overview_kind(kind: str) -> None:
    if not isinstance(kind, str) or kind not in _VALID_OVERVIEW_KINDS:
        raise ValueError(f"invalid overview subject kind: {kind!r}")


class Client:
    """HTTP client for the AIPlex control-plane API."""

    def __init__(
        self,
        base_url: str | None = None,
        token: str | None = None,
        *,
        timeout: float = 30.0,
        http_client: httpx.Client | None = None,
    ) -> None:
        resolved_url = base_url if base_url is not None else os.environ.get("AIPLEX_URL")
        if not resolved_url or not resolved_url.strip():
            raise ValueError("base_url is required (pass base_url or set AIPLEX_URL)")
        resolved_token = token if token is not None else os.environ.get("AIPLEX_TOKEN")
        if not resolved_token or not resolved_token.strip():
            raise ValueError("token is required (pass token or set AIPLEX_TOKEN)")

        self._base_url: str = resolved_url.rstrip("/")
        self._token: str = resolved_token
        self._timeout: float = timeout
        if http_client is not None:
            self._client: httpx.Client = http_client
            self._owns_client: bool = False
        else:
            self._client = httpx.Client(timeout=timeout)
            self._owns_client = True

    def close(self) -> None:
        """Close the underlying HTTP client if owned by this instance."""
        if self._owns_client:
            self._client.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    def __repr__(self) -> str:
        return f"Client(base_url={self._base_url!r}, token=<redacted>)"

    def __str__(self) -> str:
        return self.__repr__()

    def _request(
        self,
        method: str,
        path: str,
        *,
        params: dict[str, str] | None = None,
        json_body: Any = None,
        headers: dict[str, str] | None = None,
        expect_no_content: bool = False,
    ) -> Any:
        url = f"{self._base_url}{path}"
        req_headers: dict[str, str] = {
            "Authorization": f"Bearer {self._token}",
            "Accept": "application/json",
            "User-Agent": _USER_AGENT,
        }
        content: bytes | None = None
        if json_body is not None:
            req_headers["Content-Type"] = "application/json"
            content = json.dumps(json_body).encode("utf-8")
        if headers:
            req_headers.update(headers)

        clean_params: dict[str, str] | None = None
        if params:
            clean_params = {k: v for k, v in params.items() if v is not None and v != ""}

        try:
            response = self._client.request(
                method=method,
                url=url,
                params=clean_params,
                content=content,
                headers=req_headers,
                timeout=self._timeout,
            )
        except Exception as exc:
            cls_name = type(exc).__name__
            raise AiplexError(
                f"request failed: {cls_name}",
                code="transport_error",
                status_code=None,
            ) from None

        status = response.status_code
        if status < 200 or status >= 300:
            raise self._parse_error(response)

        if expect_no_content or status == 204:
            return None

        try:
            return response.json()
        except Exception:
            raise AiplexError(
                "invalid JSON in response body",
                code="decode_error",
                status_code=None,
            ) from None

    def _parse_error(self, response: httpx.Response) -> AiplexError:
        status = response.status_code
        reason = response.reason_phrase or f"HTTP {status}"
        try:
            body = response.json()
        except Exception:
            body = None

        if isinstance(body, dict):
            raw_code = body.get("code")
            if isinstance(raw_code, str) and raw_code != "":
                raw_msg = body.get("message")
                msg = raw_msg if isinstance(raw_msg, str) else reason
                msg = redact_value(msg, self._token)

                raw_details = body.get("details")
                details: dict[str, Any] | None = None
                if isinstance(raw_details, dict):
                    details = redact_value(raw_details, self._token)

                raw_req_id = body.get("request_id")
                request_id = (
                    redact_value(raw_req_id, self._token)
                    if isinstance(raw_req_id, str)
                    else None
                )

                raw_trace_id = body.get("trace_id")
                trace_id = (
                    redact_value(raw_trace_id, self._token)
                    if isinstance(raw_trace_id, str)
                    else None
                )

                return AiplexError(
                    msg,
                    code=redact_value(raw_code, self._token),
                    status_code=status,
                    details=details,
                    request_id=request_id,
                    trace_id=trace_id,
                )

        return AiplexError(
            redact_value(reason, self._token),
            code="unknown",
            status_code=status,
        )

    def whoami(self) -> Whoami:
        """Return the authenticated caller's identity, roles, and scopes."""
        data = self._request("GET", "/api/v1/iam/whoami")
        return Whoami.from_wire(data)

    def list_access_points(self) -> list[AccessPointRecord]:
        """List all access points in the tenant."""
        data = self._request("GET", "/api/v1/access-points/")
        if not isinstance(data, list):
            raise AiplexError(
                "access points list must be a JSON array",
                code="decode_error",
                status_code=None,
            )
        return [AccessPointRecord.from_wire(item) for item in data]

    def get_access_point(self, id: str) -> AccessPointRecord:
        """Fetch a single access point by ID."""
        quoted = _quote_segment(id, "id")
        data = self._request("GET", f"/api/v1/access-points/{quoted}")
        return AccessPointRecord.from_wire(data)

    def put_access_point(self, spec: AccessPointSpec, revision: int = 0) -> AccessPointRecord:
        """Create (revision 0) or replace an access point specification."""
        if not isinstance(spec, AccessPointSpec):
            raise ValueError("spec must be an AccessPointSpec")
        _validate_non_negative_revision(revision)
        quoted = _quote_segment(spec.id, "spec.id")
        data = self._request(
            "PUT",
            f"/api/v1/access-points/{quoted}",
            json_body=spec.to_wire(),
            headers={"If-Match": str(revision)},
        )
        return AccessPointRecord.from_wire(data)

    def delete_access_point(self, id: str, revision: int) -> None:
        """Delete an access point at the given positive revision."""
        quoted = _quote_segment(id, "id")
        _validate_positive_revision(revision)
        self._request(
            "DELETE",
            f"/api/v1/access-points/{quoted}",
            headers={"If-Match": str(revision)},
            expect_no_content=True,
        )

    def connect_access_point(self, id: str, client: str | None = None) -> ConnectConfig:
        """Return client connection configuration for an access point."""
        quoted = _quote_segment(id, "id")
        params: dict[str, str] = {}
        if client is not None and client != "":
            params["client"] = client
        data = self._request(
            "GET",
            f"/api/v1/overview/access-points/{quoted}/connect",
            params=params,
        )
        return ConnectConfig.from_wire(data)

    def list_overview_access_points(
        self,
        *,
        limit: int | None = None,
        cursor: str | None = None,
    ) -> OverviewAccessPointPage:
        """Return a page of access point overview summaries."""
        _validate_overview_limit(limit)
        params: dict[str, str] = {}
        if limit is not None:
            params["limit"] = str(limit)
        if cursor is not None and cursor != "":
            params["cursor"] = cursor
        data = self._request("GET", "/api/v1/overview/access-points", params=params)
        return OverviewAccessPointPage.from_wire(data)

    def iter_overview_access_points(
        self,
        *,
        limit: int | None = None,
    ) -> Iterator[OverviewAccessPointSummary]:
        """Lazily iterate over all access point overview summaries across pages."""
        _validate_overview_limit(limit)
        cursor: str | None = None
        while True:
            page = self.list_overview_access_points(limit=limit, cursor=cursor)
            yield from page.access_points
            if not page.next_cursor:
                break
            if page.next_cursor == cursor:
                raise AiplexError(
                    "server returned the same cursor twice",
                    code="pagination_stalled",
                    status_code=None,
                )
            cursor = page.next_cursor

    def get_overview_access_point(
        self,
        id: str,
        *,
        since: str | None = None,
    ) -> OverviewAccessPointDetail:
        """Return the detailed overview for a single access point."""
        quoted = _quote_segment(id, "id")
        params: dict[str, str] = {}
        if since is not None and since != "":
            params["since"] = since
        data = self._request(
            "GET",
            f"/api/v1/overview/access-points/{quoted}",
            params=params,
        )
        return OverviewAccessPointDetail.from_wire(data)

    def list_overview_subjects(
        self,
        kind: str,
        *,
        since: str | None = None,
        limit: int | None = None,
        cursor: str | None = None,
    ) -> OverviewSubjectPage:
        """Return a page of overview subjects for a caller kind."""
        _validate_overview_kind(kind)
        _validate_overview_limit(limit)
        params: dict[str, str] = {"kind": kind}
        if since is not None and since != "":
            params["since"] = since
        if limit is not None:
            params["limit"] = str(limit)
        if cursor is not None and cursor != "":
            params["cursor"] = cursor
        data = self._request("GET", "/api/v1/overview/subjects", params=params)
        return OverviewSubjectPage.from_wire(data)

    def iter_overview_subjects(
        self,
        kind: str,
        *,
        since: str | None = None,
        limit: int | None = None,
    ) -> Iterator[OverviewSubject]:
        """Lazily iterate over all overview subjects of the given kind across pages."""
        _validate_overview_kind(kind)
        _validate_overview_limit(limit)
        cursor: str | None = None
        while True:
            page = self.list_overview_subjects(
                kind,
                since=since,
                limit=limit,
                cursor=cursor,
            )
            yield from page.subjects
            if not page.next_cursor:
                break
            if page.next_cursor == cursor:
                raise AiplexError(
                    "server returned the same cursor twice",
                    code="pagination_stalled",
                    status_code=None,
                )
            cursor = page.next_cursor

    def get_overview_subject(
        self,
        kind: str,
        id: str,
        *,
        since: str | None = None,
    ) -> OverviewSubjectDetail:
        """Return the detailed overview for a single subject."""
        _validate_overview_kind(kind)
        if not isinstance(id, str) or not id:
            raise ValueError("id must be a non-empty string")
        quoted_kind = _quote_segment(kind, "kind")
        params: dict[str, str] = {}
        if since is not None and since != "":
            params["since"] = since
        if "/" in id:
            params["id"] = id
            path = f"/api/v1/overview/subjects/{quoted_kind}"
        else:
            quoted_id = _quote_segment(id, "id")
            path = f"/api/v1/overview/subjects/{quoted_kind}/{quoted_id}"
        data = self._request("GET", path, params=params)
        return OverviewSubjectDetail.from_wire(data)

    def list_usage_records(
        self,
        filter: UsageFilter | None = None,
        *,
        limit: int | None = None,
        cursor: str | None = None,
    ) -> UsageRecordPage:
        """Return a page of usage ledger records matching the filter."""
        _validate_usage_limit(limit)
        params = filter.to_query() if filter is not None else {}
        if limit is not None:
            params["limit"] = str(limit)
        if cursor is not None and cursor != "":
            params["cursor"] = cursor
        data = self._request("GET", "/api/v1/usage/records", params=params)
        return UsageRecordPage.from_wire(data)

    def iter_usage_records(
        self,
        filter: UsageFilter | None = None,
        *,
        limit: int | None = None,
    ) -> Iterator[UsageRecord]:
        """Lazily iterate over all usage records matching the filter across pages."""
        _validate_usage_limit(limit)
        cursor: str | None = None
        while True:
            page = self.list_usage_records(filter, limit=limit, cursor=cursor)
            yield from page.records
            if not page.next_cursor:
                break
            if page.next_cursor == cursor:
                raise AiplexError(
                    "server returned the same cursor twice",
                    code="pagination_stalled",
                    status_code=None,
                )
            cursor = page.next_cursor

    def get_usage_record(self, id: str) -> UsageRecord:
        """Fetch a single usage record by ID."""
        quoted = _quote_segment(id, "id")
        data = self._request("GET", f"/api/v1/usage/records/{quoted}")
        return UsageRecord.from_wire(data)

    def usage_summary(
        self,
        filter: UsageFilter | None = None,
        *,
        group_by: Sequence[str] = (),
        bucket: str | None = None,
    ) -> UsageSummary:
        """Return aggregated usage summary statistics."""
        cleaned_groups: list[str] = []
        seen: set[str] = set()
        for g in group_by:
            if not isinstance(g, str):
                raise ValueError("group_by entries must be strings")
            if g == "":
                continue
            if g not in _VALID_GROUP_BY:
                raise ValueError(f"invalid group_by field: {g!r}")
            if g in seen:
                raise ValueError(f"duplicate group_by field: {g!r}")
            seen.add(g)
            cleaned_groups.append(g)

        if bucket is not None and bucket != "":
            if bucket not in _VALID_BUCKETS:
                raise ValueError(f"invalid bucket: {bucket!r}")

        params = filter.to_query() if filter is not None else {}
        if cleaned_groups:
            params["group_by"] = ",".join(cleaned_groups)
        if bucket is not None and bucket != "":
            params["bucket"] = bucket

        data = self._request("GET", "/api/v1/usage/summary", params=params)
        return UsageSummary.from_wire(data)

    def list_prices(self) -> list[ModelPriceRecord]:
        """List all configured model prices."""
        data = self._request("GET", "/api/v1/usage/prices/")
        if not isinstance(data, list):
            raise AiplexError(
                "model prices list must be a JSON array",
                code="decode_error",
                status_code=None,
            )
        return [ModelPriceRecord.from_wire(item) for item in data]

    def get_price(self, id: str) -> ModelPriceRecord:
        """Fetch a single model price record by ID."""
        quoted = _quote_segment(id, "id")
        data = self._request("GET", f"/api/v1/usage/prices/{quoted}")
        return ModelPriceRecord.from_wire(data)

    def put_price(self, price: ModelPrice, revision: int = 0) -> ModelPriceRecord:
        """Create (revision 0) or replace a model price entry."""
        if not isinstance(price, ModelPrice):
            raise ValueError("price must be a ModelPrice")
        _validate_non_negative_revision(revision)
        quoted = _quote_segment(price.id, "price.id")
        data = self._request(
            "PUT",
            f"/api/v1/usage/prices/{quoted}",
            json_body=price.to_wire(),
            headers={"If-Match": str(revision)},
        )
        return ModelPriceRecord.from_wire(data)

    def delete_price(self, id: str, revision: int) -> None:
        """Delete a model price entry at the given positive revision."""
        quoted = _quote_segment(id, "id")
        _validate_positive_revision(revision)
        self._request(
            "DELETE",
            f"/api/v1/usage/prices/{quoted}",
            headers={"If-Match": str(revision)},
            expect_no_content=True,
        )

    def list_model_surfaces(self) -> list[ModelSurface]:
        """List all configured LLM model surfaces."""
        data = self._request("GET", "/api/v1/llm/surfaces")
        if not isinstance(data, list):
            raise AiplexError(
                "model surfaces list must be a JSON array",
                code="decode_error",
                status_code=None,
            )
        return [ModelSurface.from_wire(item) for item in data]

    def get_model_surface(self, id: str) -> ModelSurface:
        """Fetch a single LLM model surface by ID."""
        quoted = _quote_segment(id, "id")
        data = self._request("GET", f"/api/v1/llm/surfaces/{quoted}")
        return ModelSurface.from_wire(data)

    def list_tool_connections(self) -> list[ToolConnection]:
        """List all configured MCP tool connections."""
        data = self._request("GET", "/api/v1/mcp/connections")
        if not isinstance(data, list):
            raise AiplexError(
                "tool connections list must be a JSON array",
                code="decode_error",
                status_code=None,
            )
        return [ToolConnection.from_wire(item) for item in data]

    def get_tool_connection(self, id: str) -> ToolConnection:
        """Fetch a single MCP tool connection by ID."""
        quoted = _quote_segment(id, "id")
        data = self._request("GET", f"/api/v1/mcp/connections/{quoted}")
        return ToolConnection.from_wire(data)

"""Typed models and wire parsers for the AIPlex HTTP API."""

from __future__ import annotations

from dataclasses import dataclass, field
from decimal import Decimal, InvalidOperation
from typing import Any

from .errors import AiplexError


def _decode_err(msg: str) -> AiplexError:
    return AiplexError(msg, code="decode_error", status_code=None)


def _expect_dict(val: Any, name: str = "response") -> dict[str, Any]:
    if not isinstance(val, dict):
        raise _decode_err(f"{name} must be a JSON object")
    return val


def _expect_list(val: Any, name: str = "response") -> list[Any]:
    if not isinstance(val, list):
        raise _decode_err(f"{name} must be a JSON array")
    return val


def _opt_str(obj: dict[str, Any], key: str) -> str | None:
    val = obj.get(key)
    if val is None:
        return None
    if not isinstance(val, str):
        raise _decode_err(f"field '{key}' must be a string")
    return val


def _req_str(obj: dict[str, Any], key: str) -> str:
    val = _opt_str(obj, key)
    if val is None:
        raise _decode_err(f"missing required string field '{key}'")
    return val


def _opt_int(obj: dict[str, Any], key: str) -> int | None:
    val = obj.get(key)
    if val is None:
        return None
    if isinstance(val, bool) or not isinstance(val, int):
        raise _decode_err(f"field '{key}' must be an integer")
    return val


def _req_int(obj: dict[str, Any], key: str) -> int:
    val = _opt_int(obj, key)
    if val is None:
        raise _decode_err(f"missing required integer field '{key}'")
    return val


def _opt_bool(obj: dict[str, Any], key: str) -> bool | None:
    val = obj.get(key)
    if val is None:
        return None
    if not isinstance(val, bool):
        raise _decode_err(f"field '{key}' must be a boolean")
    return val


def _str_map(obj: dict[str, Any], key: str) -> dict[str, str]:
    val = obj.get(key)
    if val is None:
        return {}
    if not isinstance(val, dict):
        raise _decode_err(f"field '{key}' must be an object")
    out: dict[str, str] = {}
    for k, v in val.items():
        if not isinstance(k, str) or not isinstance(v, str):
            raise _decode_err(f"entries of '{key}' must be strings")
        out[k] = v
    return out


def _opt_float(obj: dict[str, Any], key: str) -> float | None:
    val = obj.get(key)
    if val is None:
        return None
    if isinstance(val, bool) or not isinstance(val, (int, float)):
        raise _decode_err(f"field '{key}' must be a number")
    return float(val)


def _opt_dict(obj: dict[str, Any], key: str) -> dict[str, Any] | None:
    val = obj.get(key)
    if val is None:
        return None
    if not isinstance(val, dict):
        raise _decode_err(f"field '{key}' must be an object")
    return dict(val)


def _str_list(obj: dict[str, Any], key: str) -> list[str]:
    val = obj.get(key)
    if val is None:
        return []
    if not isinstance(val, list):
        raise _decode_err(f"field '{key}' must be a list")
    out: list[str] = []
    for item in val:
        if not isinstance(item, str):
            raise _decode_err(f"elements of '{key}' must be strings")
        out.append(item)
    return out


def _dict_list(obj: dict[str, Any], key: str) -> list[dict[str, Any]]:
    val = obj.get(key)
    if val is None:
        return []
    if not isinstance(val, list):
        raise _decode_err(f"field '{key}' must be a list")
    out: list[dict[str, Any]] = []
    for item in val:
        if not isinstance(item, dict):
            raise _decode_err(f"elements of '{key}' must be objects")
        out.append(dict(item))
    return out


def _str_dict(obj: dict[str, Any], key: str) -> dict[str, str]:
    val = obj.get(key)
    if val is None:
        return {}
    if not isinstance(val, dict):
        raise _decode_err(f"field '{key}' must be an object")
    out: dict[str, str] = {}
    for k, v in val.items():
        if not isinstance(k, str) or not isinstance(v, str):
            raise _decode_err(f"entries of '{key}' must be strings")
        out[k] = v
    return out


def _int_map(obj: dict[str, Any], key: str) -> dict[str, int]:
    val = obj.get(key)
    if val is None:
        return {}
    if not isinstance(val, dict):
        raise _decode_err(f"field '{key}' must be an object")
    out: dict[str, int] = {}
    for k, v in val.items():
        if not isinstance(k, str):
            raise _decode_err(f"keys of '{key}' must be strings")
        if v is None:
            continue
        if isinstance(v, bool) or not isinstance(v, int):
            raise _decode_err(f"values of '{key}' must be integers")
        out[k] = v
    return out


def _parse_cost_fields(
    d: dict[str, Any]
) -> tuple[str | None, str | None, int | None, int | None, Decimal | None]:
    basis = _opt_str(d, "basis")
    currency = _opt_str(d, "currency")
    scale = _opt_int(d, "scale")
    amount_str = _opt_str(d, "amount")
    amount_dec_str = _opt_str(d, "amount_decimal")

    amount_minor: int | None = None
    if amount_str is not None:
        try:
            amount_minor = int(amount_str, 10)
        except ValueError as exc:
            raise _decode_err(f"invalid cost amount '{amount_str}'") from exc

    amount: Decimal | None = None
    if amount_dec_str is not None:
        try:
            amount = Decimal(amount_dec_str)
        except InvalidOperation as exc:
            raise _decode_err(f"invalid cost amount_decimal '{amount_dec_str}'") from exc
    elif amount_str is not None and scale is not None:
        try:
            amount = Decimal(amount_str).scaleb(-scale)
        except InvalidOperation as exc:
            raise _decode_err(f"invalid cost amount '{amount_str}'") from exc

    if basis == "unknown":
        return basis, None, None, None, None

    return basis, currency, amount_minor, scale, amount


@dataclass(frozen=True)
class UsageFilter:
    """Filter parameters for querying usage records and summaries."""

    plane: str | None = None
    provider: str | None = None
    model_id: str | None = None
    tool_name: str | None = None
    actor_id: str | None = None
    principal_id: str | None = None
    client_id: str | None = None
    access_point_id: str | None = None
    caller_kind: str | None = None
    #: Records that drew from this tenant budget.
    budget_id: str | None = None
    status: str | None = None
    source: str | None = None
    error_class: str | None = None
    usage_bound: str | None = None
    batch: bool | None = None
    #: Reported records from one harness (claude-code, codex, gemini-cli).
    harness: str | None = None
    #: allow, deny, transform, error, skipped, or none.
    hook_decision: str | None = None
    #: Records carrying every tag pair, sent as tag.<key>=<value>.
    tags: dict[str, str] | None = None
    since: str | None = None
    until: str | None = None

    def to_query(self) -> dict[str, str]:
        """Convert filter fields to query string parameters."""
        params: dict[str, str] = {}
        str_fields = (
            ("plane", self.plane),
            ("provider", self.provider),
            ("model_id", self.model_id),
            ("tool_name", self.tool_name),
            ("actor_id", self.actor_id),
            ("principal_id", self.principal_id),
            ("client_id", self.client_id),
            ("access_point_id", self.access_point_id),
            ("caller_kind", self.caller_kind),
            ("budget_id", self.budget_id),
            ("status", self.status),
            ("source", self.source),
            ("error_class", self.error_class),
            ("usage_bound", self.usage_bound),
            ("harness", self.harness),
            ("hook_decision", self.hook_decision),
            ("since", self.since),
            ("until", self.until),
        )
        for name, value in str_fields:
            if value is not None and value != "":
                params[name] = value
        if self.batch is not None:
            params["batch"] = "true" if self.batch else "false"
        for key, value in (self.tags or {}).items():
            params["tag." + key] = value
        return params


@dataclass(frozen=True)
class Identity:
    """Authenticated caller identity returned by whoami."""

    principal_id: str | None = None
    subject: str | None = None
    email: str | None = None
    display_name: str | None = None
    groups: list[str] = field(default_factory=list)
    domain: str | None = None
    provider: str | None = None
    pool_id: str | None = None
    is_workforce: bool | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> Identity:
        d = _expect_dict(data, "identity")
        return cls(
            principal_id=_opt_str(d, "principal_id"),
            subject=_opt_str(d, "subject"),
            email=_opt_str(d, "email"),
            display_name=_opt_str(d, "display_name"),
            groups=_str_list(d, "groups"),
            domain=_opt_str(d, "domain"),
            provider=_opt_str(d, "provider"),
            pool_id=_opt_str(d, "pool_id"),
            is_workforce=_opt_bool(d, "is_workforce"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class Whoami:
    """Caller identity, roles, and scopes."""

    configuration_revision: int | None = None
    identity: Identity | None = None
    roles: list[str] = field(default_factory=list)
    scopes: list[str] = field(default_factory=list)
    group_scopes: list[str] = field(default_factory=list)
    matched_bindings: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> Whoami:
        d = _expect_dict(data, "whoami")
        ident_raw = d.get("identity")
        ident = Identity.from_wire(ident_raw) if ident_raw is not None else None
        return cls(
            configuration_revision=_opt_int(d, "configuration_revision"),
            identity=ident,
            roles=_str_list(d, "roles"),
            scopes=_str_list(d, "scopes"),
            group_scopes=_str_list(d, "group_scopes"),
            matched_bindings=_str_list(d, "matched_bindings"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class AccessPointModel:
    """Model route entry inside an access point specification."""

    name: str | None = None
    surface_ref: str | None = None
    native_model: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> AccessPointModel:
        d = _expect_dict(data, "access_point.model")
        return cls(
            name=_opt_str(d, "name"),
            surface_ref=_opt_str(d, "surface_ref"),
            native_model=_opt_str(d, "native_model"),
            raw=dict(d),
        )

    def to_wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.name is not None:
            out["name"] = self.name
        if self.surface_ref is not None:
            out["surface_ref"] = self.surface_ref
        if self.native_model is not None:
            out["native_model"] = self.native_model
        return out


@dataclass(frozen=True)
class AccessPointTool:
    """Tool connection exposure entry inside an access point specification."""

    connection_ref: str
    expose: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> AccessPointTool:
        d = _expect_dict(data, "access_point.tool")
        return cls(
            connection_ref=_req_str(d, "connection_ref"),
            expose=_str_list(d, "expose"),
            raw=dict(d),
        )

    def to_wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {"connection_ref": self.connection_ref}
        if self.expose:
            out["expose"] = list(self.expose)
        return out


@dataclass(frozen=True)
class AccessPointSpec:
    """Specification of an access point."""

    id: str
    display_name: str | None = None
    caller_kinds: list[str] = field(default_factory=list)
    subjects: dict[str, Any] | None = None
    models: list[AccessPointModel] = field(default_factory=list)
    tools: list[AccessPointTool] = field(default_factory=list)
    skills: list[dict[str, Any]] = field(default_factory=list)
    qos: dict[str, Any] | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any, fallback_id: str | None = None) -> AccessPointSpec:
        d = _expect_dict(data, "access_point.spec")
        ap_id = _opt_str(d, "id") or fallback_id
        if ap_id is None:
            raise _decode_err("missing required string field 'id' in access point spec")
        models_raw = d.get("models")
        models: list[AccessPointModel] = []
        if models_raw is not None:
            for item in _expect_list(models_raw, "models"):
                models.append(AccessPointModel.from_wire(item))
        tools_raw = d.get("tools")
        tools: list[AccessPointTool] = []
        if tools_raw is not None:
            for item in _expect_list(tools_raw, "tools"):
                tools.append(AccessPointTool.from_wire(item))
        return cls(
            id=ap_id,
            display_name=_opt_str(d, "display_name"),
            caller_kinds=_str_list(d, "caller_kinds"),
            subjects=_opt_dict(d, "subjects"),
            models=models,
            tools=tools,
            skills=_dict_list(d, "skills"),
            qos=_opt_dict(d, "qos"),
            raw=dict(d),
        )

    def to_wire(self) -> dict[str, Any]:
        """Serialize the specification into a JSON-compatible dictionary."""
        out: dict[str, Any] = {
            "id": self.id,
            "caller_kinds": list(self.caller_kinds),
        }
        if self.display_name is not None:
            out["display_name"] = self.display_name
        if self.subjects is not None:
            out["subjects"] = dict(self.subjects)
        if self.models:
            out["models"] = [m.to_wire() for m in self.models]
        if self.tools:
            out["tools"] = [t.to_wire() for t in self.tools]
        if self.skills:
            out["skills"] = [dict(s) for s in self.skills]
        if self.qos is not None:
            out["qos"] = dict(self.qos)
        return out


@dataclass(frozen=True)
class AccessPointRecord:
    """Stored access point record with optimistic concurrency revision."""

    tenant_id: str | None
    kind: str | None
    id: str
    revision: int
    spec: AccessPointSpec
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> AccessPointRecord:
        d = _expect_dict(data, "access_point_record")
        rec_id = _req_str(d, "id")
        rev = _req_int(d, "revision")
        spec_raw = d.get("spec")
        spec = AccessPointSpec.from_wire(spec_raw, fallback_id=rec_id)
        return cls(
            tenant_id=_opt_str(d, "tenant_id"),
            kind=_opt_str(d, "kind"),
            id=rec_id,
            revision=rev,
            spec=spec,
            raw=dict(d),
        )


@dataclass(frozen=True)
class ConnectModel:
    """Model entry in a connect configuration plan."""

    name: str | None = None
    protocols: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ConnectModel:
        d = _expect_dict(data, "connect.model")
        return cls(
            name=_opt_str(d, "name"),
            protocols=_str_list(d, "protocols"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class ConnectSnippet:
    """Configuration or command snippet in a connect plan."""

    title: str | None = None
    language: str | None = None
    content: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ConnectSnippet:
        d = _expect_dict(data, "connect.snippet")
        return cls(
            title=_opt_str(d, "title"),
            language=_opt_str(d, "language"),
            content=_opt_str(d, "content"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class ConnectConfig:
    """Client connection configuration for an access point."""

    access_point_id: str | None = None
    client: str | None = None
    base_urls: dict[str, str] = field(default_factory=dict)
    endpoints: list[dict[str, Any]] = field(default_factory=list)
    models: list[ConnectModel] = field(default_factory=list)
    mcp_url: str | None = None
    mcp_catalog_url: str | None = None
    auth: dict[str, Any] = field(default_factory=dict)
    snippets: list[ConnectSnippet] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ConnectConfig:
        d = _expect_dict(data, "connect_config")
        models_raw = d.get("models")
        models: list[ConnectModel] = []
        if models_raw is not None:
            for item in _expect_list(models_raw, "models"):
                models.append(ConnectModel.from_wire(item))
        snippets_raw = d.get("snippets")
        snippets: list[ConnectSnippet] = []
        if snippets_raw is not None:
            for item in _expect_list(snippets_raw, "snippets"):
                snippets.append(ConnectSnippet.from_wire(item))
        auth_val = _opt_dict(d, "auth")
        return cls(
            access_point_id=_opt_str(d, "access_point_id"),
            client=_opt_str(d, "client"),
            base_urls=_str_dict(d, "base_urls"),
            endpoints=_dict_list(d, "endpoints"),
            models=models,
            mcp_url=_opt_str(d, "mcp_url"),
            mcp_catalog_url=_opt_str(d, "mcp_catalog_url"),
            auth=auth_val if auth_val is not None else {},
            snippets=snippets,
            notes=_str_list(d, "notes"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageCounters:
    """Nullable token usage counters for a single usage record."""

    input_tokens: int | None = None
    output_tokens: int | None = None
    cache_read_tokens: int | None = None
    cache_write_tokens: int | None = None
    reasoning_tokens: int | None = None
    total_tokens: int | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageCounters:
        d = _expect_dict(data, "usage_counters")
        return cls(
            input_tokens=_opt_int(d, "input_tokens"),
            output_tokens=_opt_int(d, "output_tokens"),
            cache_read_tokens=_opt_int(d, "cache_read_tokens"),
            cache_write_tokens=_opt_int(d, "cache_write_tokens"),
            reasoning_tokens=_opt_int(d, "reasoning_tokens"),
            total_tokens=_opt_int(d, "total_tokens"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class Cost:
    """Fixed-point monetary cost of a usage record."""

    basis: str | None = None
    currency: str | None = None
    amount_minor: int | None = None
    scale: int | None = None
    amount: Decimal | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> Cost:
        d = _expect_dict(data, "cost")
        basis, currency, amount_minor, scale, amount = _parse_cost_fields(d)
        return cls(
            basis=basis,
            currency=currency,
            amount_minor=amount_minor,
            scale=scale,
            amount=amount,
            raw=dict(d),
        )


@dataclass(frozen=True)
class CostTotal:
    """Aggregated fixed-point monetary cost across records."""

    currency: str | None = None
    basis: str | None = None
    amount_minor: int | None = None
    scale: int | None = None
    amount: Decimal | None = None
    records: int | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> CostTotal:
        d = _expect_dict(data, "cost_total")
        basis, currency, amount_minor, scale, amount = _parse_cost_fields(d)
        return cls(
            currency=currency,
            basis=basis,
            amount_minor=amount_minor,
            scale=scale,
            amount=amount,
            records=_opt_int(d, "records"),
            raw=dict(d),
        )


def _cost_total_list(obj: dict[str, Any], key: str = "cost") -> list[CostTotal]:
    val = obj.get(key)
    if val is None:
        return []
    items = _expect_list(val, key)
    return [CostTotal.from_wire(item) for item in items]


@dataclass(frozen=True)
class Feedback:
    """One feedback entry on a usage record. Append-only; the newest is current."""

    id: str | None = None
    record_id: str | None = None
    by: str | None = None
    created_at: str | None = None
    #: From -1 to 1. None means the field was absent, never "neutral".
    score: float | None = None
    label: str | None = None
    comment: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> Feedback:
        d = _expect_dict(data, "feedback")
        return cls(
            id=_opt_str(d, "id"),
            record_id=_opt_str(d, "record_id"),
            by=_opt_str(d, "by"),
            created_at=_opt_str(d, "created_at"),
            score=_opt_float(d, "score"),
            label=_opt_str(d, "label"),
            comment=_opt_str(d, "comment"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class FeedbackTotals:
    """Latest-feedback totals of a summary group.

    ``average_score`` is None when no record has feedback; records without
    feedback are counted in ``without_feedback`` and are not a neutral score.
    """

    records: int | None = None
    without_feedback: int | None = None
    positive: int | None = None
    negative: int | None = None
    neutral: int | None = None
    average_score: float | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> FeedbackTotals:
        d = _expect_dict(data, "feedback_totals")
        return cls(
            records=_opt_int(d, "records"),
            without_feedback=_opt_int(d, "without_feedback"),
            positive=_opt_int(d, "positive"),
            negative=_opt_int(d, "negative"),
            neutral=_opt_int(d, "neutral"),
            average_score=_opt_float(d, "average_score"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageRecord:
    """Single usage ledger record."""

    id: str | None = None
    tenant_id: str | None = None
    started_at: str | None = None
    finished_at: str | None = None
    latency_ms: int | None = None
    plane: str | None = None
    provider: str | None = None
    resource_id: str | None = None
    operation: str | None = None
    model_id: str | None = None
    model_version: str | None = None
    principal_id: str | None = None
    principal_display: str | None = None
    access_point_id: str | None = None
    caller_kind: str | None = None
    request_id: str | None = None
    trace_id: str | None = None
    span_id: str | None = None
    status: str | None = None
    source: str | None = None
    streaming: bool | None = None
    http_status: int | None = None
    requests: int | None = None
    usage: UsageCounters | None = None
    cost: Cost | None = None
    #: Reported records: the harness (claude-code, codex, gemini-cli), its
    #: version and its own session id. Labels the sender chose, never identity.
    harness: str | None = None
    harness_version: str | None = None
    session_id: str | None = None
    client_request_id: str | None = None
    caller_span_id: str | None = None
    reported_attributes: dict[str, str] = field(default_factory=dict)
    #: Experiment labels from access point labels and allowlisted headers.
    tags: dict[str, str] = field(default_factory=dict)
    #: Tenant budgets the exchange drew from (own and ancestors).
    budget_ids: list[str] = field(default_factory=list)
    #: Request-hook summary (decision, results); None when no hook ran.
    hooks: dict[str, Any] | None = None
    #: Latest feedback, present on a record fetched with get_usage_record.
    feedback: Feedback | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageRecord:
        d = _expect_dict(data, "usage_record")
        usage_raw = d.get("usage")
        usage = UsageCounters.from_wire(usage_raw) if usage_raw is not None else None
        cost_raw = d.get("cost")
        cost = Cost.from_wire(cost_raw) if cost_raw is not None else None
        return cls(
            id=_opt_str(d, "id"),
            tenant_id=_opt_str(d, "tenant_id"),
            started_at=_opt_str(d, "started_at"),
            finished_at=_opt_str(d, "finished_at"),
            latency_ms=_opt_int(d, "latency_ms"),
            plane=_opt_str(d, "plane"),
            provider=_opt_str(d, "provider"),
            resource_id=_opt_str(d, "resource_id"),
            operation=_opt_str(d, "operation"),
            model_id=_opt_str(d, "model_id"),
            model_version=_opt_str(d, "model_version"),
            principal_id=_opt_str(d, "principal_id"),
            principal_display=_opt_str(d, "principal_display"),
            access_point_id=_opt_str(d, "access_point_id"),
            caller_kind=_opt_str(d, "caller_kind"),
            request_id=_opt_str(d, "request_id"),
            trace_id=_opt_str(d, "trace_id"),
            span_id=_opt_str(d, "span_id"),
            status=_opt_str(d, "status"),
            source=_opt_str(d, "source"),
            streaming=_opt_bool(d, "streaming"),
            http_status=_opt_int(d, "http_status"),
            requests=_opt_int(d, "requests"),
            usage=usage,
            cost=cost,
            harness=_opt_str(d, "harness"),
            harness_version=_opt_str(d, "harness_version"),
            session_id=_opt_str(d, "session_id"),
            client_request_id=_opt_str(d, "client_request_id"),
            caller_span_id=_opt_str(d, "caller_span_id"),
            reported_attributes=_str_map(d, "reported_attributes"),
            tags=_str_map(d, "tags"),
            budget_ids=_str_list(d, "budget_ids"),
            hooks=_opt_dict(d, "hooks"),
            feedback=Feedback.from_wire(d["feedback"]) if d.get("feedback") is not None else None,
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageRecordPage:
    """Page of usage records with optional pagination cursor."""

    records: list[UsageRecord] = field(default_factory=list)
    next_cursor: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageRecordPage:
        d = _expect_dict(data, "usage_record_page")
        recs_raw = d.get("records")
        records: list[UsageRecord] = []
        if recs_raw is not None:
            for item in _expect_list(recs_raw, "records"):
                records.append(UsageRecord.from_wire(item))
        next_cursor = _opt_str(d, "next_cursor")
        if next_cursor == "":
            next_cursor = None
        return cls(
            records=records,
            next_cursor=next_cursor,
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageSummaryGroup:
    """Single grouped bucket in a usage summary response."""

    key: dict[str, str | None] = field(default_factory=dict)
    records: int | None = None
    requests: int | None = None
    errors: int | None = None
    denied: int | None = None
    unknown_status: int | None = None
    unknown_usage_records: int | None = None
    unknown_cost_records: int | None = None
    tokens: dict[str, int] = field(default_factory=dict)
    tokens_unknown: dict[str, int] = field(default_factory=dict)
    cost: list[CostTotal] = field(default_factory=list)
    #: Present when the summary asked for feedback.
    feedback: FeedbackTotals | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageSummaryGroup:
        d = _expect_dict(data, "usage_summary_group")
        key_raw = d.get("key")
        key_map: dict[str, str | None] = {}
        if key_raw is not None:
            kd = _expect_dict(key_raw, "key")
            for k, v in kd.items():
                if not isinstance(k, str):
                    raise _decode_err("group key names must be strings")
                if v is None:
                    key_map[k] = None
                elif isinstance(v, str):
                    key_map[k] = v
                elif isinstance(v, bool):
                    key_map[k] = "true" if v else "false"
                else:
                    raise _decode_err(f"group key '{k}' must be string or null")
        return cls(
            key=key_map,
            records=_opt_int(d, "records"),
            requests=_opt_int(d, "requests"),
            errors=_opt_int(d, "errors"),
            denied=_opt_int(d, "denied"),
            unknown_status=_opt_int(d, "unknown_status"),
            unknown_usage_records=_opt_int(d, "unknown_usage_records"),
            unknown_cost_records=_opt_int(d, "unknown_cost_records"),
            tokens=_int_map(d, "tokens"),
            tokens_unknown=_int_map(d, "tokens_unknown"),
            cost=_cost_total_list(d, "cost"),
            feedback=FeedbackTotals.from_wire(d["feedback"]) if d.get("feedback") is not None else None,
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageSummary:
    """Aggregated usage summary across matching ledger records."""

    group_by: list[str] = field(default_factory=list)
    groups: list[UsageSummaryGroup] = field(default_factory=list)
    records_scanned: int | None = None
    truncated: bool | None = None
    #: True when managed and reported records are separate groups.
    source_split: bool | None = None
    #: Reported records left out because they describe a request that has a
    #: managed record. None when the server did not report it (zero is omitted).
    excluded_correlated_reported: int | None = None
    #: How sources were treated, in plain words.
    notes: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageSummary:
        d = _expect_dict(data, "usage_summary")
        groups_raw = d.get("groups")
        groups: list[UsageSummaryGroup] = []
        if groups_raw is not None:
            for item in _expect_list(groups_raw, "groups"):
                groups.append(UsageSummaryGroup.from_wire(item))
        return cls(
            group_by=_str_list(d, "group_by"),
            groups=groups,
            records_scanned=_opt_int(d, "records_scanned"),
            truncated=_opt_bool(d, "truncated"),
            source_split=_opt_bool(d, "source_split"),
            excluded_correlated_reported=_opt_int(d, "excluded_correlated_reported"),
            notes=_str_list(d, "notes"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class UsageTotals:
    """Usage rollup totals embedded in overview responses."""

    requests: int | None = None
    records: int | None = None
    errors: int | None = None
    denied: int | None = None
    tokens: dict[str, int] = field(default_factory=dict)
    tokens_unknown: dict[str, int] = field(default_factory=dict)
    cost: list[CostTotal] = field(default_factory=list)
    unknown_cost_records: int | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> UsageTotals:
        d = _expect_dict(data, "usage_totals")
        return cls(
            requests=_opt_int(d, "requests"),
            records=_opt_int(d, "records"),
            errors=_opt_int(d, "errors"),
            denied=_opt_int(d, "denied"),
            tokens=_int_map(d, "tokens"),
            tokens_unknown=_int_map(d, "tokens_unknown"),
            cost=_cost_total_list(d, "cost"),
            unknown_cost_records=_opt_int(d, "unknown_cost_records"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class ModelPriceRate:
    """Rational price rate (numerator / denominator) for a single usage meter."""

    numerator: int | None = None
    denominator: int | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ModelPriceRate:
        d = _expect_dict(data, "model_price_rate")
        return cls(
            numerator=_opt_int(d, "numerator"),
            denominator=_opt_int(d, "denominator"),
            raw=dict(d),
        )

    def to_wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.numerator is not None:
            out["numerator"] = self.numerator
        if self.denominator is not None:
            out["denominator"] = self.denominator
        return out


@dataclass(frozen=True)
class ModelPrice:
    """Specification of a model price table entry."""

    id: str
    model_id: str | None = None
    currency: str | None = None
    scale: int | None = None
    meters: dict[str, ModelPriceRate] = field(default_factory=dict)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any, fallback_id: str | None = None) -> ModelPrice:
        d = _expect_dict(data, "model_price")
        price_id = _opt_str(d, "id") or fallback_id
        if price_id is None:
            raise _decode_err("missing required string field 'id' in model price")
        meters_raw = d.get("meters")
        meters: dict[str, ModelPriceRate] = {}
        if meters_raw is not None:
            md = _expect_dict(meters_raw, "meters")
            for k, v in md.items():
                if not isinstance(k, str):
                    raise _decode_err("meter names must be strings")
                if v is not None:
                    meters[k] = ModelPriceRate.from_wire(v)
        return cls(
            id=price_id,
            model_id=_opt_str(d, "model_id"),
            currency=_opt_str(d, "currency"),
            scale=_opt_int(d, "scale"),
            meters=meters,
            raw=dict(d),
        )

    def to_wire(self) -> dict[str, Any]:
        """Serialize the model price specification to a wire dictionary."""
        out: dict[str, Any] = {"id": self.id}
        if self.model_id is not None:
            out["model_id"] = self.model_id
        if self.currency is not None:
            out["currency"] = self.currency
        if self.scale is not None:
            out["scale"] = self.scale
        meters_out: dict[str, Any] = {}
        for k, rate in self.meters.items():
            if rate is not None:
                meters_out[k] = rate.to_wire()
        out["meters"] = meters_out
        return out


@dataclass(frozen=True)
class ModelPriceRecord:
    """Stored model price record with revision."""

    id: str
    revision: int
    tenant_id: str | None
    kind: str | None
    price: ModelPrice
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ModelPriceRecord:
        d = _expect_dict(data, "model_price_record")
        rec_id = _req_str(d, "id")
        rev = _req_int(d, "revision")
        spec_raw = d.get("spec")
        price = ModelPrice.from_wire(spec_raw if spec_raw is not None else d, fallback_id=rec_id)
        return cls(
            id=rec_id,
            revision=rev,
            tenant_id=_opt_str(d, "tenant_id"),
            kind=_opt_str(d, "kind"),
            price=price,
            raw=dict(d),
        )


@dataclass(frozen=True)
class ModelSurface:
    """Stored LLM model surface record."""

    id: str
    revision: int
    tenant_id: str | None = None
    kind: str | None = None
    protocol: str | None = None
    provider_id: str | None = None
    models: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ModelSurface:
        d = _expect_dict(data, "model_surface")
        rec_id = _req_str(d, "id")
        rev = _req_int(d, "revision")
        spec = _opt_dict(d, "spec") or {}
        surface = _opt_dict(spec, "surface") or {}
        provider = _opt_dict(surface, "provider") or {}
        return cls(
            id=rec_id,
            revision=rev,
            tenant_id=_opt_str(d, "tenant_id"),
            kind=_opt_str(d, "kind"),
            protocol=_opt_str(surface, "protocol"),
            provider_id=_opt_str(provider, "id"),
            models=_str_list(surface, "models"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class ToolConnection:
    """Stored MCP tool connection record."""

    id: str
    revision: int
    tenant_id: str | None = None
    kind: str | None = None
    resource_url: str | None = None
    auth_type: str | None = None
    allowed_tools: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> ToolConnection:
        d = _expect_dict(data, "tool_connection")
        rec_id = _req_str(d, "id")
        rev = _req_int(d, "revision")
        spec = _opt_dict(d, "spec") or {}
        auth = _opt_dict(spec, "auth") or {}
        execution = _opt_dict(spec, "execution") or {}
        return cls(
            id=rec_id,
            revision=rev,
            tenant_id=_opt_str(d, "tenant_id"),
            kind=_opt_str(d, "kind"),
            resource_url=_opt_str(spec, "resource_url"),
            auth_type=_opt_str(auth, "type"),
            allowed_tools=_str_list(execution, "allowed_tools"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewAccessPointSummary:
    """Summary entry for an access point in the overview list."""

    id: str
    display_name: str | None = None
    caller_kinds: list[str] = field(default_factory=list)
    counts: dict[str, int] = field(default_factory=dict)
    quota_coverage_state: str | None = None
    quota_coverage_detail: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewAccessPointSummary:
        d = _expect_dict(data, "overview_access_point_summary")
        qc = _opt_dict(d, "quota_coverage") or {}
        return cls(
            id=_req_str(d, "id"),
            display_name=_opt_str(d, "display_name"),
            caller_kinds=_str_list(d, "caller_kinds"),
            counts=_int_map(d, "counts"),
            quota_coverage_state=_opt_str(qc, "state"),
            quota_coverage_detail=_opt_str(qc, "detail"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewAccessPointPage:
    """Paginated list of access point overview summaries."""

    access_points: list[OverviewAccessPointSummary] = field(default_factory=list)
    next_cursor: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewAccessPointPage:
        d = _expect_dict(data, "overview_access_point_page")
        ap_raw = d.get("access_points")
        aps: list[OverviewAccessPointSummary] = []
        if ap_raw is not None:
            for item in _expect_list(ap_raw, "access_points"):
                aps.append(OverviewAccessPointSummary.from_wire(item))
        next_cursor = _opt_str(d, "next_cursor")
        if next_cursor == "":
            next_cursor = None
        return cls(
            access_points=aps,
            next_cursor=next_cursor,
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewAccessPointDetail:
    """Detailed overview of a single access point."""

    id: str | None = None
    display_name: str | None = None
    revision: int | None = None
    since: str | None = None
    usage_available: bool | None = None
    usage_truncated: bool | None = None
    usage: UsageTotals | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewAccessPointDetail:
        d = _expect_dict(data, "overview_access_point_detail")
        ap = _opt_dict(d, "access_point") or {}
        ap_id = _opt_str(ap, "id") or _opt_str(d, "id")
        display_name = _opt_str(ap, "display_name") or _opt_str(d, "display_name")
        usage_block = _opt_dict(d, "usage")
        usage_available: bool | None = None
        usage_truncated: bool | None = None
        usage_totals: UsageTotals | None = None
        if usage_block is not None:
            usage_available = _opt_bool(usage_block, "available")
            usage_truncated = _opt_bool(usage_block, "truncated")
            total_raw = usage_block.get("total")
            if total_raw is not None:
                usage_totals = UsageTotals.from_wire(total_raw)
        return cls(
            id=ap_id,
            display_name=display_name,
            revision=_opt_int(d, "revision"),
            since=_opt_str(d, "since"),
            usage_available=usage_available,
            usage_truncated=usage_truncated,
            usage=usage_totals,
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewSubject:
    """Subject entry in an overview subjects list."""

    kind: str | None = None
    id: str | None = None
    display_name: str | None = None
    identity: dict[str, Any] | None = None
    access_points: list[str] = field(default_factory=list)
    last_seen: str | None = None
    known_from: list[str] = field(default_factory=list)
    usage: UsageTotals | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewSubject:
        d = _expect_dict(data, "overview_subject")
        usage_raw = d.get("usage")
        usage = UsageTotals.from_wire(usage_raw) if usage_raw is not None else None
        return cls(
            kind=_opt_str(d, "kind"),
            id=_opt_str(d, "id"),
            display_name=_opt_str(d, "display_name"),
            identity=_opt_dict(d, "identity"),
            access_points=_str_list(d, "access_points"),
            last_seen=_opt_str(d, "last_seen"),
            known_from=_str_list(d, "known_from"),
            usage=usage,
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewSubjectPage:
    """Paginated list of overview subjects for a caller kind."""

    kind: str | None = None
    since: str | None = None
    subjects: list[OverviewSubject] = field(default_factory=list)
    usage_available: bool | None = None
    usage_truncated: bool | None = None
    derived_from: list[str] = field(default_factory=list)
    limitations: list[str] = field(default_factory=list)
    next_cursor: str | None = None
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewSubjectPage:
        d = _expect_dict(data, "overview_subject_page")
        subj_raw = d.get("subjects")
        subjects: list[OverviewSubject] = []
        if subj_raw is not None:
            for item in _expect_list(subj_raw, "subjects"):
                subjects.append(OverviewSubject.from_wire(item))
        next_cursor = _opt_str(d, "next_cursor")
        if next_cursor == "":
            next_cursor = None
        return cls(
            kind=_opt_str(d, "kind"),
            since=_opt_str(d, "since"),
            subjects=subjects,
            usage_available=_opt_bool(d, "usage_available"),
            usage_truncated=_opt_bool(d, "usage_truncated"),
            derived_from=_str_list(d, "derived_from"),
            limitations=_str_list(d, "limitations"),
            next_cursor=next_cursor,
            raw=dict(d),
        )


@dataclass(frozen=True)
class OverviewSubjectDetail:
    """Detailed overview of a single caller subject."""

    kind: str | None = None
    id: str | None = None
    identity: dict[str, Any] | None = None
    access_points: list[dict[str, Any]] = field(default_factory=list)
    since: str | None = None
    last_seen: str | None = None
    usage: UsageTotals | None = None
    notes: list[str] = field(default_factory=list)
    known_from: list[str] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> OverviewSubjectDetail:
        d = _expect_dict(data, "overview_subject_detail")
        usage_raw = d.get("usage")
        usage = UsageTotals.from_wire(usage_raw) if usage_raw is not None else None
        return cls(
            kind=_opt_str(d, "kind"),
            id=_opt_str(d, "id"),
            identity=_opt_dict(d, "identity"),
            access_points=_dict_list(d, "access_points"),
            since=_opt_str(d, "since"),
            last_seen=_opt_str(d, "last_seen"),
            usage=usage,
            notes=_str_list(d, "notes"),
            known_from=_str_list(d, "known_from"),
            raw=dict(d),
        )


@dataclass(frozen=True)
class BudgetStatus:
    """A budget's counters in its current window. Amounts are exact decimal strings."""

    id: str
    unit: str
    limit: str
    spent: str
    reserved: str
    remaining: str
    display_name: str | None = None
    parent: str | None = None
    currency: str | None = None
    over_limit: bool = False
    window_start: str | None = None
    window_end: str | None = None
    #: Operator-only; None for a member. Never zero when not reported.
    unknown_cost_requests: int | None = None
    threshold_events: list[dict[str, Any]] = field(default_factory=list)
    children: list[dict[str, Any]] = field(default_factory=list)
    top_spenders: list[dict[str, Any]] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_wire(cls, data: Any) -> BudgetStatus:
        d = _expect_dict(data, "budget_status")
        window = _opt_dict(d, "window") or {}

        def req(key: str) -> str:
            value = d.get(key)
            if not isinstance(value, str):
                raise AiplexError(
                    f"budget_status.{key} must be a string", code="decode_error", status_code=None
                )
            return value

        def dicts(key: str) -> list[dict[str, Any]]:
            value = d.get(key)
            return [dict(x) for x in value if isinstance(x, dict)] if isinstance(value, list) else []

        return cls(
            id=req("id"),
            unit=req("unit"),
            limit=req("limit"),
            spent=req("spent"),
            reserved=req("reserved"),
            remaining=req("remaining"),
            display_name=_opt_str(d, "display_name"),
            parent=_opt_str(d, "parent"),
            currency=_opt_str(d, "currency"),
            over_limit=bool(_opt_bool(d, "over_limit")),
            window_start=_opt_str(window, "start"),
            window_end=_opt_str(window, "end"),
            unknown_cost_requests=_opt_int(d, "unknown_cost_requests"),
            threshold_events=dicts("threshold_events"),
            children=dicts("children"),
            top_spenders=dicts("top_spenders"),
            raw=dict(d),
        )

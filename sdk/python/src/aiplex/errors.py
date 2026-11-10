"""Exception definitions for the AIPlex client."""

from __future__ import annotations

from typing import Any


class AiplexError(Exception):
    """Error raised for any API, transport, or decoding failure."""

    status_code: int | None
    code: str
    message: str
    details: dict[str, Any] | None
    request_id: str | None
    trace_id: str | None

    def __init__(
        self,
        message: str,
        *,
        code: str = "unknown",
        status_code: int | None = None,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
        trace_id: str | None = None,
    ) -> None:
        self.message = message
        self.code = code
        self.status_code = status_code
        self.details = details
        self.request_id = request_id
        self.trace_id = trace_id
        super().__init__(self._format())

    def _format(self) -> str:
        if self.status_code is None:
            if self.code == "transport_error":
                return f"aiplex: {self.code}: {self.message}"
            return f"aiplex: {self.code} (transport): {self.message}"
        return f"aiplex: {self.code} ({self.status_code}): {self.message}"

    @property
    def is_revision_conflict(self) -> bool:
        """Return True when the server rejected the write with HTTP 409."""
        return self.status_code == 409

    def __str__(self) -> str:
        return self._format()

    def __reduce__(
        self,
    ) -> tuple[type[AiplexError], tuple[str], dict[str, Any]]:
        return (
            self.__class__,
            (self.message,),
            {
                "code": self.code,
                "status_code": self.status_code,
                "details": self.details,
                "request_id": self.request_id,
                "trace_id": self.trace_id,
            },
        )


def redact_value(value: Any, secret: str) -> Any:
    """Recursively replace occurrences of secret in strings, dicts, and lists."""
    if not secret:
        return value
    if isinstance(value, str):
        return value.replace(secret, "[redacted]")
    if isinstance(value, dict):
        return {redact_value(k, secret): redact_value(v, secret) for k, v in value.items()}
    if isinstance(value, list):
        return [redact_value(item, secret) for item in value]
    return value

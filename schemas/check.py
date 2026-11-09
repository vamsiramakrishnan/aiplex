#!/usr/bin/env python3
"""Check the language-neutral v1 wire contracts and representative examples."""

from __future__ import annotations

import copy
import datetime
import json
from pathlib import Path
import re

from jsonschema import Draft202012Validator, FormatChecker, ValidationError


ROOT = Path(__file__).resolve().parent
FORMATS = FormatChecker()


@FORMATS.checks("date-time", raises=ValueError)
def timestamp(value: object) -> bool:
    # Explicitly enable RFC3339 formats without jsonschema's optional packages.
    if not isinstance(value, str):
        return True
    if not re.fullmatch(
        r"\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[Zz]|[+-]\d{2}:\d{2})",
        value,
    ):
        return False
    datetime.datetime.fromisoformat(value.upper().replace("Z", "+00:00"))
    return True


def validator(name: str) -> Draft202012Validator:
    schema = json.loads((ROOT / "v1" / f"{name}.schema.json").read_text())
    Draft202012Validator.check_schema(schema)
    return Draft202012Validator(schema, format_checker=FORMATS)


def reject(checker: Draft202012Validator, value: object) -> None:
    try:
        checker.validate(value)
    except ValidationError:
        return
    raise AssertionError(f"Invalid wire object accepted: {value!r}")


def main() -> None:
    request = {
        "version": "v1", "tenant_id": "example", "request_id": "request-1",
        "principal_id": "principal-1", "actor_id": "agent-1", "resource_id": "model-1",
        "membership_snapshot_ref": {"id": "membership-1", "version": "7"},
    }
    snapshot = {
        "version": "v1", "ref": request["membership_snapshot_ref"],
        "tenant_id": "example", "principal_id": "principal-1",
        "groups": ["entra:engineering", "entra:research"],
        "issued_at": "2026-11-09T00:00:00Z", "expires_at": "2026-11-09T00:05:00Z",
    }
    descriptor = json.loads(
        (ROOT.parent / "examples/modules/echo/modules.json").read_text()
    )["modules"][0]["descriptor"]
    fixtures = {
        "request-context": request, "membership-snapshot": snapshot,
        "module-descriptor": descriptor,
    }
    for name, value in fixtures.items():
        checker = validator(name)
        checker.validate(value)
        invalid = {**value, "unexpected_field": "not-v1"}
        reject(checker, invalid)
        for field in ("version",) if name != "module-descriptor" else ("contract_version", "name"):
            invalid = copy.deepcopy(value)
            del invalid[field]
            reject(checker, invalid)

    request_checker = validator("request-context")
    reject(request_checker, {**request, "version": "v2"})
    reject(request_checker, {**request, "principal_id": ""})
    reject(request_checker, {**request, "membership_snapshot_ref": {"id": "incomplete"}})
    snapshot_checker = validator("membership-snapshot")
    reject(snapshot_checker, {**snapshot, "groups": ["entra:research", "entra:research"]})
    reject(snapshot_checker, {**snapshot, "expires_at": "not-a-time"})
    descriptor_checker = validator("module-descriptor")
    reject(descriptor_checker, {**descriptor, "version": "1.0.0-prerelease"})
    reject(descriptor_checker, {**descriptor, "capabilities": ["example.echo", "example.echo"]})
    reject(descriptor_checker, {**descriptor, "api_prefixes": ["/api/../admin"]})
    reject(descriptor_checker, {**descriptor, "config_schema": []})
    print("Validated 3 v1 schemas, standalone module descriptor, and invalid-wire fixtures")


if __name__ == "__main__":
    main()

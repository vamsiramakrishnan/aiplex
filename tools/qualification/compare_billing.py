#!/usr/bin/env python3
"""Compare normalized per-attempt provider evidence with QuotaPlex accounting.

This reads local files only. It neither queries billing services nor normalizes a
provider invoice: the operator must establish the evidence/mapping provenance.
All quantities use exact decimal integers; floating JSON numbers are refused.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys

MAX_FILE_BYTES = 64 << 20
MAX_LINE_BYTES = 1 << 20
MAX_RECORDS = 100_000
MAX_INTEGER = (1 << 63) - 1


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = value
    return result


def forbidden_number(_):
    raise ValueError("quantities must be exact JSON integers")


def integer(value, maximum=MAX_INTEGER):
    if type(value) is not int or not 0 <= value <= maximum:
        raise ValueError("bounded nonnegative integer required")
    return value


def text(value):
    if not isinstance(value, str) or not value or len(value) > 1024 or any(ord(c) < 32 or ord(c) == 127 for c in value):
        raise ValueError("bounded nonempty text required")
    return value


def load(path: Path):
    if path.stat().st_size > MAX_FILE_BYTES:
        raise ValueError("evidence file exceeds 64MiB")
    records = {}
    consumed_bytes = 0
    with path.open(encoding="utf-8") as stream:
        for line in stream:
            line_bytes = len(line.encode("utf-8"))
            consumed_bytes += line_bytes
            if consumed_bytes > MAX_FILE_BYTES:
                raise ValueError("evidence file exceeds 64MiB")
            if line_bytes > MAX_LINE_BYTES:
                raise ValueError("evidence record exceeds 1MiB")
            if not line.strip():
                continue
            record = json.loads(line, object_pairs_hook=unique_object, parse_float=forbidden_number, parse_constant=forbidden_number)
            if not isinstance(record, dict) or set(record) - {"tenant_id", "attempt_id", "model_version", "counters", "money", "evidence_ref"}:
                raise ValueError("unsupported evidence shape")
            key = (text(record.get("tenant_id")), text(record.get("attempt_id")))
            text(record.get("model_version"))
            if "evidence_ref" in record:
                text(record["evidence_ref"])
            counters, money = record.get("counters"), record.get("money")
            if not isinstance(counters, dict) or not counters or len(counters) > 64 or not isinstance(money, dict) or len(money) > 8:
                raise ValueError("bounded counters and currency map required")
            for meter, value in counters.items():
                text(meter)
                integer(value)
            for currency, amount in money.items():
                if len(text(currency)) != 3 or not currency.isascii() or not currency.isupper() or not currency.isalpha():
                    raise ValueError("three-letter uppercase currency required")
                if not isinstance(amount, dict) or set(amount) != {"scale", "amount"}:
                    raise ValueError("currency requires exact scale and amount")
                integer(amount["scale"], 18)
                integer(amount["amount"])
            if key in records:
                raise ValueError("duplicate tenant/attempt identity")
            if len(records) >= MAX_RECORDS:
                raise ValueError("evidence exceeds 100000 attempts")
            records[key] = record
    if not records:
        raise ValueError("nonempty evidence required")
    return records


def compare(quota, provider, tolerance=0):
    integer(tolerance)
    differences = []
    missing = 0
    for key in sorted(quota.keys() | provider.keys()):
        left, right = quota.get(key), provider.get(key)
        if left is None or right is None:
            missing += 1
            differences.append({"tenant_id": key[0], "attempt_id": key[1], "field": "attempt", "reason": "missing_quota" if left is None else "missing_provider"})
            continue
        for field in ("model_version", "counters"):
            if left[field] != right[field]:
                differences.append({"tenant_id": key[0], "attempt_id": key[1], "field": field, "reason": "mismatch"})
        a, b = left["money"], right["money"]
        if a.keys() != b.keys():
            differences.append({"tenant_id": key[0], "attempt_id": key[1], "field": "money", "reason": "currency_mismatch"})
        for currency in a.keys() & b.keys():
            if a[currency]["scale"] != b[currency]["scale"]:
                differences.append({"tenant_id": key[0], "attempt_id": key[1], "field": currency, "reason": "scale_mismatch"})
            elif abs(a[currency]["amount"] - b[currency]["amount"]) > tolerance:
                differences.append({"tenant_id": key[0], "attempt_id": key[1], "field": currency, "reason": "amount_mismatch"})
    return {"version": "v1", "matched": not differences, "quota_attempts": len(quota), "provider_attempts": len(provider), "missing_attempts": missing, "difference_count": len(differences), "differences": differences[:100], "differences_truncated": len(differences) > 100, "money_tolerance_scaled_units": tolerance}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--quota", type=Path, required=True)
    parser.add_argument("--provider", type=Path, required=True)
    parser.add_argument("--money-tolerance", type=int, default=0, help="absolute per-currency difference in the declared scale; default exact")
    args = parser.parse_args(argv)
    try:
        report = compare(load(args.quota), load(args.provider), args.money_tolerance)
    except (OSError, UnicodeError, ValueError, KeyError, TypeError):
        print(json.dumps({"version": "v1", "matched": False, "error": "invalid_evidence"}))
        return 2
    print(json.dumps(report, sort_keys=True))
    return 0 if report["matched"] else 1


if __name__ == "__main__":
    sys.exit(main())

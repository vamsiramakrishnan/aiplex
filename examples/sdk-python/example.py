"""Use the AIPlex Python SDK against `aiplex dev`.

The SDK does not wrap model calls, so this script sends no chat request.
It reads the access point, lists usage records and the usage configuration,
and makes one administrative round trip: create a hook policy, dry-run a
sample through it, then delete it. The policy id is reserved for this
example: an earlier copy of it is deleted before the run, and the policy is
deleted again in a finally block even when a later step fails.

Needs AIPLEX_URL and AIPLEX_TOKEN from: eval "$(aiplex dev env)"
"""

from __future__ import annotations

import sys

from aiplex import (
    AiplexError,
    Client,
    Hook,
    HookPolicy,
    HookPolicyTest,
    HookScope,
    RedactConfig,
    RedactRule,
    UsageFilter,
)

POLICY_ID = "sdk-python-pii"


def check(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def delete_example_policy(client: Client) -> None:
    """Delete the example's own policy if one exists. Other policies are never touched."""
    for rec in client.list_hook_policies():
        if rec.id == POLICY_ID:
            client.delete_hook_policy(POLICY_ID, rec.revision)
            print(f"removed hook policy {POLICY_ID}")


def main() -> None:
    # Reads AIPLEX_URL and AIPLEX_TOKEN. The token is never printed.
    with Client() as client:
        ap = client.get_access_point("local")
        print(f"access point {ap.id} (revision {ap.revision}), callers: {', '.join(ap.spec.caller_kinds)}")

        page = client.list_usage_records(UsageFilter(access_point_id="local"), limit=5)
        print(f"usage records on local: {len(page.records)} shown")
        for record in page.records:
            # The SDK sets Cost.amount (a Decimal) from the server's amount_decimal.
            # It is None when the cost is unknown; print that as "unknown", never 0.
            amount = record.cost.amount if record.cost is not None else None
            cost = "unknown" if amount is None else str(amount)
            print(f"  {record.id} {record.model_id or 'unknown'} {record.status} cost={cost}")

        summary = client.usage_summary(group_by=["model_id"])
        scanned = "unknown" if summary.records_scanned is None else summary.records_scanned
        print(f"usage summary: {scanned} records scanned")

        cfg = client.get_usage_config()
        feedback = "unknown" if cfg.feedback is None else cfg.feedback
        # tag_keys_allowed is read from the raw response: the SDK turns a missing list into [],
        # which would read as "no tag keys allowed" rather than "the server did not say".
        tag_keys = cfg.raw.get("tag_keys_allowed")
        if isinstance(tag_keys, list):
            tags = ", ".join(tag_keys) if tag_keys else "(none)"
        else:
            tags = "unknown"
        print(f"usage config: feedback={feedback} tag keys={tags}")

        policy = HookPolicy(
            id=POLICY_ID,
            description="SDK example: mask email addresses (deleted at the end)",
            scope=HookScope(kind="tenant"),
            hooks=[
                Hook(
                    id="mask",
                    type="redact",
                    phases=["request"],
                    redact=RedactConfig(rules=[RedactRule(id="email", builtin="email", action="mask")]),
                )
            ],
        )
        delete_example_policy(client)
        try:
            # revision=0 asks the server to create. The server refuses (409) if the id exists,
            # so a policy is never overwritten silently.
            rec = client.put_hook_policy(policy, revision=0)
            print(f"created hook policy {rec.id} (revision {rec.revision})")

            result = client.test_hook_policy(
                POLICY_ID, HookPolicyTest(texts=["mail jane@example.com"], phase="request")
            )
            print(f"dry run: decision={result.decision} texts={result.texts}")
            # The decision word is usageledger.HookTransform ("transform", pkg/usageledger/hooks.go:9).
            # The mask text is the built-in email placeholder "[EMAIL]" (pkg/hooks/redact.go:44).
            check(result.decision == "transform", f"dry run decision was {result.decision!r}, expected 'transform'")
            check(result.texts == ["mail [EMAIL]"], f"dry run texts were {result.texts!r}, expected masked text")
        finally:
            delete_example_policy(client)
    print("sdk-python: all checks passed")


if __name__ == "__main__":
    try:
        main()
    except AiplexError as err:
        print(f"sdk-python failed (HTTP {err.status_code}, {err.code}): {err}", file=sys.stderr)
        sys.exit(1)
    except RuntimeError as err:
        print(f"sdk-python failed: {err}", file=sys.stderr)
        sys.exit(1)

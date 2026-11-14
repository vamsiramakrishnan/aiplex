// Use the AIPlex TypeScript SDK (@aiplex/sdk) against `aiplex dev`.
//
// The SDK does not wrap model calls, so this script sends no chat request.
// It reads the access point, lists usage records and the usage configuration,
// and makes one administrative round trip: create a hook policy, dry-run a
// sample through it, then delete it. The policy id is reserved for this
// example: an earlier copy is deleted before the run, and the policy is
// deleted again in a finally block even when a later step fails.
//
// Needs AIPLEX_URL and AIPLEX_TOKEN from: eval "$(aiplex dev env)"

import { AiplexClient, AiplexError } from "@aiplex/sdk";

const POLICY_ID = "sdk-typescript-pii";

function check(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

// Deletes the example's own policy if one exists. Other policies are never touched.
async function deleteExamplePolicy(client) {
  for (const rec of await client.listHookPolicies()) {
    if (rec.id === POLICY_ID) {
      await client.deleteHookPolicy(POLICY_ID, rec.revision);
      console.log(`removed hook policy ${POLICY_ID}`);
    }
  }
}

async function main() {
  // Reads AIPLEX_URL and AIPLEX_TOKEN when the options are omitted. The token is never printed.
  const client = new AiplexClient();

  const ap = await client.getAccessPoint("local");
  console.log(`access point ${ap.id} (revision ${ap.revision}), callers: ${ap.spec.callerKinds.join(", ")}`);

  const page = await client.listUsageRecords({ accessPointId: "local" }, { limit: 5 });
  console.log(`usage records on local: ${page.records.length} shown`);
  for (const record of page.records) {
    // The SDK sets cost.amountDecimal from the server's amount_decimal. It is null when the
    // cost is unknown; print that as "unknown", never 0.
    const amount = record.cost?.amountDecimal ?? null;
    console.log(`  ${record.id} ${record.modelId ?? "unknown"} ${record.status} cost=${amount ?? "unknown"}`);
  }

  const summary = await client.usageSummary(undefined, { groupBy: ["model_id"] });
  console.log(`usage summary: ${summary.recordsScanned ?? "unknown"} records scanned`);

  const cfg = await client.getUsageConfig();
  // tagKeysAllowed is [] when the server sends no list, which would read as "none allowed".
  // Read the raw response to tell a missing list apart from an empty one.
  const tagKeys = cfg.raw.tag_keys_allowed;
  let tags = "unknown";
  if (Array.isArray(tagKeys)) {
    tags = tagKeys.length > 0 ? tagKeys.join(", ") : "(none)";
  }
  console.log(`usage config: feedback=${cfg.feedback} tag keys=${tags}`);

  const policy = {
    id: POLICY_ID,
    description: "SDK example: mask email addresses (deleted at the end)",
    scope: { kind: "tenant" },
    hooks: [
      {
        id: "mask",
        type: "redact",
        phases: ["request"],
        redact: { rules: [{ id: "email", builtin: "email", action: "mask" }] },
      },
    ],
  };

  await deleteExamplePolicy(client);
  try {
    // Revision 0 asks the server to create. The server refuses (409) if the id exists,
    // so a policy is never overwritten silently.
    const created = await client.putHookPolicy(policy, 0);
    console.log(`created hook policy ${created.id} (revision ${created.revision})`);

    const result = await client.testHookPolicy(POLICY_ID, {
      texts: ["mail jane@example.com"],
      phase: "request",
    });
    console.log(`dry run: decision=${result.decision} texts=${JSON.stringify(result.texts)}`);
    // The decision word is usageledger.HookTransform ("transform", pkg/usageledger/hooks.go:9).
    // The mask text is the built-in email placeholder "[EMAIL]" (pkg/hooks/redact.go:44).
    check(result.decision === "transform", `dry run decision was ${result.decision}, expected transform`);
    check(
      JSON.stringify(result.texts) === JSON.stringify(["mail [EMAIL]"]),
      `dry run texts were ${JSON.stringify(result.texts)}, expected the masked text`,
    );
  } finally {
    await deleteExamplePolicy(client);
  }

  console.log("sdk-typescript: all checks passed");
}

main().catch((err) => {
  if (err instanceof AiplexError) {
    console.error(`sdk-typescript failed (HTTP ${err.statusCode}, ${err.code}): ${err.message}`);
  } else {
    console.error(`sdk-typescript failed: ${err.message}`);
  }
  process.exit(1);
});

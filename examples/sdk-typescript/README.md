# TypeScript SDK

Uses the `@aiplex/sdk` client (`sdk/typescript`) against `aiplex dev`. The SDK does not wrap model calls, so [example.mjs](example.mjs) sends no chat request. It reads the `local` access point, lists its usage records, the usage summary and the usage configuration, then creates a hook policy, dry-runs a sample through it and deletes the policy. The policy is deleted even when a later step fails. The id `sdk-typescript-pii` is reserved for this example: a policy with that id left by an earlier run is removed before the run starts (other policies are never touched). Creation uses revision 0, so the server refuses rather than overwrite an existing policy.

The package is not on npm. Build it and install it from the repository (the test script does this in a temporary directory):

```bash
(cd sdk/typescript && npm ci && npm run build)
mkdir my-app && cd my-app && npm init -y && npm install /path/to/repo/sdk/typescript
cp /path/to/repo/examples/sdk-typescript/example.mjs .
eval "$(aiplex dev env)"        # sets AIPLEX_URL and AIPLEX_TOKEN
node example.mjs
```

Expected last line: `sdk-typescript: all checks passed`.

`make examples-test` skips this example unless `EXAMPLES_INSTALL_SDKS=1` is set, then builds the SDK from the checkout and runs it.

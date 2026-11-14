# ClientPlex

ClientPlex owns developer-client registrations and configuration planning.
`Registry.Register(Profile, Renderer)` adds trusted host-owned client adapters per
composition. Duplicate profiles fail, metadata is copied, and no registry API
loads code from remote metadata. API hosts can select a registry with
`ClientHandler.WithProfiles`; built-in registration remains the default. It is
separate from A2APlex agent execution, LLMPlex model operations, and identity
verification. Profiles describe client-native protocols; registrations bind a
profile to a principal, a cloud project or a tenant-owned destination reference.

## API and persistence

- `GET /api/v1/clients/profiles`
- `GET /api/v1/clients/registrations`
- `GET|PUT|DELETE /api/v1/clients/registrations/{id}`
- `POST /api/v1/clients/registrations/{id}/plan`

The module stores `client-registration` records through `pkg/resources.Store`.
Tenant selection comes from the host's verified context, never the request body.
Optional `network_profile_ref` must resolve to a network profile in that same
tenant at registration and again when planning. A deleted profile blocks plans.
PUT creation uses revision zero; updates use `If-Match: "<revision>"`. DELETE
requires the current revision. Configuration plans resolve destination references
in that same tenant, validate protocol compatibility and render fragments. They
do not execute commands, fetch credentials or write workstation files. Secret
references and target environment names are returned separately from artifacts.
The CLI's separate, opt-in `aiplex connect --write` is what edits a client's config
file ([harness onboarding](harness-onboarding.md)); ClientPlex plans never do.

The Console provides profile discovery, revision-aware registration editing and
plan review. The SDK and `aiplex clients` commands use the same API.

## Client contracts

An Anthropic Messages destination can expose an installed native Messages
operation or explicit Vertex rawPredict operations. The native operation keeps
its body/path/headers; Vertex translation validates the original JSON before
re-encoding it. Protocol compatibility checks include transport, action and
resource ownership semantics, not just an operation name.

Console plan/save responses stay bound to their original review/editor intent.
A late result cannot replace a newer plan or reopen a discarded editor. Completed
mutations still refresh the saved registration inventory.

| Profile | Direct Google Cloud | Gateway |
|---|---|---|
| Antigravity App/CLI | ADC or enterprise workforce sign-in | GenAI or Chat Completions |
| Antigravity ACP | Enterprise client flow | GenAI only |
| Codex | Built-in `gcp` provider, project/location, optional ADC file | Responses-compatible endpoint |
| Claude Code | Vertex provider configuration with ADC | Anthropic Messages, Vertex rawPredict, or native Claude apps gateway |
| Gemini CLI | Not offered | Google GenAI, gateway key only |
| Claude Desktop | Native Claude apps gateway bootstrap | Native gateway only |

Codex's GCP provider and custom authentication are documented in
[official OpenAI configuration docs](https://learn.chatgpt.com/docs/config-file/config-advanced).
A configurable model name does not prove launch availability or project access.
Product availability announcements do not change behaviour: only the documented, configured operations are used.

Antigravity gateway mode uses a centralized service account and consumption
billing, separately from seat-based included quotas. Plans preserve the required
attribution headers and identify streaming requirements. Direct enterprise plans
include global `businessaicode.googleapis.com` and `cloudcode-pa.googleapis.com`
client-service dependencies even when inference is regional. These are not
invented model inference APIs. See [enterprise setup](https://antigravity.google/docs/enterprise/)
and [gateway deployment](https://docs.cloud.google.com/gemini/enterprise/docs/ai-developer-tools/deploy-enterprise-gateway).

Claude Code's generic gateway [protocol guide](https://code.claude.com/docs/en/llm-gateway-protocol)
requires provider-native paths, streaming events and beta/version headers.
[Claude apps gateway](https://code.claude.com/docs/en/claude-apps-gateway) has its
own sign-in and bootstrap protocol. CLI managed login settings and Desktop's
`bootstrapUrl` are rendered separately. Browser sign-in is required; unattended
CI should use provider credentials directly.

## One source of truth with `connect --write`

The Claude Code (Anthropic Messages gateway) and Gemini CLI settings fragments
are rendered by `internal/harness/fragment`, the same code `aiplex connect --write`
uses to edit those files. A plan and a written file for the same client and
inputs hold the same keys in the same order; tests compare the bytes. The
package depends on nothing else in the repository, so both can import it.
Codex, Antigravity and Claude Desktop fragments are still rendered by ClientPlex
alone and are not covered by that guarantee.

- **Claude Code token helper.** `auth_mode: token-helper` is accepted for the
  Messages protocol and renders `apiKeyHelper` as one POSIX-quoted command line
  built from `token_helper.command` (an absolute path) and `args`. The plan
  carries no `ANTHROPIC_AUTH_TOKEN` binding, because that variable would take
  precedence over the helper. `apiKeyHelper` has no timeout or refresh setting,
  and the helper supplies the credential, so `timeout_ms`, `refresh_interval_ms`
  and `credential_ref` are refused for this combination rather than ignored.
  `aiplex` or the helper executable must exist on the workstation; ClientPlex
  does not run or check it. The Vertex rawPredict protocol does not use a
  helper and refuses `token-helper`.
- **Gemini CLI.** Gateway mode with Google GenAI and a gateway key. The plan has
  the `~/.gemini/settings.json` fragment (API-key auth method and model name),
  the `GOOGLE_GEMINI_BASE_URL` environment line, and a credential binding from
  `credential_ref` to `GEMINI_API_KEY`. When `credential_env` names another
  variable, the plan adds a `workstation shell` line copying it into
  `GEMINI_API_KEY`, because Gemini CLI reads only that one. Only
  `auth_mode: gateway-key` is accepted; every other value is refused with a
  message naming `auth_mode` (tested for every value). Gemini CLI has no credential helper setting, so
  `token-helper` is refused with that reason. `managed` is refused because
  nothing is rendered for it. The settings fragment was observed working with
  Gemini CLI 0.67 through `connect --write`; a ClientPlex plan has not been
  validated against a running client.
- **`platform` is removed.** It was validated and never read: `connect --write`
  does not vary what it writes by operating system, and plan targets are
  display paths. A registration stored with `platform` still reads, lists and
  plans (the stored value is ignored). A PUT whose body carries `platform` is
  refused with `400 INVALID_REGISTRATION` and a message saying it is no longer a
  registration field and must be removed (every other unknown member keeps the
  decoder's generic message), so a document read back from the API must drop
  `platform` before it is written again; the replacement then no longer holds
  it. This holds for the in-memory store and
  for Firestore, which keep the registration as an opaque JSON string; it was
  tested against the in-memory store only.
- **No field is accepted and then silently dropped.** One rule applies to every
  profile, for the Antigravity profiles too:
  - A field the plan uses is accepted (for example `managed` moves the Claude
    Code and Antigravity settings fragment to the managed-settings target, and
    in Claude Code's native apps-gateway mode it also adds
    `parentSettingsBehavior: merge` and drops the user-file version
    requirement).
  - A field that promises control over the client, where the plan would not
    deliver it and nothing else enforces it, is refused when written (`400`,
    naming every refused field and what to remove), and a stored record that carries it
    reads but gets no plan (`409`, the same message): `allowed_models` in direct
    mode, because the client calls Google Cloud itself and no gateway restricts
    its models, and `managed` for Codex, Gemini CLI and Antigravity direct mode,
    which have no managed configuration the plan renders.
  - Any other field with no effect in that profile and mode is accepted and
    named in one plan warning each: for example `project_id` and `location` on a
    non-Vertex gateway, `credential_env` and `credential_ref` in direct mode,
    `model_id` and the credential and project fields in the native apps-gateway
    mode, `credential_env` on a Codex token helper. A registration valid when
    stored therefore keeps its plan.
  - `managed` on Claude Desktop and Antigravity ACP is accepted without a
    warning: their plans are already the managed configuration.
  - The Gemini CLI profile and the Claude Code token helper are new, so no
    stored registration can carry their unused fields; those stay strict
    (`project_id`, `location`, and for the Claude Code helper `credential_env`
    and `credential_ref`).
- **`allowed_models` is not enforced by the client configuration.** In a gateway
  mode it is only checked against the destination's declared model ids when the
  registration is saved or planned, and the plan says so. What the client may
  actually use is decided by the destination's own model list: an Aiplex access
  point answers `404 MODEL_NOT_FOUND` for a model outside its list
  (`internal/api/access_point_models.go`, the unknown-model branch after the
  lookup in `ap.Models`), and a model surface refuses one outside its allowlist
  (`pkg/modelapi/gateway.go`, `allowedModel`, `403 model is not allowed`). A
  destination that is some other gateway decides for itself; Aiplex cannot say.
  The registration's list is read nowhere at request time
  (`internal/api/access_point_caller.go` only looks the registration up to
  attribute the client). Antigravity renders the list into the client's model
  list, so it is used there. The Overview connect page does not pass the list for
  the other profiles, so its notes do not carry the warning.
- **`credential_env`.** For Claude Code, Gemini CLI and Antigravity gateway mode
  it is where the launcher already holds the credential. Those clients read one
  fixed variable, so when the names differ the plan binds `credential_ref` to
  `credential_env` and adds a `workstation shell` line that copies it into
  `ANTHROPIC_AUTH_TOKEN`, `GEMINI_API_KEY` or `AGY_LLM_GATEWAY_API_KEY`, the same step `aiplex connect` prints. A launcher that
  follows the binding and runs the line ends up with the credential.
- **Upgrade.** Stored registrations are not rewritten and always read. Their
  plans gain the warnings above; one that carries `allowed_models` in direct
  mode, or `managed` where it is refused, returns `409 CLIENT_PLAN_UNAVAILABLE`
  until the field is removed with a `PUT` at the current revision. A document
  read back from the API that still holds `platform` must drop that member
  first; the `400` says so.

Boundaries are unchanged: ClientPlex writes no workstation files, installs
nothing, issues no credentials and never runs a helper. Every plan is
`configured-unverified`, and plans hold references, never secret values.

## Claude apps gateway server artifacts

A native gateway registration can include a complete `gateway_server` spec.
`RenderClaudeGateway` then renders `gateway.yaml`, the command arguments, secret
bindings and network requirements for the separately supplied Claude binary.
The renderer requires a pinned corporate OIDC issuer, group claim and scopes,
distinct environment references for secrets, a private ingress allowlist,
PostgreSQL hostname, explicit model mappings, group rules and HTTPS OTLP collector.
The Vertex upstream uses ADC. Metrics are enabled; logs/traces require explicit
opt-in. Partial configurations produce errors rather than runnable-looking YAML.

The server artifact follows [Anthropic's gateway config](https://code.claude.com/docs/en/claude-apps-gateway-config)
and [Google Cloud deployment example](https://code.claude.com/docs/en/claude-apps-gateway-on-gcp).
Aiplex does not distribute the licensed binary or emulate its sign-in service.
Actual installed-version schema, group matching, IAM and model access must be
verified before deployment. Google Workspace ID tokens do not carry groups;
organizations must supply a reviewed directory integration when using that IdP.

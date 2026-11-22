# Naming and glossary

## What "plex" means

AIPlex groups its capabilities into modules. Start with the thing you want to use. The CLI and API use the names in the last two columns.

| Work with | Module | What you can do | CLI | Managed API |
| --- | --- | --- | --- | --- |
| Agents | **A2APlex** | Invoke a registered cloud agent or delegate work over the Agent2Agent protocol (A2A). | `aiplex a2a` | `/api/v1/a2a` |
| Models | **LLMPlex** | Offer model endpoints across providers and forward requests in the provider's native protocol. | `aiplex llm` | `/api/v1/llm` |
| Tools | **MCPlex** | Expose selected tools from MCP servers at one access point and control who can call them. | `aiplex mcp`, `aiplex tools`, `aiplex tool-auth` | `/api/v1/mcp` |
| Harnesses and other clients | **ClientPlex** | Choose a profile, register a client and review its configuration. Use `connect` to point an installed harness at an access point. | `aiplex clients`, `aiplex connect` | `/api/v1/clients` |
| Skills | **SkillsPlex** | List deployed skills and inspect their invocation history. | `aiplex skills` | `/api/v1/skills` |
| Budgets and limits | **QuotaPlex** | Admit a supported managed call against its limits and settle it from usage evidence. | `aiplex budgets`, `aiplex quota` | `/api/v1/budgets`, `/api/v1/quota` |

Run `aiplex modules list` to check which modules your API enables. Code and Console filters also use **plane** for the module handling a request. Identity, access points and the usage ledger are shared across modules.

## Glossary

### Entry points

| Term | Meaning |
| --- | --- |
| **Access point** | A named, stable URL (`/ap/{id}/...`) in front of a chosen set of models, tools and skills for a chosen audience. It admits callers by kind and identity before any model or tool work happens. Specified in [access points](access-points.md). |
| **Managed path** | A request served by the AIPlex API: access points (`/ap/{id}/...`) and the managed endpoints under `/api/v1/...`. The caller is verified, admitted, optionally checked against quota, and the call is written to the usage ledger. |
| **Gateway path** | A request routed by Envoy AI Gateway (`/llm/*`, `/mcp/*`, `/a2a/*`) and authorized by `aiplex-authz`. It is a separate path: the managed path does not admit or meter it. In these docs "the gateway" means Envoy unless a sentence says otherwise. |
| **Managed exchange** | One request and its response on the managed path. It is the unit the usage ledger records. |

### Who is calling

| Term | Meaning |
| --- | --- |
| **Caller** | Whoever presents a credential. Every caller is exactly one kind: agent, client or user. The kind comes from the verified token and cannot be set by the request. See [callers](https://vamsiramakrishnan.github.io/aiplex/docs/concepts/callers). |
| **Agent** | A registered workload, or an agent acting for a user. |
| **User** | A person who signed in through an identity provider. |
| **Client** | In access-point policy, a caller with a verified authorized party (`azp`) matching a ClientPlex registration. A workload or delegated actor keeps the `agent` kind and can also carry client attribution. |
| **Harness** | The program that runs a coding agent’s work session. Claude Code, Codex and Gemini CLI are harnesses. A harness can use a developer’s token to call models and tools. Choosing `--client codex` selects configuration for Codex. The token determines the caller kind. |
| **Principal** | In a delegated call, the user the call is made on behalf of. |
| **Actor** | In a delegated call, the agent doing the calling (the token's `act` claim). Usage and audit records keep both principal and actor. |

### What is being called

| Term | Meaning |
| --- | --- |
| **Provider** | Unqualified, a model vendor or endpoint family (OpenAI, Anthropic, Bedrock, ...). Other uses are always qualified: *identity provider*, *deployment provider*. |
| **Wire** | The request and response protocol a caller's SDK speaks, such as OpenAI Chat Completions, Anthropic Messages or Gemini generateContent. AIPlex forwards in the provider's native wire. |
| **Surface** | A saved model configuration in LLMPlex: an operator-approved provider origin, a credential reference, and an allowlist of operations and models. It is the model-side counterpart of a connection. See [model surfaces](model-surfaces.md). |
| **Connection** | A registered upstream that is not a model: an MCP tool server (*tool connection*) or a cloud agent (*agent connection*). `aiplex connect` is unrelated; it prints client configuration. |
| **Route** | Unqualified, a model routing rule in LLMPlex. Envoy routes and API routes (as in "route policy") are always qualified. |
| **Profile** | A *client profile* defines the protocols and configuration a client supports. A *metering profile* defines which usage a gateway can measure and enforce. |

### What is recorded and limited

| Term | Meaning |
| --- | --- |
| **Usage ledger** | The record of every managed exchange: caller, resource, counts and cost. See [the usage ledger](usage-ledger.md). |
| **Unknown** | A count, price or cost AIPlex could not establish. It is shown as unknown, never as zero. |
| **Estimated** | A cost computed from a price you set or imported, as opposed to one a provider reported. |
| **Reported** | A ledger record that a harness or agent sent about its own work, as opposed to one AIPlex observed on the managed path. A report is not proof the work was authorized. |
| **Settlement** | Recording what a request actually used against the budget that admitted it, after the response. |
| **Admission** | The decision to let a request proceed: caller kind and identity at the access point, then quota when QuotaPlex is enabled. |
| **Scope** | A permission string in the caller's token, such as `llm:model:{model_id}` or `mcp:tools:{tool_name}`. |

### Words from Branchyard

[Branchyard](branchyard.md) is a separate product that can use an access point as its model backend. Its words appear in that guide, in the `branchyard` federation preset and in usage tags. They keep Branchyard's meaning, and are written so they cannot be taken for AIPlex's.

| Term | Meaning |
| --- | --- |
| **Branchyard** | A separate product that runs several coding agents at once, each on its own git branch. It is not a plex and not part of AIPlex. |
| **Branch**, **turn** | In Branchyard, one coding agent's working copy and git branch, and one round of work on it. AIPlex sees them only as the tags `by_branch` and `by_turn` and as claims in a Branchyard token. |
| **Model gateway** (Branchyard's) | The loopback proxy Branchyard runs for each turn, which the harness calls instead of the provider. Written "Branchyard's gateway" or "the model gateway"; "the gateway" alone still means Envoy. |
| **Backend** (Branchyard's) | A named upstream of that model gateway in `branchyard.toml`. An access point is used as one. Unrelated to the backends of an LLMPlex route. |
| **Branchyard tenant** | The tenant inside a Branchyard server: its `by_tenant` claim and the `tenant` input of the `branchyard` preset. Not AIPlex's tenant, which comes from the verified identity. |
| **Turn token** | A short-lived token a Branchyard server signs for one turn. The one it presents to AIPlex carries `by_purpose = "aiplex"`. |

## Names that are easy to confuse

| These look alike | The difference |
| --- | --- |
| `aiplex who` and `aiplex whoami` | `who` lists every caller the API knows. `whoami` shows the identity this CLI is using. |
| `aiplex tools` and `aiplex tool-auth` | `tools` is for operators: a tool connection's credential type and which tools it allows. `tool-auth` is for a user: sign yourself in to a downstream tool. |
| `aiplex health`, `aiplex doctor`, `aiplex status` | `health` asks whether the API and its components answer. `doctor` checks your local setup and says what to fix. `status` summarises a deployed platform. |
| `aiplex connect` and "connection" | `connect` prints or writes client configuration for an access point. A connection is an upstream tool server or agent. |
| `aiplex connect --client branchyard` and a client | This target configures Branchyard’s model gateway to use an access point. Branchyard runs harnesses. Generating its configuration requires no ClientPlex registration. |
| `aiplex identity` and `aiplex agents identity` | The first configures how people sign in. The second binds an agent to its cloud workload identity. |
| `aiplex init` and `aiplex quickstart` | Both set up a cloud deployment: `init` is guided, `quickstart` runs end to end. Neither is the local path; that is `aiplex dev`. |

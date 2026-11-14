# Naming and glossary

This page says what the names in AIPlex mean, and which command, URL and package each one corresponds to. Where a word is used for more than one thing, the page says so instead of hiding it.

## What "plex" means

A **plex** is a module that puts **many of one kind of thing behind one front**, as in *multiplex*: many signals, one line. You register many providers, servers or agents once; callers see a single, stable interface, and the plex applies the same identity check, policy and usage record to all of them.

LLMPlex, MCPlex, A2APlex and SkillsPlex each put many upstreams behind one endpoint. ClientPlex and QuotaPlex turn many client registrations, or many budgets and limits, into one configuration or one decision. **AIPlex** is those plexes plus the parts they share: identity, access points and the usage ledger.

| Plex | Many of what | One what in front | CLI group | Managed API prefix |
| --- | --- | --- | --- | --- |
| **LLMPlex** | Models, across providers and wire protocols | One model endpoint per access point; requests are forwarded in the provider's native protocol | `aiplex llm` | `/api/v1/llm` |
| **MCPlex** | MCP (Model Context Protocol) tool servers | One MCP endpoint per access point (`/ap/{id}/mcp`), with upstream authentication and tool policy | `aiplex mcp`, `aiplex tools`, `aiplex tool-auth` | `/api/v1/mcp` |
| **A2APlex** | Agents: deployed agents that speak A2A (the Agent2Agent protocol), and native cloud agents | One way to invoke them, and one delegation history | `aiplex a2a` | `/api/v1/a2a` |
| **SkillsPlex** | Deployed skills | One list of skills and one invocation history | `aiplex skills` | `/api/v1/skills` |
| **ClientPlex** | Developer clients (IDEs and coding CLIs) | One reviewed configuration per client | `aiplex clients`, `aiplex connect` | `/api/v1/clients` |
| **QuotaPlex** (opt-in) | Budgets, allocations and limits | One admission decision and one settlement per request | `aiplex quota`, `aiplex budgets` | `/api/v1/quota` |

Things to know about the pattern:

- **The plain word is the one you type.** CLI groups, API prefixes and scopes use the plain or protocol name (`llm`, `mcp`, `a2a`, `skills`, `clients`, `quota`). The Plex name is the module's name in the docs and the Console. `aiplex modules` lists the modules an API has enabled.
- **MCPlex** is MCP + plex with the shared P written once. It is not spelled "MCPPlex".
- **LLMPlex, MCPlex and A2APlex are named after what they speak** (model APIs, MCP, A2A); SkillsPlex, ClientPlex and QuotaPlex are named after what they hold.
- **ClientPlex and QuotaPlex stretch the definition.** ClientPlex does not sit in front of clients; it holds many client registrations and produces one reviewed configuration for each. QuotaPlex holds no upstreams; it turns many budgets and limits into one decision. Both keep the "many of one, one front" shape.
- **Not everything is a plex.** Identity, networking, access points and the usage ledger are shared parts that every plex uses, and keep plain names.
- In Go code and Console filters a plex is sometimes called a **plane** (`models.PlaneLLMPlex`, "by plane"). Plane and plex mean the same module there.

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
| **Client** | A registered developer client such as Claude Code, Codex or Antigravity (a ClientPlex registration). Not the same as an *OAuth client*, which is a credential type a tool connection can use, or an HTTP client library. |
| **Harness** | A coding agent's host program (Claude Code, Codex, Gemini CLI). Used when talking about configuring one or receiving its telemetry; a registered harness is a client. |
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
| **Profile** | Either a *client profile* (a supported developer client template) or a *metering profile* (how usage is read from one provider format). Always qualified. |

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
| `aiplex connect --client branchyard` and a client | The flag's other values are developer clients (ClientPlex registrations). Branchyard is not one: its model gateway calls the access point with a token, and no client registration is involved. |
| `aiplex identity` and `aiplex agents identity` | The first configures how people sign in. The second binds an agent to its cloud workload identity. |
| `aiplex init` and `aiplex quickstart` | Both set up a cloud deployment: `init` is guided, `quickstart` runs end to end. Neither is the local path; that is `aiplex dev`. |

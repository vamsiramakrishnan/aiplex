# AIPlex

AIPlex is a gateway for models, tools and agents. This extension adds the `aiplex` MCP server, which has read-only tools: `whoami`, `list_access_points`, `get_connect_config`, `list_models`, `usage_summary` and `usage_records`. They use the signed-in user's own token and change nothing.

## Point Gemini CLI at an access point

Gemini CLI has no settings-file key for the API base URL, so part of this is shell environment. Let the CLI write what it can:

```bash
aiplex connect local --client gemini-cli --write --dry-run
aiplex connect local --client gemini-cli --write
```

It shows a diff, backs up `~/.gemini/settings.json` first (undo with `aiplex connect --undo --client gemini-cli`) and adds the access point's MCP server. It prints the rest as "Still to do by hand": set `GOOGLE_GEMINI_BASE_URL` to the access point's genai base URL and `GEMINI_API_KEY` to the access token. Never put the token in a file.

Local, no cloud account: `aiplex dev --detach`, then `eval "$(aiplex dev env)"`; the access point is `local`. For a remote AIPlex use `aiplex login`.

## Answering questions

- What did I spend: `usage_summary` with `group_by` `["model"]`. A missing count or cost is unknown, never zero.
- Why was I denied: `usage_records` with `status` `denied` or `error`, then read `error_class`; compare with `whoami` roles and scopes.
- What can I use: `list_access_points`, then `list_models` for one.

Do not ask the user to paste a token into the conversation.

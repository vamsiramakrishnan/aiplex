# Request hooks

Inspect, mask or refuse prompts, responses and tool calls before they reach
a model or a tool server. See [docs/request-hooks.md](../../docs/request-hooks.md).

| File | What |
| --- | --- |
| `policy.yaml` | Tenant policy: mask email addresses, refuse private key blocks |
| `hooks.sh` | Applies `policy.yaml`, sends a masked and a refused prompt through `aiplex dev`, shows the ledger |
| `sidecar/` | Reference sidecar for the AIPlex hook contract (Go standard library) |
| `sidecar-policy.yaml` | Access-point policy that calls the sidecar on requests and responses |

```bash
aiplex dev                      # terminal 1
eval "$(aiplex dev env)"        # terminal 2
bash examples/hooks/hooks.sh
```

With the sidecar:

```bash
go run ./examples/hooks/sidecar -addr 127.0.0.1:5005 &
aiplex dev --allow-origin http://127.0.0.1:5005
aiplex hooks policies set -f examples/hooks/sidecar-policy.yaml
curl -sS "$AIPLEX_URL/ap/local/v1/chat/completions" -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Ticket for EMP-123456"}]}'
```

Redaction rules are pattern matching. They find text shaped like an email
address or a key; they do not guarantee that personal data or secrets are
removed.

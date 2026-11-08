# Public wire contracts

The self-contained JSON Schema Draft 2020-12 documents in `v1/` describe the
public Go types in `pkg/contracts` and the descriptor in `pkg/modules`. Services
in any language can implement the same JSON wire contract. The `$id` values are
schema identifiers; validation does not require fetching a remote schema.

| Schema | Public type |
| --- | --- |
| `v1/request-context.schema.json` | `contracts.RequestContext` |
| `v1/membership-snapshot.schema.json` | `contracts.MembershipSnapshot` |
| `v1/module-descriptor.schema.json` | `modules.Descriptor` |

Enable `date-time` format assertions when validating membership snapshots. An
empty `groups` array or `null` both represent no groups, matching Go's empty and
nil slices. Unknown fields are rejected for these versioned wire objects.

Schema validation checks structure. Runtime validation additionally enforces
tenant/principal/reference binding, timestamp validity relative to server time,
release-version integer bounds, duplicate dependency names, cross-module route
conflicts, dependency compatibility, and cycle detection. Schema validity does
not establish authenticity. Authenticate the sending service before trusting a
request context or membership snapshot.

Optional `config_schema` describes module-owned configuration; it must be a JSON
object. Each module owns validation of that schema and its configuration values.
The host must not publish actual credential values in descriptor metadata.

Verify the schemas, example module descriptor, and rejected malformed examples:

```bash
python3 -m pip install -r schemas/requirements.txt
python3 schemas/check.py
```

The independently deployable example is in `examples/modules/echo` and includes
an HTTP contract test through the public remote-module adapter.

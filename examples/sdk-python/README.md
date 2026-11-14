# Python SDK

Uses the `aiplex` Python client (`sdk/python`) against `aiplex dev`. The SDK does not wrap model calls, so [example.py](example.py) sends no chat request. It reads the `local` access point, lists its usage records, the usage summary and the usage configuration, then creates a hook policy, dry-runs a sample through it and deletes the policy. The policy is deleted even when a later step fails. The id `sdk-python-pii` is reserved for this example: a policy with that id left by an earlier run is removed before the run starts (other policies are never touched). Creation uses revision 0, so the server refuses rather than overwrite an existing policy.

The package is not on PyPI. Install it from the repository (the test script does this in a temporary virtual environment):

```bash
pip install ./sdk/python
eval "$(aiplex dev env)"        # sets AIPLEX_URL and AIPLEX_TOKEN
python3 examples/sdk-python/example.py
```

Expected last line: `sdk-python: all checks passed`.

`make examples-test` skips this example unless `EXAMPLES_INSTALL_SDKS=1` is set, then installs the SDK from the checkout and runs it.

# Third-party notices

AIPlex is licensed under the Apache License 2.0 (see [LICENSE](LICENSE)). This
repository also contains third-party data under its own licence.

## LiteLLM model prices (MIT)

[`pkg/pricecatalog/snapshot.json`](pkg/pricecatalog/snapshot.json) is a trimmed
extract of `model_prices_and_context_window.json` from
[BerriAI/litellm](https://github.com/BerriAI/litellm), pinned to one commit (the
commit and retrieval date are in the file's `meta` object). The file is under the
MIT licence, Copyright (c) 2023 Berri AI; it is not from LiteLLM's `enterprise/`
directory. The licence text and attribution are in
[`pkg/pricecatalog/NOTICE`](pkg/pricecatalog/NOTICE).

The data is used only when an operator imports a price from it with
`aiplex usage prices catalog import`. It is list prices from a third party and
can be stale or wrong.

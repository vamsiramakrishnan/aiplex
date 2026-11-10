# Sidecar adapter payloads

These request and response bodies were written by hand from the projects'
published API documentation and source on 2026-11-10. They were **not**
captured from running Presidio or LLM Guard services.

- `presidio-analyze-*.json`: Presidio Analyzer `POST /analyze`. Request
  fields `text`, `language`, `entities`, `score_threshold`; the response is an
  array of `RecognizerResult` (`entity_type`, `start`, `end`, `score`, and
  optionally `analysis_explanation` / `recognition_metadata`). Offsets index
  the Python string (Unicode code points); this is inferred from the
  implementation language, not stated in the API description.
  Source: microsoft/presidio `docs/api-docs/api-docs.yml`.
- `presidio-anonymize-response.json`: Presidio Anonymizer `POST /anonymize`
  (`text`, `items[]`). Same source.
- `llm-guard-*.json`: LLM Guard API `POST /analyze/prompt` and
  `/analyze/output` (`is_valid`, `scanners` name-to-score map,
  `sanitized_prompt` / `sanitized_output`). Source: protectai/llm-guard
  `llm_guard_api/app/schemas.py` and `app.py`, and the API client docs.

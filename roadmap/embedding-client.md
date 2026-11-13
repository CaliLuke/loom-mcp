# Reusable embedding client

Contract: provide a reusable `runtime/embeddings.Client` and an Ollama implementation for single-text and batch embedding calls. Validate configuration, response cardinality, finite nonzero vectors and consistent dimensions. Honor cancellation and bound responses. Errors must not contain request text, credentials or provider bodies. No application storage, queue, tenant policy, new providers, DSL or agent workflow changes.

Current owners: provider-neutral chat types live in `runtime/agent/model`; this change introduces a separate embedding contract without changing chat. No generated surfaces change.

Steps:
1. Add executable HTTP contract tests for success, invalid configuration and vectors, provider failures, cancellation and response limits.
2. Implement the client and document consumer use.
3. Run package tests, lint, full tests and routine integration checks; obtain independent review before committing.

Acceptance matrix: single and batch requests preserve input order; empty input makes no request; blank/invalid configuration rejects; missing/extra/ragged/zero vectors reject; oversized/malformed response rejects; cancellation propagates; provider failure exposes only status. Consumer tests own durable admission, retries and vector persistence.

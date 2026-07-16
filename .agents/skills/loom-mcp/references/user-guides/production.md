# Production Guide Index

Use the focused production references instead of a duplicated monolithic guide:

- `production/temporal-setup.md` — Temporal workflow recovery, persistent runtime stores, and restart verification
- `production/security-and-runtime.md` — runtime boundaries and deployment safety
- `production/observability.md` — tracing, metrics, logs, and event reliability
- `production/streaming-ui.md` — session-owned stream consumption and UI projection
- `production/model-rate-limiting.md` — local and cluster-aware adaptive limiting
- `production/prompt-overrides.md` — scoped prompt override stores and rollout
- `production/system-reminders.md` — static and dynamic reminder behavior
- `production/index.md` — navigation across these topics

The canonical product contracts remain in `docs/runtime.md`, `docs/dsl.md`, and `docs/mcp_sdk_server.md`. When a focused production guide disagrees with those docs or current source, update the guide and trust the evaluated design/runtime contract.

Before calling production-facing framework work complete, run the verification ladder in the repo-local `loom-mcp` skill. Use the pinned remote Loom mode before CI-facing verification or commits.

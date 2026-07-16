# DeepWiki delta review: cross-cutting documentation and skill routing

Reviewed: 2026-07-16
Current tree: `067b8adee4def7d9f12f1504a5e8497bc329e2f8`
DeepWiki index: `dd837947`

## Scope and method

This is a post-remediation delta review. The complete page-level audit of the
DeepWiki snapshot is retained in
[`../deepwiki-2026-07-15/`](../deepwiki-2026-07-15/); commit `067b8ad` closes
the documented backlog. This log checks the current canonical documentation,
the routed `loom-mcp` skill, and the entry-point navigation for regressions or
remaining improvement opportunities. Code remains authoritative when it
differs from either documentation source.

## Findings

| ID | Kind | Priority | Evidence | Recommended action |
| --- | --- | --- | --- | --- |
| X1 | Resolved | P1 | `docs/dsl.md` now labels `OAuthScope` as advertised metadata and explicitly assigns enforcement to application authentication/authorization; `docs/runtime.md` and the skill repeat the fail-closed session and resource-grant contracts. | Preserve these statements as contract tests/change-review requirements; no product change is needed. |
| X2 | Resolved | P1 | `docs/runtime.md`, `references/runtime-contracts.md`, and `SKILL.md` now distinguish retryable planner activity attempts, active-work `TimeBudget`, persistent runlog/session stores, transcript memory, long-term memory, and stream/Pulse delivery. | Keep the canonical docs and skill references in the same change set whenever one of these semantic boundaries changes. |
| X3 | Documentation information architecture | P3 | The README's Documentation list links overview, DSL, runtime, glossary, quickstart, and `AGENTS.md`, but omits the public MCP guide at `docs/mcp_sdk_server.md` and the payload-defaults contract at `docs/tool_payload_defaults.md`. The overview points readers to source directories for MCP integration rather than the user guide. | Add both documents to the README and replace the overview's MCP source-directory entry with a link to `docs/mcp_sdk_server.md` (optionally retaining source locations as contributor detail). |
| X4 | Documentation drift prevention | P2 | DeepWiki still indexes `dd837947`, while the current behavior and documentation are in `067b8ad`. The audit fixes also touched several independently authored skill guides and generated quickstarts, showing that code-derived and hand-authored docs can diverge together. | Add a lightweight documentation-contract check: validate internal Markdown links and assert the README documentation index includes every canonical top-level guide. For generated quickstarts, retain/extend the existing golden or compile checks. Request a DeepWiki re-index after the next public push rather than treating the old site as current behavior. |
| X5 | Architectural operational guardrail | P2 | The remediation correctly makes server grants the ceiling for request-scoped MCP allows and binds sessions to principals. These are high-value contracts expressed in generated code and transport tests, while external DeepWiki text will necessarily lag the tree. | Keep the generated SDK/native transport matrices mandatory for changes to MCP adapter options, authentication hooks, or session lifecycle. Record the indexed commit in release notes/user docs when publishing material transport changes. |

## Assessment

No new confirmed security, data-integrity, or runtime-correctness defect was
found in this cross-cutting delta pass. The remaining work is principally
discoverability and regression prevention: make the canonical MCP guide easier
to find, and automatically detect documentation-navigation drift.

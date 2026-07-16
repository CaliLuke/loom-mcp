# DeepWiki documentation, skill, and architecture delta review

Reviewed: 2026-07-16
Current tree: `067b8adee4def7d9f12f1504a5e8497bc329e2f8`
DeepWiki index: `dd8379472f8341a694e65ce53935fe6cda12c2ad`

> Resolution update: the local contract and documentation backlog in this
> review was addressed on branch `codex/deepwiki-debt-remediation`. The
> generated SDK transport now returns 404 for invalid session IDs in accordance
> with the bundled 2025-11-25 MCP Streamable HTTP specification; generated
> layout docs, MCP narrowing guidance, navigation, glossary entries, and
> registry/persistence operations guidance were updated with regression tests.
> DeepWiki re-indexing remains an external publishing action after push.

## Outcome

All 40 DeepWiki pages were rechecked by area against the current repository,
canonical user documentation, and the repository-local `loom-mcp` skill.
The previous audit's high-risk issues are fixed in the current tree. The
external wiki is one contract-changing revision behind, so it is a useful map
of `dd837947`, not current operational guidance.

The newly confirmed MCP session discrepancy was fixed locally: SDK and native
generated servers now classify invalid session IDs as HTTP 404. The remaining
external follow-up is DeepWiki re-indexing; the repository-side backlog is
resolved in this branch.

## Area summaries

| Area | Pages | Current assessment | Area log |
| --- | --- | --- | --- |
| Foundations, DSL, and code generation | 1–3.2 | The skill is aligned. One local P1 documentation error remains: `docs/dsl.md` and `RuntimeData` GoDoc describe obsolete generated agent files, which then feeds inaccurate DeepWiki codegen output. | [foundations-dsl-codegen.md](foundations-dsl-codegen.md) |
| Runtime, planner, persistence, and Pulse | 4–4.7, 8–8.2 | Local docs and skill now correctly describe retries, active-work budgets, authority boundaries, Mongo buckets, manual acknowledgment, and telemetry. The wiki's old Mongo `$push` description is materially unsafe if copied. | [runtime-persistence.md](runtime-persistence.md) |
| MCP, providers, registry, testing, and glossary | 5–10 | MCP authorization and native session binding fixes are present. SDK session status mapping differs from native behavior and contradicts the public guide. Operator docs would benefit from registry and persistence references; provider capabilities should be mechanically checked against conformance. | [mcp-providers-registry.md](mcp-providers-registry.md) |
| Cross-cutting documentation and skill routing | Entry points and contract routing | Current documentation has good semantic coverage but needs stronger navigation and lightweight drift checks. | [cross-cutting-documentation.md](cross-cutting-documentation.md) |

## Prioritized improvement backlog

| Priority | ID | Improvement | Why it matters |
| --- | --- | --- | --- |
| P1 | MCP-1 | Unify SDK and native JSON-RPC session lifecycle authority, or at least use the same typed invalid/expired/ownership outcomes and HTTP mapping. Correct the guide only after behavior converges. | Avoids ambiguous session recovery and prevents documentation from promising 404 where SDK currently returns generic 403. |
| P1 | DOC-1 | Correct `docs/dsl.md` and `RuntimeData` GoDoc to the real generated-agent layout; add a generator documentation regression check. | Eliminates unsafe codegen guidance at its source and prevents the next DeepWiki refresh from republishing it. |
| P1 | DOC-2 | Re-index DeepWiki after the source corrections, and publish the indexed commit alongside release material. | The current wiki repeats obsolete security, persistence, timing, and provider semantics. |
| P2 | DOC-3 | Add a documentation-contract check for internal links, canonical-guide navigation, and executable high-risk snippets. | Reduces the class of stale skill, guide, and generated-quickstart problems found in the first audit. |
| P2 | PROVIDER-1 | Derive or verify the provider capability matrix from the conformance source. | Prevents renewed drift around streaming, structured output, token counting, and optional capabilities. |
| P2 | OPS-1 | Add concise public registry and persistence operations references; consider a Temporal durability readiness warning. | Makes deployment boundaries discoverable without enlarging the runtime monograph. |
| P2 | MCP-2 | Document SDK-specific resource-name narrowing: raw `x-mcp-allow-names` / `x-mcp-deny-names` are automatically mapped only by native JSON-RPC. | Prevents SDK applications from assuming headers enforce their intended grant narrowing. |
| P3 | OPS-2 | Decide whether session capacity/TTL needs one configurable typed policy; consider a Pulse dead-letter/commit helper. | Improves operability while retaining current fail-closed and application-owned delivery contracts. |
| P3 | DOC-4 | Surface `docs/mcp_sdk_server.md` and `docs/tool_payload_defaults.md` from README/overview navigation. | Helps users reach the authoritative public guides instead of source directories. |

## Explicitly resolved since the indexed wiki

Do not reopen these as current defects: request resource names only narrow a
server grant; native session-principal binding fails closed; planner activity
attempts are retry-safe by contract; `TimeBudget` measures active work;
`CapsState.ExpiresAt` is deprecated and ignored; Mongo transcripts use
immutable buckets; manual Pulse acknowledgement exists; provider tool-use IDs
are collision-safe; and optional `TokenCounter` capability remains truthful
through middleware.

## Coverage

- Foundations: 10 pages (1 through 3.2)
- Runtime: 8 pages (4 through 4.7)
- MCP/providers/registry/testing/glossary: 22 pages (5 through 10)

The runtime and MCP logs both discuss pages 8–8.2 because persistence crosses
the two architectural boundaries; together they cover 40 unique pages.

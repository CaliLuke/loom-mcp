---
name: loom-compatibility-release
description: Validate and sequence coupled Loom and loom-mcp releases. Use when a Loom change may affect loom-mcp, when preparing or verifying releases of both sibling repositories, or when updating loom-mcp to a new published Loom tag.
---

# Loom Compatibility Release

Use this workflow whenever Loom and loom-mcp must be released compatibly. Release
Loom first; do not put an unreleased Loom version or branch in a loom-mcp module
file. The complete supporting runbook is `docs/loom_compatibility_release.md`.

## 1. Establish the candidate

- Start with clean, reviewable worktrees in both `../loom` and this repository.
- Record the Loom candidate SHA, its tag description, both worktree statuses, and
  `make loom-status` in release notes or the pull request.
- Confirm the canonical local Loom checkout is `../loom`; use one identical,
  absolute spelling for every local `replace` directive.
- Run Loom's ordinary candidate gates before cross-repo validation:

```bash
make -C ../loom lint
make -C ../loom test
make -C ../loom integration-test
```

## 2. Prove the unreleased Loom candidate through loom-mcp

Switch every loom-mcp module to the sibling checkout. `make loom-local` manages
the root module, assistant fixture, agent-feature fixture, SDK bridge consumer
fixture, and quickstart.

```bash
make loom-local
make loom-status
make gen-registry
make regen-quickstart
make regen-assistant-fixture
make regen-progressive-discovery-fixture
make regen-agent-feature-fixture
# Run this only after sdkbridge.CompatibilityVersion increments.
make regen-sdkbridge-consumer-fixture
make verify-mcp-local
make lint
make test
make itest
```

Review the generated diff before accepting it. Every change must be explained
by the candidate Loom behavior; never hand-edit `gen/` output. If Loom causes a
failure, fix Loom and repeat this entire section. Do not ship a loom-mcp
workaround for an upstream regression; record the failing command and scenario
as an upstream issue.

## 3. Publish Loom only after local compatibility is green

From `../loom`, commit and push the verified candidate, select its full SHA on
canonical `origin/main`, and dispatch the immutable-source publisher:

```bash
make release SOURCE=<full-loom-commit-sha>
```

The workflow allocates the alpha from the selected source's release train.
For stable publication, explicitly promote an eligible existing alpha with
`make release-promote ALPHA=vX.Y.Z-alpha.N VERSION=vX.Y.Z`. Follow the workflow
and verify its exact-source CI evidence. Do not treat Loom as released until
the tag and matching non-draft GitHub Release exist. Its `isPrerelease` state
must be true for a hyphenated semantic prerelease tag and false for a stable tag.
Confirm both before changing
loom-mcp's pin:

```bash
git -C ../loom ls-remote --tags origin "refs/tags/vX.Y.Z" "refs/tags/vX.Y.Z^{}"
gh release view vX.Y.Z --repo CaliLuke/loom
```

## 4. Pin loom-mcp to the published Loom release and prove parity

Set `REMOTE_VERSION` in `scripts/loom_core_mode.sh` to the published tag. Then
restore and verify remote mode:

```bash
make loom-remote
make regen-quickstart
make loom-status
```

Ensure the root module, assistant fixture, SDK bridge consumer fixture,
quickstart, and agent-features fixture all require `vX.Y.Z`. Ensure that none
retain a Loom `replace`. Update README, docs, and repo-local skills when the new
dependency or behavior changes their contract. Then run the published-module
parity gates:
```bash
make verify-mcp-local
make lint
make test
make itest
go test ./...
```

Inspect the final diff and verify each module resolves the released tag with
`go list -m github.com/CaliLuke/loom` (using `-C` for nested modules).

## 5. Release loom-mcp

Only now use `loom-mcp-release`: commit and push the verified compatibility
change, then dispatch `make release SOURCE=<full-loom-mcp-commit-sha>`. The trusted
workflow reuses exact-source main-push CI, allocates an alpha, and publishes
without changing source or main. Stable publication explicitly promotes an
existing alpha with `make release-promote`. Follow the workflow and verify the
annotated tag, Release and evidence asset. Keep remote Loom mode enabled. Go
module proxy visibility may lag publication.

## Release gates and continued work

- A dirty or unexpected generated diff: explain it before accepting the affected
  generated output; preserve unrelated changes and continue independent work.
- A local-candidate or remote-parity failure: keep the affected upgrade or release
  gated, reproduce the failure, and record a concrete ticket in the owning
  repository. Fix the root cause there within the authorized scope; never mask
  it with a `replace` or MCP-side shim. Continue independent authorized work.
- These gates prohibit advancing an unverified release, not continued work.
  Bound investigation to evidence needed for a fix or actionable handoff, rather
  than unrelated rabbit holes. Escalate only when further progress needs
  unavailable access, a user decision, or an action outside the authorized scope.
- A pushed tag with incomplete publication: retry the same source or alpha
  through the owning release workflow; do not cut another version or manually
  bypass its evidence checks.

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

From `../loom`, ensure clean `main` exactly matches `origin/main`, prepare
substantive release notes, and run:

```bash
make release VERSION=vX.Y.Z
```

Do not treat Loom as released until the tag and matching non-draft GitHub
Release exist. Its `isPrerelease` state must be true for a hyphenated semantic
prerelease tag and false for a stable tag. Confirm both before changing
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

Only now use `loom-mcp-release` to commit, tag, push, create the GitHub Release,
and verify `origin/main`, the annotated tag, and the release object. Keep remote
Loom mode enabled throughout this release. Report that Go module proxy visibility
may lag after publication.

## Stop conditions

- A dirty or unexpected generated diff: investigate before proceeding.
- A local-candidate or remote-parity failure: stop the release sequence and fix
  the owning repository; never mask it with a `replace` or MCP-side shim.
- A pushed tag with no GitHub Release: backfill the release object for that tag;
  do not cut another version.

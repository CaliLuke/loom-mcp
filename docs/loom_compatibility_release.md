# Loom compatibility verification

Use this process before releasing Loom when the candidate includes changes that
could affect `loom-mcp`. It proves the unreleased sibling checkout first, then
records the new published tag as the reproducible CI baseline.

## Preconditions

- Start with clean, reviewable worktrees in both sibling repositories.
- Use the canonical `loom` checkout at `../loom`, or set `LOOM_DIR` explicitly.
- Install the toolchain required by both repositories, including Go, `protoc`,
  the protobuf generators, and `golangci-lint`.
- Choose the intended Loom release version before starting. Do not put an
  unreleased version or a branch name in any `loom-mcp` module file.

Record these inputs in the release or pull request notes:

```bash
git -C ../loom rev-parse HEAD
git -C ../loom describe --tags --always --dirty
git status --short --branch
make loom-status
```

If either checkout contains unrelated changes, identify and preserve them. Do
not use `git clean`, `git stash`, `git reset`, or `git checkout` to prepare the
test.

## 1. Prove the Loom release candidate locally

Point the root module, assistant fixture, and quickstart at the exact sibling
checkout:

```bash
make loom-local
local_loom="$(awk '$1 == "replace" && $2 == "github.com/CaliLuke/loom" { print $4 }' go.mod)"
test -n "$local_loom"
go -C integration_tests/fixtures/agent_features mod edit \
  -replace=github.com/CaliLuke/loom="$local_loom"
go -C integration_tests/fixtures/agent_features mod tidy
make loom-status
```

The status output must show a `replace github.com/CaliLuke/loom => .../loom`
line for each module. `make loom-local` manages the root, assistant fixture, and
quickstart; the separately versioned agent-features fixture is switched by the
explicit commands above. Every path must resolve to the candidate commit
recorded above. When using a Go workspace, use the same absolute path spelling
for every replacement so Go does not report conflicting replacements.

Regenerate the checked-in surfaces because compiler success alone does not
detect generator contract drift:

```bash
make gen-registry
make regen-assistant-fixture
make regen-agent-feature-fixture
```

Review the diff before testing. Generated changes must be explainable by the
candidate Loom changes; unexpected churn is a compatibility failure to
investigate, not something to accept mechanically. Never edit generated files
by hand.

Run the local framework/fixture ladder and the full repository gates:

```bash
make verify-mcp-local
make lint
make test
make itest
```

All commands must pass. If a failure is caused by Loom, fix it in Loom and
repeat this section from regeneration. Do not add a `loom-mcp` workaround for
an upstream regression. Record the failing command, package or scenario, and
relevant test output in the Loom release issue or pull request.

## 2. Publish and pin the Loom release

Only after the local ladder is green, complete the Loom release using Loom's
release process. A semantic prerelease is valid only when the GitHub Release is
also marked prerelease. Confirm that both the tag and GitHub Release exist:

```bash
git -C ../loom ls-remote --tags origin "refs/tags/vX.Y.Z" "refs/tags/vX.Y.Z^{}"
gh release view vX.Y.Z --repo CaliLuke/loom
```

Then update `REMOTE_VERSION` in `scripts/loom_core_mode.sh` and switch the
managed modules to the release:

```bash
make loom-remote
go -C integration_tests/fixtures/agent_features get github.com/CaliLuke/loom@vX.Y.Z
go -C integration_tests/fixtures/agent_features mod tidy
make loom-status
```

The root module, assistant fixture, quickstart, and agent-features fixture must
all require the same Loom release tag. No Loom `replace` directive may remain:

```bash
rg -n 'github.com/CaliLuke/loom v|replace github.com/CaliLuke/loom' \
  go.mod quickstart/go.mod integration_tests/fixtures/*/go.mod
```

Update user-facing install/version examples, such as the root `README.md`, in
the same change.

## 3. Prove published-module parity

Repeat the gates without local replacements so the result matches CI and a
downstream user's module graph:

```bash
make verify-mcp-local
make lint
make test
make itest
```

Inspect the final diff and module graph:

```bash
go list -m github.com/CaliLuke/loom
go -C quickstart list -m github.com/CaliLuke/loom
go -C integration_tests/fixtures/assistant list -m github.com/CaliLuke/loom
go -C integration_tests/fixtures/agent_features list -m github.com/CaliLuke/loom
git status --short --branch
```

The compatibility change is ready only when local candidate verification and
published-tag parity both pass. Include the Loom commit, Loom tag, generated
diff summary, exact verification commands, and any compatibility impact in the
pull request or release notes.

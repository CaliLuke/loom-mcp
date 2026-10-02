---
name: loom-mcp-release
description: Prepare, publish, or verify loom-mcp releases directly from main, including safe retries after interrupted publication.
---
# loom-mcp-release

Release `github.com/CaliLuke/loom-mcp/v2` directly from committed `main`.
This is primarily a single-maintainer repository. Do not create a release branch
or PR solely to satisfy the release workflow.

## Publication contract

- Use `make release VERSION=v2.MINOR.PATCH` (or a semantic prerelease version).
  The command pushes committed main, waits for its exact-commit CI push run,
  then publishes an annotated tag and a non-draft GitHub Release.
- Start from clean `main`. Never bypass commit or push hooks.
- Keep remote Loom mode enabled. Use `make loom-remote` before verification
  when necessary; do not introduce dependency changes merely to publish.
- Existing tags are immutable recovery checkpoints. Retry the same command
  and version after interruption, including when main has advanced.
- Conflicting tags or release metadata are errors. Never move a published tag
  or force-push main. Do not silently choose a different version to evade a
  partially published release.
- Stable releases are latest; semantic prereleases are not latest and have
  `isPrerelease=true`.
- A release is complete only when its tag and non-draft GitHub Release exist
  remotely and its CI-verified commit remains on remote main.

## Workflow

1. Inspect `git status --short --branch`, `make loom-status`, actual remote tags,
   and `gh release list`. Choose the next unused version for a new release;
   choose the same version when resuming. Never use web search for tag existence.
2. Finish authorized code and documentation work, then get the required
   independent review and commit it on main. Do not invent a release-only
   code change. Retain the repository's normal local verification requirements.
3. Regenerate only affected surfaces. For a Loom dependency bump, regenerate
   registry, quickstart, assistant, progressive-discovery, and agent-feature
   fixtures. Regenerate the SDK bridge consumer when its compatibility version
   increments. Never edit generated output by hand.
4. Review docs and relevant skills when DSL, runtime, codegen, dependency,
   or release behavior changes. Keep `sdkbridge.CompatibilityVersion` for
   additive compatible changes; increment it for incompatible generated contracts.
5. Run required local gates for the change. The complete `make ci` contract
   includes generation, release-script tests, build, lint, unit/coverage tests,
   integration fixtures, and Docker tests. `make itest` already includes
   `make verify-mcp-local`; do not repeat it without a new reason.
6. Run `make release VERSION=<version>`. Let it push main and wait for CI.
   Missing or pending CI is handled by the command. Failed/canceled CI needs
   diagnosis; fix or rerun it, then retry the same release command.
7. Verify with `git ls-remote --tags origin refs/tags/<version>`,
   `git ls-remote origin main`, and
   `gh release view <version> --json tagName,isDraft,isPrerelease,url,publishedAt`.
   Confirm the tag commit is on remote main. If main advanced, fetch and
   fast-forward the local checkout when possible; do not reset it.
8. Report the release URL. Check Go module visibility only when requested;
   proxy propagation may lag publication.

Do not stop after preparation when the user has authorized publication.
For a preparation-only request, leave a verified, committed candidate and
state that publication has not been run.

## Failure handling

- A dirty/behind/diverged new-release checkout fails before tag publication.
  Reconcile changes without discarding work, then retry.
- A local tag or a remote tag without a release is resumed automatically.
  A completed release is verified and returned without duplicate creation.
- Wrong draft/prerelease metadata requires a deliberate correction; the command
  does not overwrite it. Inspect the actual release before editing anything.
- Never compensate for upstream Loom regressions in loom-mcp. Return exact
  failing scenarios to the owning repository.
- Branch protection prohibits force pushes and deletion, including for admins.
  CI is enforced at release publication rather than before main accepts a push.
  Do not restore a required pre-push check or PR requirement for this workflow.

## References

- `docs/releases.md`: user-facing command, recovery, repository policy, and model scope
- `scripts/release_test.sh`: executable publication regression scenarios
- `docs/formal/Release.tla`: safety and progress model
- `references/release-checklist.md`: concise completion checklist

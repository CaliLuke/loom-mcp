---
name: loom-mcp-release
description: Publish loom-mcp alphas or promote an existing alpha to stable through the CI-verified GitHub workflow.
---
# loom-mcp-release

Use `internal/release` through `.github/workflows/release.yml`. Local commands
dispatch trusted main; they do not create release commits, push main, or publish
tags directly. Do not create release-only branches or PRs.

## Selection and dispatch

- Complete implementation review, mandatory hooks and local checks, then commit
  and push normally. Preserve remote Loom mode and the published Loom pin.
- Select a full SHA from canonical `origin/main`. Run
  `make release SOURCE=<sha>` for an alpha. Optional `VERSION=v2.X.Y-alpha.N`
  asserts allocation; it cannot override the next version or duplicate a source.
- Daily publication runs at 23:47 America/Los_Angeles using the scheduled run's
  captured `GITHUB_SHA`. Scheduling can be delayed or dropped. No new source means
  no new alpha; never fall back to an older green commit.
- `internal/release/train.json` at the selected source owns the v2 release train.
  After stable promotion, daily publication waits for a reviewed train update.
- Promote explicitly with
  `make release-promote ALPHA=v2.X.Y-alpha.N VERSION=v2.X.Y`. Stable must use the
  alpha's exact source; preserve the alpha tag and release. Historical alphas
  without the train marker and CI eligibility gate cannot be promoted.
- All publication modes share one workflow concurrency group. Keep inputs in
  environment variables and credentials out of untrusted PR jobs.

## Evidence and publication

Require successful main-push `ci.yml` for the selected SHA, including every job
and the aggregate Release eligibility job. Inspect the latest run and attempt.
Missing, skipped, canceled, failed, or wrong-source evidence is not green.
Pending CI waits up to 45 minutes. Never substitute local tests for hosted CI.

Keep the normal full verification requirements for code changes. To publish an
unchanged green source, reuse its CI instead of rerunning local suites or adding
a version-only commit. CI owns the full `make ci` contract. Regenerate only when
implementation/design changes require it; never edit generated output by hand.

The publisher creates an immutable annotated tag, draft Release, deterministic
`release-evidence.json`, and substantive notes, then publishes and verifies them.
Alphas are prereleases and not latest; stable promotions are latest. Notes name
source changes and migration guidance; do not add routine CI command lists.

## Recovery and completion

A dispatch is not a completed release. Follow the workflow and verify the remote
tag source, non-draft Release, correct prerelease state, notes and evidence asset.
Report the release URL only after those checks pass. Go proxy visibility may lag.

Retry the same source/alpha after interruption. Reuse existing tags and repair
incomplete draft publication. Completed matching releases are no-ops. Never move
published tags, bypass CI, or choose another version to hide a partial release.
Investigate the actual failed check and continue independent authorized work.

The publisher never changes main. Keep direct normal pushes permitted while
prohibiting force pushes and branch deletion; do not require a main-push CI check
before accepting the push that triggers it.

## Maintainer references

- `docs/releases.md`: commands, scheduling, evidence, recovery and limitations.
- `internal/release`: publisher and executable regression tests.
- `internal/release/tla/README.md`: model assumptions and reproduction.
- `references/release-checklist.md`: completion checks.

For publisher changes, run `make release-test` and
`make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar`, the required
repository gates, and independent frozen-diff review before committing.

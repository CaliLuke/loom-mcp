# Publish immutable source releases

loom-mcp uses the same release setup as Loom: a trusted GitHub workflow publishes
CI-verified source snapshots. Local commands dispatch that workflow. Publication
never edits source files, creates a version commit, or pushes `main`.

## Alpha releases

Finish code review and local verification, commit, and push normally. Select the
full SHA on canonical `origin/main`, then dispatch:

```bash
make release SOURCE=<full-commit-sha>
```

The publisher reads `internal/release/train.json` from that exact source. The
current train is `v2.1.0`; tags advance as `v2.1.0-alpha.N`. An optional
`VERSION=v2.1.0-alpha.N` asserts the allocated version; it cannot override it.
The same source reuses its existing alpha. No new source means no new alpha.

The workflow also requests a daily alpha at 23:47 America/Los_Angeles. It uses
the immutable `GITHUB_SHA` captured for that scheduled run. GitHub may delay or
drop scheduled runs; this is a daily snapshot policy, not a timing guarantee.
A failed source never falls back to an older green commit. After stable
promotion closes a train, publication waits for a reviewed source commit that
updates `train.json`.

## Stable promotion

Promote a published alpha explicitly:

```bash
make release-promote ALPHA=v2.1.0-alpha.N VERSION=v2.1.0
```

The stable tag identifies exactly the alpha's source, even if `main` has
advanced. The alpha remains available. Stable releases are latest; alphas are
prereleases and never become latest. A promotion must stay within the same v2
release train. Historical alphas without the train marker and CI eligibility
gate cannot be promoted by this workflow; publish a new eligible alpha first.

## CI and publication

`.github/workflows/release.yml` runs on trusted `main` and serializes manual,
scheduled, and promotion jobs through one repository-wide concurrency group.
Inputs pass through environment variables, not interpolated shell source.
The publisher validates the canonical `CaliLuke/loom-mcp` origin and requires
that the selected source belongs to remote `main`.

New publication requires the latest main-push run of `ci.yml` for the exact
source. Every job must succeed, including the aggregate **Release eligibility**
job. Failed, canceled, skipped, missing, or wrong-source evidence is rejected.
Pending CI waits up to 45 minutes. `ci.yml` runs the complete `make ci` contract:
generation checks, release regression tests, build, lint, unit/coverage tests,
integration fixtures, and Docker tests. Weekly stress tests remain independent.

The publisher creates an annotated tag, a draft GitHub Release, and a
`release-evidence.json` asset recording the source and successful CI run and
attempt. It then publishes and verifies the Release, evidence, and remote tag.
Notes list commit subjects since the preceding release; stable notes cover the
range since the previous stable and name the promoted alpha. Write meaningful
commit subjects and update public migration guidance with implementation changes.

Keep normal review, hooks, remote Loom pins, generation, and local verification
when changing code. Publishing an unchanged eligible commit reuses its CI; do
not create version-only commits or rerun the entire local suite just to release.
The repository permits direct normal pushes to `main`; force pushes and branch
deletion remain prohibited. Do not introduce a required pre-push main check that
would require CI before the push that triggers it.

## Recovery and completion

A successful dispatch is not completed publication. Follow the workflow run and
verify the non-draft Release, annotated tag source, prerelease state, notes, and
`release-evidence.json`. Report the resulting release URL. Go proxy visibility
can lag publication.

Retry the same source or alpha after interruption. The publisher reuses the
allocated tag and repairs incomplete draft/asset publication. A completed matching
release is a no-op. Conflicting tags, incomplete evidence, or inconsistent
metadata fail closed. Never force-update tags, invent a new version to evade a
partial publication, or manually publish around the CI gate.

## Regression checks

```bash
make release-test
make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar
```

The Go tests cover allocation, v2 train validation, exact-source CI and jobs,
API failures, retries, promotion, and publication metadata without publishing real
releases. The [TLA+ model](../internal/release/tla/README.md) checks immutable source
ownership under a moving main branch and serialized publication. Its unsafe
configuration must reproduce a counterexample. These are bounded invariants,
not a proof of GitHub availability, credential security, or test coverage.

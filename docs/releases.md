# Release from main

Commit your changes on `main`, then run:

```bash
make release VERSION=v2.1.0-alpha.32
```

Choose the next unused version for a new release. No release branch, PR,
manual CI polling, or separate tag command is needed. Use the same version
when retrying an interrupted release.

The command:

1. Requires a clean `main` checkout and a valid v2 semantic version.
2. Pushes the committed `main` candidate with a normal fast-forward push.
   A behind or diverged checkout stops before publication; reconcile it first.
3. Waits for the latest **CI push run for that exact commit** to succeed.
   Startup delay and pending runs are expected. Failed or canceled CI stops
   publication. Fix the failure or rerun that CI run, then retry the command.
4. Creates an annotated tag and pushes only that tag. The candidate must still
   belong to remote `main`; newer commits on `main` do not invalidate it.
5. Creates and verifies the GitHub Release. Stable releases become latest.
   Prereleases have `isPrerelease=true` and never become latest.

The default CI wait is one hour, polling every ten seconds. Override
`RELEASE_WAIT_SECONDS` and `RELEASE_POLL_SECONDS` when needed. Timing out does
not undo the main push. Repeat the same command when CI is available.

## Recovery

A local or remote tag fixes the release candidate. Repeating the command
verifies that annotated tag, checks its commit's CI, and finishes any missing
publication step. This also works after `main` has advanced. A completed release
is verified and returned without creating another release.

Conflicting local/remote tags, lightweight tags, tags outside remote `main`,
and existing releases with incorrect draft/prerelease metadata are errors.
The command never deletes, replaces, or force-pushes a tag. A failed network
reply after GitHub creates the release is reconciled by reading the release
back. If that read also fails, retry the same command.

## Verification and repository settings

Keep normal repository review, hooks, remote Loom pins, regeneration, and
local test requirements when changing code. Hosted CI runs the complete
`make ci` contract once on the pushed main commit. The release command waits
for that run; a second PR/merge CI cycle is unnecessary.

For this primarily single-maintainer repository, `main` permits direct normal
pushes. Branch protection still applies to administrators and prohibits force
pushes and branch deletion. There is no required pre-push status check or PR
requirement: requiring a main push run before accepting the push creates a
circular dependency. CI remains the publication gate in `make release`.
Do not add a ruleset that reintroduces that pre-push requirement.

Keep hooks installed with `make lint-install-hook`. Configure the clone with:

```bash
git config --local fetch.prune true
git config --local pull.ff only
```

Use `make release` for publication. Manually creating tags or releases does
not run these guards.

## Regression proof

`make release-test` uses temporary Git remotes and a fake GitHub CLI. It covers
main pushes, delayed/failed CI, immutable tags, concurrent updates, interruption
recovery, and stable/prerelease metadata without publishing anything.

The [TLA+ model](formal/Release.tla) checks safety and eventual publication for
two competing release invocations, two ordered commits, and at most one
interruption per invocation. It models ref/API updates as atomic mutations;
a crash after mutation also represents a lost network reply. Eventual
publication assumes successful CI and fair scheduling after the bounded
interruptions. A separate configuration allows CI failure and checks safety
without requiring publication. This is bounded model checking, not a proof of
all Git/GitHub behavior; the shell scenarios exercise the implementation.

Run all four model configurations with a development-only
[TLC 1.7.4 jar](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4) and Java:

```bash
make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar
```

The fixed configurations verify that main never rewinds, tags/releases never
move, and only a verified commit on main is published. The two old-policy
configurations must produce progress counterexamples:

- `ReleasePrePushCI.cfg`: the first main push needs CI, but CI needs that push.
- `ReleaseNoResume.cfg`: an interruption leaves a tag, and retries refuse it.

See the [TLA+ reference](https://lamport.azurewebsites.net/tla/book-02-08-08.pdf)
for TLC's safety and liveness checking semantics.

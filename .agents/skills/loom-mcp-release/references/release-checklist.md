# loom-mcp Release Checklist

- Clean committed `main`; normal hooks, review, and local gates completed.
- Published Loom pin selected; required generated output and docs current.
- Version chosen from actual remote tags/releases; same version for recovery.
- Run `make release VERSION=<version>` directly on main. The command pushes,
  waits for exact-commit main CI, and publishes. No release branch or PR.
- If CI fails, diagnose it and fix or rerun it. If the command is interrupted,
  rerun with the same version. Never move an existing tag.
- Verify the remote annotated tag, successful CI on its commit, ancestry on
  remote main, and non-draft GitHub Release with correct prerelease metadata.
- Report the release URL; Go module proxy visibility can lag.

For release-workflow changes, run `make release-test` and
`make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar`.
The latter checks the fixed model and requires counterexamples for the old
pre-push-CI and non-resumable policies. See `docs/releases.md` for the model's
bounds and assumptions.

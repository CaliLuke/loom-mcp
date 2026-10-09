# loom-mcp Release Checklist

- Implementation committed and pushed; required review, hooks and local checks passed.
- Published Loom pin and required generated output/docs current.
- Select exact remote-main source for alpha, or published eligible alpha for promotion.
- Dispatch `make release SOURCE=<sha>` or
  `make release-promote ALPHA=v2.X.Y-alpha.N VERSION=v2.X.Y`.
- Follow the workflow: successful dispatch alone is not publication.
- Require the latest exact-source main-push `ci.yml` run, all jobs successful,
  including Release eligibility. Do not bypass missing or failed evidence.
- On interruption, retry the same source/alpha. Never move tags or create release commits.
- Verify annotated remote tag source, non-draft Release, prerelease/latest policy,
  substantive notes and matching `release-evidence.json`.
- Report the release URL; Go module proxy visibility can lag.

For workflow changes, run `make release-test` and
`make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar`.
The model checks selected-source identity and requires an unsafe moving-main
counterexample. See `internal/release/tla/README.md` for bounds and assumptions.

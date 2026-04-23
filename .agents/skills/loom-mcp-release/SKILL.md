---
name: loom-mcp-release
description: Cut and publish a loom-mcp release from verified code. Use this skill when the task is to prepare a patch/minor release, tag the repo, push the release, verify remote publication, or walk from finished code to a fully published loom-mcp version.
---
# loom-mcp-release

Use this skill when releasing `github.com/CaliLuke/loom-mcp`. Keep the workflow strict. Release work should be deterministic, fail fast, and leave a traceable tag on `main`.

## Non-Negotiables

- Release from a clean `main` worktree unless the user explicitly wants a different branch flow.
- Never bypass hooks. If commit-time hooks fail, fix the underlying problem and retry.
- Use `make loom-remote` before release verification and before the release commit so the repo is pinned to the published `github.com/CaliLuke/loom` dependency, not a local checkout.
- If the release changed assistant fixture DSL or generated MCP output, run `make regen-assistant-fixture` before verification.
- If the release changed user-facing DSL, codegen, runtime, or release workflow behavior, update the repo docs in `docs/`, any release-facing root docs, and the relevant repo-local skills in `.agents/skills/` before tagging.
- Do not hand-edit generated `gen/` files.
- Do not call the release published until `main`, the tag, and the GitHub Release object all exist remotely.
- If a tag already exists without a GitHub Release, backfill the release object before treating that version as published.

## Default Release Workflow

1. Confirm the intended semantic version bump and inspect the repo state with:
   - `git status --short`
   - `git branch --show-current`
   - `make loom-status`
   - `git tag --sort=creatordate`
   - `gh release list --limit 20`
2. Switch to remote Loom mode for release parity:
   - `make loom-remote`
3. Regenerate only when required by the change:
   - `make regen-assistant-fixture` for assistant fixture DSL changes
   - any normal design/codegen regeneration already required by the change itself
4. Update docs whenever shipped behavior or release workflow guidance changed:
   - update `docs/` for user-facing DSL, runtime, or codegen contract changes
   - update release-facing root docs such as `README.md` when dependency pins, commands, or workflow expectations changed
   - update the relevant repo-local skills in `.agents/skills/`, especially `.agents/skills/loom-mcp/` and this release skill, when the shipped product or release workflow changed
5. Run the full release verification suite in this order:
   - `make lint`
   - `make test`
   - `make itest`
   - `make verify-mcp-local`
   - `go test ./...`
6. Review the final diff and confirm the docs shipped with the same contract as the code.
7. Commit the release-ready changes on `main`.
8. Create an annotated tag for the release version, for example:
   - `git tag -a v1.0.3 -m "v1.0.3"`
9. Publish the release:
   - `git push origin main`
   - `git push origin v1.0.3`
   - `gh release create v1.0.3 --verify-tag --generate-notes --latest`
10. Verify the published state:
   - `git ls-remote --tags origin v1.0.3`
   - `git ls-remote origin main`
   - `gh release view v1.0.3 --json tagName,isDraft,isPrerelease,url,publishedAt`
11. Verify module visibility if the user asks for full downstream confirmation:
   - `go list -m -versions github.com/CaliLuke/loom-mcp`
   - if the new version is not visible yet, note that Go proxy propagation can lag after the Git push

## Backfill Workflow For Missing GitHub Releases

Use this when a semver tag already exists on `origin` but the GitHub Releases page is missing that version.

1. Confirm the tag exists remotely:
   - `git ls-remote --tags origin vX.Y.Z`
2. Confirm the GitHub Release object is missing:
   - `gh release view vX.Y.Z --json tagName,url`
3. Create the missing release from the existing tag:
   - `gh release create vX.Y.Z --verify-tag --notes-from-tag`
4. If the tag message is not suitable release notes, replace step 3 with:
   - `gh release create vX.Y.Z --verify-tag --generate-notes`
5. Verify the release now exists:
   - `gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,publishedAt`

## Exact Command Ladder

Use this ladder unless the repo state makes one of the steps irrelevant:

```bash
git status --short
git branch --show-current
make loom-status
git tag --sort=creatordate
gh release list --limit 20
make loom-remote
<update docs and repo-local skills if behavior or workflow changed>
make lint
make test
make itest
make verify-mcp-local
go test ./...
git status --short
git add <files>
git commit -m "<release or fix message>"
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main
git push origin vX.Y.Z
gh release create vX.Y.Z --verify-tag --generate-notes --latest
git ls-remote --tags origin vX.Y.Z
git ls-remote origin main
gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,publishedAt
```

## Decision Rules

- If `make loom-status` shows a local replace before release, switch with `make loom-remote` and rerun verification.
- If verification fails after switching to remote mode, stop. Do not patch around an upstream `loom` regression in `loom-mcp`; return the exact failing scenario.
- If generated output changed unexpectedly, trace it back to source changes before committing.
- If the release includes user-facing framework behavior changes, update the repo docs under `docs/` in the same release.
- If dependency pins, verification commands, or local-vs-remote workflow guidance changed, update release-facing root docs such as `README.md` in the same release.
- If the shipped product or release workflow changed, update the relevant repo-local skills in `.agents/skills/` in the same release.
- If the user asked for a dot release, prefer the smallest semver bump that matches the shipped behavior.
- If `gh release view vX.Y.Z` fails while `git ls-remote --tags origin vX.Y.Z` succeeds, backfill the missing GitHub Release before closing the task.

## Publish Contract

Treat the release as complete only when all of the following are true:

- verification passed in remote mode
- docs and relevant repo-local skills were reviewed and updated wherever the shipped contract or release workflow changed
- the release commit exists on `main`
- the annotated `vX.Y.Z` tag exists locally and on `origin`
- `origin/main` points at the release commit
- the GitHub Release object for `vX.Y.Z` exists and is not a draft
- the user is told that Go module proxy availability may lag slightly after push

## References

- `references/release-checklist.md`: release checklist and rationale for each gate

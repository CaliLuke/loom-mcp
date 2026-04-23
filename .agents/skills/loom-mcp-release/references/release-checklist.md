# loom-mcp Release Checklist

Use this checklist when cutting a release from already-finished code.

## 1. Preflight

- Worktree clean enough to understand exactly what will ship: `git status --short`
- On the intended branch, normally `main`: `git branch --show-current`
- Confirm current Loom source: `make loom-status`
- Confirm the next version tag is correct: `git tag --sort=creatordate`
- Confirm whether GitHub Release objects already exist: `gh release list --limit 20`

## 2. Release Parity

Release verification must use the pinned remote `github.com/CaliLuke/loom` dependency:

```bash
make loom-remote
```

If the repo was iterating against `/Users/luca/code/loom-mono/loom`, this command removes the local replace and restores the pinned release in both the root module and the assistant fixture module.

## 3. Regeneration

Run regeneration only when the shipped changes require it.

- Assistant fixture DSL changed: `make regen-assistant-fixture`
- Other design/codegen changes: run the normal generation step required by that surface before verification

Never hand-edit generated `gen/` files.

## 4. Docs

Docs and repo-local skills are release gates, not cleanup.

- Update `docs/` whenever the shipped change affects user-facing DSL, codegen, runtime, or schema contracts
- Update release-facing root docs such as `README.md` whenever dependency pins, commands, or local-vs-remote workflow guidance changed
- Update the relevant repo-local skills in `.agents/skills/`, especially `.agents/skills/loom-mcp/` and `.agents/skills/loom-mcp-release/`, whenever the shipped product or release workflow changed
- Review the final diff to make sure the docs and skills describe the code that is actually being tagged

## 5. Verification

Run all release gates, not only targeted tests:

```bash
make lint
make test
make itest
make verify-mcp-local
go test ./...
```

This covers:
- root linting
- non-integration package tests
- integration tests
- MCP fixture/framework verification
- full package traversal

## 6. Release Commit

After verification passes:

```bash
git add <files>
git commit -m "<release or fix message>"
```

Do not use `--no-verify`.

## 7. Tag and Publish

Create an annotated semver tag, push both branch and tag, then create the GitHub Release object:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main
git push origin vX.Y.Z
gh release create vX.Y.Z --verify-tag --generate-notes --latest
```

## 8. Remote Verification

Check that the branch, tag, and GitHub Release object exist remotely:

```bash
git ls-remote origin main
git ls-remote --tags origin vX.Y.Z
gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,publishedAt
```

The release is not complete until the GitHub Release exists and is not a draft.

## 9. Backfill Missing GitHub Releases

If a tag was already pushed without a GitHub Release object:

```bash
git ls-remote --tags origin vX.Y.Z
gh release create vX.Y.Z --verify-tag --generate-notes
gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,publishedAt
```

Use `--notes-from-tag` instead of `--generate-notes` only when the annotated tag message already contains the intended release notes.

## 10. Module Availability

If the goal is "fully published" rather than only "git release pushed", also check module visibility:

```bash
go list -m -versions github.com/CaliLuke/loom-mcp
```

If the new version is missing immediately after the push, report that Go proxy propagation can take a short time.

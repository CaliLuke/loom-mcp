# loom-mcp Release Checklist

Use this checklist when cutting a release from already-finished code.

## 1. Preflight

- Worktree clean enough to understand exactly what will ship: `git status --short`
- On the intended branch, normally `main`: `git branch --show-current`
- Confirm current Goa source: `make goa-status`
- Confirm the next version tag is correct: `git tag --sort=creatordate`

## 2. Release Parity

Release verification must use the pinned remote `github.com/CaliLuke/loom` dependency:

```bash
make goa-remote
```

If the repo was iterating against `/Users/luca/code/loom-mono/loom`, this command removes the local replace and restores the pinned release in both the root module and the assistant fixture module.

## 3. Regeneration

Run regeneration only when the shipped changes require it.

- Assistant fixture DSL changed: `make regen-assistant-fixture`
- Other design/codegen changes: run the normal generation step required by that surface before verification

Never hand-edit generated `gen/` files.

## 4. Verification

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

## 5. Release Commit

After verification passes:

```bash
git add <files>
git commit -m "<release or fix message>"
```

Do not use `--no-verify`.

## 6. Tag and Publish

Create an annotated semver tag and push both branch and tag:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main
git push origin vX.Y.Z
```

## 7. Remote Verification

Check that the branch and tag exist remotely:

```bash
git ls-remote origin main
git ls-remote --tags origin vX.Y.Z
```

## 8. Module Availability

If the goal is "fully published" rather than only "git release pushed", also check module visibility:

```bash
go list -m -versions github.com/CaliLuke/loom-mcp
```

If the new version is missing immediately after the push, report that Go proxy propagation can take a short time.

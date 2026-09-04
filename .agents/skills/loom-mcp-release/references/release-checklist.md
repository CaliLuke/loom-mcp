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

If the repository used a local Loom checkout, this command removes the local replace and restores the pinned release in the root module, assistant fixture, agent-feature fixture, SDK bridge consumer fixture, and quickstart.

## 3. Regeneration

Review each `runtime/mcp/sdkbridge` change before release.

Keep `sdkbridge.CompatibilityVersion` unchanged for compatible additions. These
changes include internal fixes and optional fields with safe zero-value
behavior. Existing generated consumers must continue to work without
regeneration.

Increment `sdkbridge.CompatibilityVersion` when old generated descriptors or
callbacks are unsafe with the new runtime contract. Do not add a compatibility
path for the old version. The generator reads the runtime constant and emits the
new value as a literal.

Run regeneration when the shipped changes require it:

- Assistant fixture DSL changed: `make regen-assistant-fixture`
- Loom dependency changed: `make gen-registry`, `make regen-quickstart`,
  `make regen-assistant-fixture`, `make regen-progressive-discovery-fixture`,
  and `make regen-agent-feature-fixture`
- `sdkbridge.CompatibilityVersion` changed: `make regen-assistant-fixture`,
  `make regen-progressive-discovery-fixture`, and
  `make regen-sdkbridge-consumer-fixture`. The SDK bridge consumer is a frozen
  compatibility baseline. Its regeneration command fails unless the runtime
  compatibility version is greater than the version at the durable comparison
  base. CI supplies that base. Local checks use the tracking branch or the most
  recent release tag.
- Other design or codegen changed: run the generation target for that surface.

After regeneration, prove that every tracked surface is current:

```bash
make verify-generated
```

This command snapshots the release diff and regenerates every tracked surface.
It fails if regeneration changes the diff. Local and hosted CI use this target.

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

Create an annotated semver tag, push both branch and tag, then create the GitHub Release object. Use `--latest` only for stable releases:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main
git push origin vX.Y.Z
gh release create vX.Y.Z --verify-tag --generate-notes --latest
```

For a prerelease such as `v2.1.0-alpha.1`, publish with:

```bash
gh release create v2.1.0-alpha.1 --verify-tag --generate-notes --prerelease
```

Never mark a prerelease as latest.

## 8. Remote Verification

Check that the branch, tag, and GitHub Release object exist remotely:

```bash
git ls-remote origin main
git ls-remote --tags origin vX.Y.Z
gh release view vX.Y.Z --json tagName,isDraft,isPrerelease,url,publishedAt
```

The release is not complete until the GitHub Release exists, is not a draft, and has `isPrerelease` set to true for a hyphenated tag or false for a stable tag.

## 9. Backfill Missing GitHub Releases

If a tag was already pushed without a GitHub Release object, use the command
that matches the tag type:

```bash
# Set this to the exact stable or prerelease tag.
VERSION=vX.Y.Z
git ls-remote --tags origin "${VERSION}"
# Stable release only:
gh release create "${VERSION}" --verify-tag --generate-notes --latest
# Prerelease only:
gh release create "${VERSION}" --verify-tag --generate-notes --prerelease
gh release view "${VERSION}" --json tagName,isDraft,isPrerelease,url,publishedAt
```

Use `--notes-from-tag` only when the annotated tag has suitable release notes.
Use it instead of `--generate-notes`. Keep the stable or prerelease flag.

## 10. Module Availability

If the goal is "fully published" rather than only "git release pushed", also check module visibility:

```bash
go list -m -versions github.com/CaliLuke/loom-mcp/v2
```

If the new version is missing immediately after the push, report that Go proxy propagation can take a short time.

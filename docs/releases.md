# Releases and Git hygiene

Publish releases from a clean `main` checkout after the commit has passed CI.
Use the repository command:

```bash
git switch main
git pull --ff-only
make release VERSION=v2.1.0-alpha.24
```

Choose the next unused version. The version above is an example.
The command checks that `HEAD` equals remote `main` and that the latest
`CI` push run for that exact commit succeeded. It rejects existing tags.
It creates an annotated tag for the checked commit. The atomic push rejects a
`main` advance visible during push negotiation. A later advance can succeed;
the tag stays fixed on the verified commit already included in protected `main`.
Stable versions become the latest GitHub release. Prereleases do not.

Run the normal release verification before landing changes: remote Loom mode,
required regeneration, generated-output verification, lint, unit tests, and
integration tests. Hosted CI repeats the full `make ci` contract.
`make release-test` exercises publication guards against temporary repositories.
It uses a fake GitHub CLI and never publishes to GitHub.

If publication fails after the local tag was created, inspect both the local
and remote tag before retrying. Do not move a published tag. If the remote tag
exists but its GitHub release is missing, confirm that the tag's commit is in
remote `main`, then backfill the release using the release skill.

## Repository settings

- Require the GitHub Actions `verify` check on `main`, with the branch current.
- Apply protection to administrators. Block force pushes and branch deletion.
- Delete GitHub branches automatically after their pull requests are merged.
- Keep the repository hooks installed with `make lint-install-hook`.

Configure each clone to prune deleted remote branches and reject merge pulls:

```bash
git config --local fetch.prune true
git config --local pull.ff only
```

The release command guards this publication path. Manual tag pushes and GitHub
UI releases do not run it; use `make release` for new versions.

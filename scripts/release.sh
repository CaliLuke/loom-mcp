#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "release: $*" >&2
  exit 1
}

version="${1:-}"
if [[ $# -ne 1 || ! "${version}" =~ ^v2\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  fail 'version must be v2.MINOR.PATCH with an optional prerelease suffix'
fi
if [[ "${version}" == *-* ]]; then
  IFS='.' read -r -a identifiers <<<"${version#*-}"
  for identifier in "${identifiers[@]}"; do
    if [[ "${identifier}" =~ ^0[0-9]+$ ]]; then
      fail 'version must be semver: numeric prerelease identifiers cannot have leading zeros'
    fi
  done
fi

cd "$(git rev-parse --show-toplevel)"
if [[ "$(git branch --show-current)" != main || -n "$(git status --porcelain)" ]]; then
  fail 'a clean main checkout is required'
fi
commit="$(git rev-parse HEAD)"
remote_main="$(git ls-remote --heads origin refs/heads/main)"
if [[ "${remote_main%%$'\t'*}" != "${commit}" ]]; then
  fail 'HEAD must equal remote main; land the change and pull before releasing'
fi
remote_tag="$(git ls-remote --tags origin "refs/tags/${version}")"
if git show-ref --verify --quiet "refs/tags/${version}" || [[ -n "${remote_tag}" ]]; then
  fail "tag already exists: ${version}; verify it before backfilling a missing GitHub release"
fi

repository="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
ci_result="$(gh run list --repo "${repository}" --workflow ci.yml --branch main \
  --event push --commit "${commit}" --limit 1 --json status,conclusion \
  --jq '.[0] | .status + ":" + .conclusion')"
if [[ "${ci_result}" != completed:success ]]; then
  fail "a successful CI push run is required for ${commit} (got ${ci_result:-none})"
fi

git tag -a "${version}" "${commit}" -m "${version}"
# Reject a main advance visible when Git negotiates the push; never roll it back.
git push --atomic origin "${commit}:refs/heads/main" "refs/tags/${version}"

release_flags=(--latest)
if [[ "${version}" == *-* ]]; then
  release_flags=(--prerelease --latest=false)
fi
gh release create "${version}" --repo "${repository}" --verify-tag \
  --target main --generate-notes "${release_flags[@]}"

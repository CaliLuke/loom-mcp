#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "release: $*" >&2
  exit 1
}

# Read remote state afresh at publication boundaries. Never force-update a ref.
fetch_main() {
  git fetch --quiet --no-tags origin refs/heads/main:refs/remotes/origin/main
}

read_remote_tag() {
  local ref
  ref="$(git ls-remote --tags origin "refs/tags/${version}")" || return
  printf '%s' "${ref%%$'\t'*}"
}

release_metadata() {
  gh release view "${version}" --repo "${repository}" \
    --json isDraft,isPrerelease,url --jq '"\(.isDraft):\(.isPrerelease):\(.url)"'
}

# CI discovery can lag a successful push. Wait for the latest push run for
# this immutable commit; never accept another commit's green branch status.
wait_for_ci() {
  local deadline=$((SECONDS + wait_seconds)) run previous=""
  while true; do
    run="$(gh run list --repo "${repository}" --workflow ci.yml --branch main \
      --event push --commit "${commit}" --limit 1 --json databaseId,status,conclusion \
      --jq '.[0] | if . == null then "missing" else "\(.databaseId):\(.status):\(.conclusion)" end')"
    if [[ "${run}" != "${previous}" ]]; then
      printf 'release: CI for %s: %s\n' "${commit}" "${run}"
      previous="${run}"
    fi
    case "${run}" in
      *:completed:success) return ;;
      *:completed:*) fail "CI did not pass (${run}); fix or rerun that CI run, then repeat this command" ;;
    esac
    (( SECONDS < deadline )) || fail "timed out waiting for CI; repeat this command to resume"
    sleep "${poll_seconds}"
  done
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
wait_seconds="${RELEASE_WAIT_SECONDS:-3600}"
poll_seconds="${RELEASE_POLL_SECONDS:-10}"
[[ "${wait_seconds}" =~ ^[1-9][0-9]*$ ]] || fail 'RELEASE_WAIT_SECONDS must be a positive integer'
[[ "${poll_seconds}" =~ ^(0|[1-9][0-9]*)$ ]] || fail 'RELEASE_POLL_SECONDS must be a nonnegative integer'

cd "$(git rev-parse --show-toplevel)"
if [[ "$(git branch --show-current)" != main || -n "$(git status --porcelain)" ]]; then
  fail 'a clean main checkout is required; commit your changes first'
fi
head="$(git rev-parse HEAD)"
repository="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
fetch_main
remote_tag="$(read_remote_tag)"
local_tag="$(git show-ref --verify --hash "refs/tags/${version}" || true)"
if [[ -n "${local_tag}" && -n "${remote_tag}" && "${local_tag}" != "${remote_tag}" ]]; then
  fail 'local and remote tags differ; neither tag was changed'
fi
if [[ -n "${remote_tag}" && -z "${local_tag}" ]]; then
  git fetch --quiet --no-tags origin "refs/tags/${version}:refs/tags/${version}"
  local_tag="$(git rev-parse "refs/tags/${version}")"
fi
if [[ -n "${local_tag}" ]]; then
  [[ "$(git cat-file -t "${local_tag}")" == tag ]] || fail 'an annotated tag is required'
  commit="$(git rev-parse "${local_tag}^{commit}")"
  git merge-base --is-ancestor "${commit}" origin/main || fail 'existing tag is not on remote main'
else
  commit="${head}"
  git merge-base --is-ancestor origin/main "${commit}" || fail 'remote main has advanced or diverged; reconcile main first'
  # A normal push rejects races and runs installed pre-push hooks.
  git push origin "${commit}:refs/heads/main"
fi
wait_for_ci

# A main advance during CI is fine: the verified candidate remains in history.
fetch_main
git merge-base --is-ancestor "${commit}" origin/main || fail 'release commit is no longer on remote main'
if [[ "$(git rev-parse HEAD)" != "${head}" || "$(git branch --show-current)" != main || -n "$(git status --porcelain)" ]]; then
  fail 'checkout changed during release; repeat from a clean main checkout'
fi
if [[ -z "${local_tag}" ]]; then
  git tag -a "${version}" "${commit}" -m "${version}"
  local_tag="$(git rev-parse "refs/tags/${version}")"
fi
remote_tag="$(read_remote_tag)"
if [[ -n "${remote_tag}" && "${remote_tag}" != "${local_tag}" ]]; then
  fail 'local and remote tags differ; neither tag was changed'
fi
if [[ -z "${remote_tag}" ]]; then
  # If the reply is lost, the next invocation discovers this same tag.
  git push origin "refs/tags/${version}"
fi

prerelease=false
release_flags=(--latest)
if [[ "${version}" == *-* ]]; then
  prerelease=true
  release_flags=(--prerelease --latest=false)
fi
if ! metadata="$(release_metadata 2>/dev/null)"; then
  if ! gh release create "${version}" --repo "${repository}" --verify-tag \
    --target "${commit}" --generate-notes "${release_flags[@]}"; then
    echo 'release: create did not confirm success; checking durable release state' >&2
  fi
  metadata="$(release_metadata)" || fail 'release could not be verified; repeat this command to resume'
fi
[[ "${metadata}" == "false:${prerelease}:"* ]] || fail 'existing release has incorrect draft/prerelease metadata'
[[ "$(read_remote_tag)" == "${local_tag}" ]] || fail 'remote tag changed during publication'
printf 'release: published %s at %s\n%s\n' "${version}" "${commit}" "${metadata#false:${prerelease}:}"

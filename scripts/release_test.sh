#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
publisher="${script_dir}/release.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
export RELEASE_POLL_SECONDS=0 RELEASE_WAIT_SECONDS=2
export GH_STATE="${test_root}/gh-state"
mkdir "${test_root}/bin" "${GH_STATE}"
cat >"${test_root}/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  'repo view') echo test/repository ;;
  'run list')
    printf '%s\n' "$*" >>"${GH_STATE}/runs"
    if [[ "${ADVANCE_MAIN}" == true && ! -f "${GH_STATE}/advanced" ]]; then
      next="$(printf 'concurrent advance\n' | git commit-tree 'HEAD^{tree}' -p HEAD)"
      git push --quiet origin "${next}:refs/heads/main"
      touch "${GH_STATE}/advanced"
    fi
    if [[ "${CONFLICT_TAG}" == true && ! -f "${GH_STATE}/contender" ]]; then
      git tag -a competing -m competing
      git push --quiet origin competing:refs/tags/v2.9.0
      touch "${GH_STATE}/contender"
    fi
    case "${CI_RESULT}" in
      delayed)
        count="$(wc -l <"${GH_STATE}/runs" | tr -d ' ')"
        case "${count}" in
          1) echo missing ;;
          2) echo '17:in_progress:' ;;
          *) echo '17:completed:success' ;;
        esac ;;
      *) printf '%s\n' "${CI_RESULT}" ;;
    esac ;;
  'release view')
    [[ -f "${GH_STATE}/release" ]] || exit 1
    cat "${GH_STATE}/release" ;;
  'release create')
    printf '%s\n' "$*" >>"${GH_STATE}/creates"
    [[ "${CREATE_RESULT}" != before ]] || exit 1
    prerelease=false
    [[ "$*" != *--prerelease* ]] || prerelease=true
    printf 'false:%s:https://example.invalid/releases/%s\n' "${prerelease}" "$3" >"${GH_STATE}/release"
    [[ "${CREATE_RESULT}" != after ]] || exit 1 ;;
  *) echo "unexpected gh call: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${test_root}/bin/gh"
export PATH="${test_root}/bin:${PATH}"

new_repo() {
  local name="$1"
  git init --bare --initial-branch=main --quiet "${test_root}/${name}.git"
  git init --initial-branch=main --quiet "${test_root}/${name}"
  cd "${test_root}/${name}"
  git config user.name 'Release Test'
  git config user.email 'release@example.invalid'
  echo initial >content
  git add content
  git commit --quiet -m initial
  git remote add origin "${test_root}/${name}.git"
  git push --quiet -u origin main
  rm -f "${GH_STATE}"/*
  export CI_RESULT=17:completed:success ADVANCE_MAIN=false CREATE_RESULT=success CONFLICT_TAG=false
}

publish() {
  if ! bash "${publisher}" "$1" >"${test_root}/output" 2>&1; then
    cat "${test_root}/output" >&2
    exit 1
  fi
}

reject() {
  local expected="$1"
  shift
  if bash "${publisher}" "$@" >"${test_root}/output" 2>&1; then
    echo "release unexpectedly accepted: ${expected}" >&2
    exit 1
  fi
  if ! grep -Fq "${expected}" "${test_root}/output"; then
    cat "${test_root}/output" >&2
    exit 1
  fi
}

assert_published() {
  local version="$1" commit="$2"
  test "$(git cat-file -t "${version}")" = tag
  test "$(git rev-parse "${version}^{commit}")" = "${commit}"
  test "$(git --git-dir="$(git remote get-url origin)" rev-parse "${version}^{commit}")" = "${commit}"
  grep -Fq -- "--workflow ci.yml --branch main --event push --commit ${commit}" "${GH_STATE}/runs"
  test -s "${GH_STATE}/release"
}

new_repo invalid
reject 'version must be' v1.0.0
reject 'version must be' v2.01.0
reject 'version must be' v2.9.0-alpha.01
new_repo branch
git switch --quiet -c feature
reject 'clean main checkout' v2.9.0
git switch --quiet --detach
reject 'clean main checkout' v2.9.0
for state in unstaged staged untracked; do
  new_repo "${state}"
  case "${state}" in
    unstaged) echo changed >>content ;;
    staged) echo changed >>content; git add content ;;
    untracked) echo new >untracked ;;
  esac
  reject 'clean main checkout' v2.9.0
done

# A fresh release pushes committed main without a branch or a PR.
new_repo ahead
git commit --quiet --allow-empty -m ahead
candidate="$(git rev-parse HEAD)"
export CI_RESULT=delayed
publish v2.9.0
assert_published v2.9.0 "${candidate}"
test "$(git --git-dir="$(git remote get-url origin)" rev-parse main)" = "${candidate}"
test "$(git branch --list | wc -l | tr -d ' ')" = 1

new_repo push_rejected
git commit --quiet --allow-empty -m ahead
cat >"$(git remote get-url origin)/hooks/update" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$(git remote get-url origin)/hooks/update"
reject 'failed to push' v2.9.0
test -z "$(git tag --list)"
test ! -f "${GH_STATE}/runs"
for state in behind diverged; do
  new_repo "${state}"
  next="$(printf 'remote advance\n' | git commit-tree 'HEAD^{tree}' -p HEAD)"
  git push --quiet origin "${next}:refs/heads/main"
  if [[ "${state}" == diverged ]]; then
    git commit --quiet --allow-empty -m local
  fi
  reject 'main has advanced or diverged' v2.9.0
  test -z "$(git tag --list)"
done
for result in 17:completed:failure 17:completed:cancelled 17:in_progress: missing; do
  new_repo "ci-${result//:/-}"
  export CI_RESULT="${result}"
  reject 'CI' v2.9.0
  test -z "$(git tag --list)"
  test ! -f "${GH_STATE}/creates"
done

# Never repoint or silently adopt lightweight/conflicting tags.
new_repo lightweight
git tag v2.9.0
reject 'annotated tag' v2.9.0
new_repo conflict
git tag -a v2.9.0 -m remote
git push --quiet origin refs/tags/v2.9.0
git tag -d v2.9.0 >/dev/null
git tag -a v2.9.0 -m different
reject 'local and remote tags differ' v2.9.0
new_repo unrelated_tag
other="$(printf 'unrelated\n' | git commit-tree 'HEAD^{tree}')"
git tag -a v2.9.0 "${other}" -m unrelated
reject 'not on remote main' v2.9.0

# Simulate a process stopping between tag creation and push.
new_repo local_tag_resume
git tag -a v2.9.0 -m v2.9.0
publish v2.9.0
assert_published v2.9.0 "$(git rev-parse HEAD)"

new_repo tag_push_rejected
cat >"$(git remote get-url origin)/hooks/update" <<'EOF'
#!/bin/sh
case "$1" in refs/tags/*) exit 1 ;; esac
EOF
chmod +x "$(git remote get-url origin)/hooks/update"
reject 'failed to push' v2.9.0
test -z "$(git ls-remote --tags origin refs/tags/v2.9.0)"
test ! -f "${GH_STATE}/creates"
rm "$(git remote get-url origin)/hooks/update"
publish v2.9.0
assert_published v2.9.0 "$(git rev-parse HEAD)"

new_repo resumed_failed_ci
git tag -a v2.9.0 -m v2.9.0
git push --quiet origin refs/tags/v2.9.0
export CI_RESULT=17:completed:failure
reject 'CI did not pass' v2.9.0
test ! -f "${GH_STATE}/creates"
# A lost push response leaves the same durable state as a completed tag push.
new_repo remote_tag_resume
candidate="$(git rev-parse HEAD)"
git tag -a v2.9.0 -m v2.9.0
git push --quiet origin refs/tags/v2.9.0
git tag -d v2.9.0 >/dev/null
git commit --quiet --allow-empty -m later
git push --quiet origin main
publish v2.9.0
assert_published v2.9.0 "${candidate}"
new_repo failed_release
export CREATE_RESULT=before
reject 'release could not be verified' v2.9.0
tag="$(git rev-parse v2.9.0)"
export CREATE_RESULT=success
publish v2.9.0
test "$(git rev-parse v2.9.0)" = "${tag}"
new_repo lost_release_response
export CREATE_RESULT=after
publish v2.9.0
assert_published v2.9.0 "$(git rev-parse HEAD)"
new_repo concurrent_main
candidate="$(git rev-parse HEAD)"
export ADVANCE_MAIN=true
publish v2.9.0
assert_published v2.9.0 "${candidate}"
test "$(git --git-dir="$(git remote get-url origin)" rev-parse main)" != "${candidate}"

new_repo concurrent_tag
export CONFLICT_TAG=true
reject 'local and remote tags differ' v2.9.0
test ! -f "${GH_STATE}/creates"
test "$(git --git-dir="$(git remote get-url origin)" rev-parse v2.9.0)" = "$(git rev-parse competing)"
for version in v2.9.0 v2.9.0-alpha.1; do
  new_repo "${version}"
  publish "${version}"
  assert_published "${version}" "$(git rev-parse HEAD)"
  if [[ "${version}" == *-* ]]; then
    grep -Fq -- '--prerelease --latest=false' "${GH_STATE}/creates"
  else
    grep -Fq -- '--latest' "${GH_STATE}/creates"
  fi
  publish "${version}"
  test "$(wc -l <"${GH_STATE}/creates" | tr -d ' ')" = 1
done
for metadata in true:false:url false:true:url; do
  new_repo "metadata-${metadata//:/-}"
  git tag -a v2.9.0 -m v2.9.0
  git push --quiet origin refs/tags/v2.9.0
  printf '%s\n' "${metadata}" >"${GH_STATE}/release"
  reject 'draft/prerelease metadata' v2.9.0
done
echo 'direct-main release, CI, immutable tags, and recovery scenarios passed'

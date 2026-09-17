#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
publisher="${script_dir}/release.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0
export CI_RESULT=completed:success
export GH_LOG="${test_root}/gh.log"
export GH_RUN_LOG="${test_root}/gh-run.log"
mkdir "${test_root}/bin"
cat >"${test_root}/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  'repo view') echo test/repository ;;
  'run list')
    printf '%s\n' "$*" >"${GH_RUN_LOG}"
    if [[ "${ADVANCE_MAIN:-}" == true ]]; then
      next="$(printf 'concurrent advance\n' | git commit-tree 'HEAD^{tree}' -p HEAD)"
      git push --quiet origin "${next}:refs/heads/main"
    fi
    printf '%s\n' "${CI_RESULT}"
    ;;
  'release create') printf '%s\n' "$*" >>"${GH_LOG}" ;;
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
  : >"${GH_LOG}"
  export CI_RESULT=completed:success
  export ADVANCE_MAIN=false
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
  test ! -s "${GH_LOG}"
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

new_repo ahead
git commit --quiet --allow-empty -m ahead
reject 'HEAD must equal remote main' v2.9.0

new_repo behind
new_head="$(printf 'remote advance\n' | git commit-tree 'HEAD^{tree}' -p HEAD)"
git push --quiet origin "${new_head}:refs/heads/main"
reject 'HEAD must equal remote main' v2.9.0

for result in completed:failure in_progress: completed:cancelled ''; do
  new_repo "ci-${result//:/-}"
  export CI_RESULT="${result}"
  reject 'successful CI push run' v2.9.0
  test -z "$(git tag --list)"
done

new_repo local_tag
git tag v2.9.0
reject 'tag already exists' v2.9.0

new_repo remote_tag
git push --quiet origin HEAD:refs/tags/v2.9.0
reject 'tag already exists' v2.9.0

new_repo concurrent_main
export ADVANCE_MAIN=true
if bash "${publisher}" v2.9.0 >"${test_root}/output" 2>&1; then
  echo 'release accepted a concurrent main advance' >&2
  exit 1
fi
test -z "$(git ls-remote --tags origin refs/tags/v2.9.0)"
test ! -s "${GH_LOG}"

for version in v2.9.0 v2.9.0-alpha.1; do
  new_repo "${version}"
  head="$(git rev-parse HEAD)"
  bash "${publisher}" "${version}"
  grep -Fq -- "--workflow ci.yml --branch main --event push --commit ${head}" "${GH_RUN_LOG}"
  test "$(git cat-file -t "${version}")" = tag
  test "$(git rev-parse "${version}^{commit}")" = "${head}"
  test "$(git --git-dir="${test_root}/${version}.git" rev-parse "${version}^{commit}")" = "${head}"
  test "$(git --git-dir="${test_root}/${version}.git" rev-parse main)" = "${head}"
  if [[ "${version}" == *-* ]]; then
    grep -Fq -- '--prerelease --latest=false' "${GH_LOG}"
  else
    grep -Fq -- '--latest' "${GH_LOG}"
  fi
done

echo 'release guards and stable/prerelease publication passed'

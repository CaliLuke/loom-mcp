#!/usr/bin/env bash
set -euo pipefail

staticcheck_version="${1:?staticcheck version is required}"
tools_version="${2:?x/tools version is required}"
go_command="${GO:-go}"
if binary="$(command -v staticcheck)"; then
  metadata="$("${go_command}" version -m "${binary}")"
  if awk -v version="${staticcheck_version}" '$1 == "mod" && $2 == "honnef.co/go/tools" && $3 == version { found = 1 } END { exit !found }' <<<"${metadata}" &&
     awk -v version="${tools_version}" '$1 == "dep" && $2 == "golang.org/x/tools" && $3 == version { found = 1 } END { exit !found }' <<<"${metadata}"; then
    printf 'staticcheck %s with x/tools %s found: %s\n' "${staticcheck_version}" "${tools_version}" "${binary}"
    exit 0
  fi
fi

build_dir="$(mktemp -d)"
trap 'rm -rf "${build_dir}"' EXIT
export GOWORK=off
"${go_command}" -C "${build_dir}" mod init loom.tools/staticcheck
"${go_command}" -C "${build_dir}" get "honnef.co/go/tools/cmd/staticcheck@${staticcheck_version}" "golang.org/x/tools@${tools_version}"
"${go_command}" -C "${build_dir}" install honnef.co/go/tools/cmd/staticcheck

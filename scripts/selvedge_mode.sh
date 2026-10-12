#!/usr/bin/env bash
# Select the Selvedge dependency used by the root and agent_features modules.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SELVEDGE_PATH="${SELVEDGE_DIR:-${ROOT_DIR}/../../selvedge}"
MODULE="github.com/CaliLuke/selvedge"
REMOTE_VERSION="v0.0.0-20261012194623-a33adedea054"
MODULE_DIRS=("${ROOT_DIR}" "${ROOT_DIR}/integration_tests/fixtures/agent_features")

status() {
  local directory selection failed=0
  for directory in "${MODULE_DIRS[@]}"; do
    selection="$(cd "$directory" && GOWORK=off go list -m -f '{{if .Replace}}local:{{.Replace.Dir}}{{else}}remote:{{.Version}}{{end}}' "$MODULE")"
    printf '%s: %s\n' "$directory" "$selection"
    if [[ "$selection" != "remote:${REMOTE_VERSION}" ]]; then
      failed=1
    fi
  done
  return "$failed"
}

case "${1:-status}" in
  local)
    if [[ ! -f "${SELVEDGE_PATH}/go.mod" ]]; then
      printf 'Selvedge go.mod not found: %s\n' "$SELVEDGE_PATH" >&2
      exit 1
    fi
    SELVEDGE_PATH="$(cd "$SELVEDGE_PATH" && pwd -P)"
    for directory in "${MODULE_DIRS[@]}"; do
      (cd "$directory" && GOWORK=off go mod edit -replace "${MODULE}=${SELVEDGE_PATH}" && GOWORK=off go mod tidy)
    done
    ;;
  remote)
    for directory in "${MODULE_DIRS[@]}"; do
      (cd "$directory" && GOWORK=off go mod edit -dropreplace "$MODULE" && GOWORK=off go get "${MODULE}@${REMOTE_VERSION}" && GOWORK=off go mod tidy)
    done
    status
    ;;
  status)
    status
    ;;
  *)
    printf 'Usage: %s local|remote|status\n' "$0" >&2
    exit 2
    ;;
esac

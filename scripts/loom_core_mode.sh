#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${ROOT_DIR}/integration_tests/fixtures/assistant"
REMOTE_VERSION="v1.0.8"
LOCAL_LOOM_DIR="${LOOM_DIR:-/Users/luca/code/loom-mono/loom}"

usage() {
  cat <<EOF
Usage: $(basename "$0") <local|remote|status>

Modes:
  local   Point both modules at the local Loom checkout (${LOCAL_LOOM_DIR} by default)
  remote  Restore the pinned Loom release (${REMOTE_VERSION})
  status  Print the current Loom source in both modules

Environment:
  LOOM_DIR   Override the local Loom checkout path used by local mode
EOF
}

set_local() {
  if [[ ! -f "${LOCAL_LOOM_DIR}/go.mod" ]]; then
    echo "local Loom checkout not found at ${LOCAL_LOOM_DIR}" >&2
    exit 1
  fi

  (
    cd "${ROOT_DIR}"
    go mod edit -replace=github.com/CaliLuke/loom="${LOCAL_LOOM_DIR}"
    go mod tidy
  )

  (
    cd "${FIXTURE_DIR}"
    go mod edit -replace=github.com/CaliLuke/loom="${LOCAL_LOOM_DIR}"
    go mod tidy
  )
}

set_remote() {
  (
    cd "${ROOT_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    go mod tidy
  )

  (
    cd "${FIXTURE_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    go mod tidy
  )
}

show_module_status() {
  local go_mod="$1"
  grep '^replace github.com/CaliLuke/loom => ' "${go_mod}" || echo "github.com/CaliLuke/loom ${REMOTE_VERSION} (remote)"
}

show_status() {
  echo "root:"
  show_module_status "${ROOT_DIR}/go.mod"
  echo "fixture:"
  show_module_status "${FIXTURE_DIR}/go.mod"
}

main() {
  if [[ $# -ne 1 ]]; then
    usage >&2
    exit 1
  fi

  case "$1" in
    local)
      set_local
      ;;
    remote)
      set_remote
      ;;
    status)
      show_status
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
}

main "$@"

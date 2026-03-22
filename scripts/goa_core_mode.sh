#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${ROOT_DIR}/integration_tests/fixtures/assistant"
REMOTE_VERSION="v3.25.4-0.20260322010145-60eb0338caae"
LOCAL_GOA_DIR="${GOA_DIR:-/Users/luca/code/goa-light}"

usage() {
  cat <<EOF
Usage: $(basename "$0") <local|remote|status>

Modes:
  local   Point both modules at the local Goa checkout (${LOCAL_GOA_DIR} by default)
  remote  Restore the pinned fork version (${REMOTE_VERSION})
  status  Print the current replace target in both modules

Environment:
  GOA_DIR   Override the local Goa checkout path used by local mode
EOF
}

set_local() {
  if [[ ! -f "${LOCAL_GOA_DIR}/go.mod" ]]; then
    echo "local Goa checkout not found at ${LOCAL_GOA_DIR}" >&2
    exit 1
  fi

  (
    cd "${ROOT_DIR}"
    go mod edit -replace=goa.design/goa/v3="${LOCAL_GOA_DIR}"
    go mod tidy
  )

  (
    cd "${FIXTURE_DIR}"
    go mod edit -replace=goa.design/goa/v3="${LOCAL_GOA_DIR}"
    go mod tidy
  )
}

set_remote() {
  (
    cd "${ROOT_DIR}"
    go mod edit -replace=goa.design/goa/v3=github.com/CaliLuke/goa/v3@"${REMOTE_VERSION}"
    go get goa.design/goa/v3@"${REMOTE_VERSION}"
    go mod tidy
  )

  (
    cd "${FIXTURE_DIR}"
    go mod edit -replace=goa.design/goa/v3=github.com/CaliLuke/goa/v3@"${REMOTE_VERSION}"
    go get goa.design/goa/v3@"${REMOTE_VERSION}"
    go mod tidy
  )
}

show_status() {
  echo "root:"
  grep '^replace goa.design/goa/v3' "${ROOT_DIR}/go.mod"
  echo "fixture:"
  grep '^replace goa.design/goa/v3' "${FIXTURE_DIR}/go.mod"
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

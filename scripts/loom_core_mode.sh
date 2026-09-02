#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${ROOT_DIR}/integration_tests/fixtures/assistant"
QUICKSTART_DIR="${ROOT_DIR}/quickstart"
REMOTE_VERSION="v1.9.0-alpha.10"
# Default to the loom checkout that lives as a peer of this repo (loom-mono
# layout); override with LOOM_DIR when the checkout is elsewhere.
LOCAL_LOOM_DIR="${LOOM_DIR:-${ROOT_DIR}/../loom}"

sync_quickstart_generator_dependencies() {
  (
    cd "${QUICKSTART_DIR}"
    # go mod tidy cannot see dependencies imported only by the Loom CLI invoked
    # through go run. Resolve that package explicitly so switching modes leaves
    # the quickstart in the same module state as regeneration.
    GOWORK=off go list -mod=mod -deps github.com/CaliLuke/loom/cmd/loom >/dev/null
  )
}

usage() {
  cat <<EOF
Usage: $(basename "$0") <local|remote|status|remote-version>

Modes:
  local   Point repo modules at the local Loom checkout (${LOCAL_LOOM_DIR} by default)
  remote  Restore the pinned Loom release (${REMOTE_VERSION})
  status  Print the current Loom source in repo modules
  remote-version  Print the canonical remote Loom release tag

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

  (
    cd "${QUICKSTART_DIR}"
    go mod edit -replace=github.com/CaliLuke/loom="${LOCAL_LOOM_DIR}"
    GOWORK=off go mod tidy
  )
  sync_quickstart_generator_dependencies
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

  (
    cd "${QUICKSTART_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    GOWORK=off go mod tidy
  )
  sync_quickstart_generator_dependencies
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
  echo "quickstart:"
  show_module_status "${QUICKSTART_DIR}/go.mod"
}

show_remote_version() {
  printf '%s\n' "${REMOTE_VERSION}"
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
    remote-version)
      show_remote_version
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
}

main "$@"

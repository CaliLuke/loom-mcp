#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_DIR="${ROOT_DIR}/integration_tests/fixtures/assistant"
AGENT_FIXTURE_DIR="${ROOT_DIR}/integration_tests/fixtures/agent_features"
CONSUMER_DIR="${ROOT_DIR}/integration_tests/fixtures/sdkbridge_consumer"
QUICKSTART_DIR="${ROOT_DIR}/quickstart"
REMOTE_VERSION="v1.9.0-alpha.14"
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
  LOCAL_LOOM_DIR="$(cd "${LOCAL_LOOM_DIR}" && pwd -P)"
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
    cd "${AGENT_FIXTURE_DIR}"
    go mod edit -replace=github.com/CaliLuke/loom="${LOCAL_LOOM_DIR}"
    GOWORK=off go mod tidy
  )
  (
    cd "${CONSUMER_DIR}"
    go mod edit -replace=github.com/CaliLuke/loom="${LOCAL_LOOM_DIR}"
    GOWORK=off go mod tidy
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
    cd "${AGENT_FIXTURE_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    GOWORK=off go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    GOWORK=off go mod tidy
  )
  (
    cd "${CONSUMER_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    GOWORK=off go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    GOWORK=off go mod tidy
  )

  (
    cd "${QUICKSTART_DIR}"
    go mod edit -dropreplace=github.com/CaliLuke/loom || true
    GOWORK=off go get github.com/CaliLuke/loom@"${REMOTE_VERSION}"
    GOWORK=off go mod tidy
  )
  sync_quickstart_generator_dependencies
  verify_remote_modules
}

module_selection() {
  (
    cd "$1"
    GOWORK=off go list -m -f '{{if .Replace}}local {{.Replace.Dir}}{{else}}remote {{.Version}}{{end}}' github.com/CaliLuke/loom
  )
}

show_module_status() {
  local label="$1"
  local directory="$2"
  local selection
  selection="$(module_selection "${directory}")"
  printf '%s:\n' "${label}"
  case "${selection}" in
    "remote ${REMOTE_VERSION}") printf 'github.com/CaliLuke/loom %s (remote)\n' "${REMOTE_VERSION}" ;;
    remote\ *)
      printf 'github.com/CaliLuke/loom %s (unexpected remote; want %s)\n' "${selection#remote }" "${REMOTE_VERSION}" >&2
      return 1
      ;;
    local\ *) printf 'github.com/CaliLuke/loom => %s (local)\n' "${selection#local }" ;;
    *) printf 'cannot determine Loom module selection for %s\n' "${directory}" >&2; return 1 ;;
  esac
}

verify_remote_modules() {
  local directory
  for directory in "${ROOT_DIR}" "${FIXTURE_DIR}" "${AGENT_FIXTURE_DIR}" "${CONSUMER_DIR}" "${QUICKSTART_DIR}"; do
    local selection
    selection="$(module_selection "${directory}")"
    if [[ "${selection}" != "remote ${REMOTE_VERSION}" ]]; then
      printf 'unexpected Loom selection in %s: %s; want remote %s\n' "${directory}" "${selection}" "${REMOTE_VERSION}" >&2
      return 1
    fi
  done
}

show_status() {
  show_module_status "root" "${ROOT_DIR}"
  show_module_status "assistant fixture" "${FIXTURE_DIR}"
  show_module_status "agent-feature fixture" "${AGENT_FIXTURE_DIR}"
  show_module_status "sdkbridge consumer" "${CONSUMER_DIR}"
  show_module_status "quickstart" "${QUICKSTART_DIR}"
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

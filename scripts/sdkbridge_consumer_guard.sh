#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_FILE="runtime/mcp/sdkbridge/bridge.go"
CONSUMER_DIR="integration_tests/fixtures/sdkbridge_consumer"
CONSUMER_SERVER="${CONSUMER_DIR}/gen/mcp_consumer/sdk_server.go"
CONSUMER_SNAPSHOT_PATHS=("${CONSUMER_DIR}/design" "${CONSUMER_DIR}/gen")

runtime_version() {
  sed -nE 's/^const CompatibilityVersion = ([0-9]+)$/\1/p' "$1" | head -n 1
}

runtime_version_at() {
  { git show "$1:${RUNTIME_FILE}" 2>/dev/null || true; } | sed -nE 's/^const CompatibilityVersion = ([0-9]+)$/\1/p' | head -n 1
}
comparison_base() {
  local base="${VERIFY_GENERATED_BASE_REF:-}"
  if [[ -z "${base}" ]]; then
    local upstream
    upstream="$(git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)"
    if [[ -n "${upstream}" ]]; then
      base="$(git merge-base HEAD "${upstream}")"
      if [[ -z "$(runtime_version_at "${base}")" ]]; then
        base=""
      fi
    fi
    if [[ -z "${base}" || "$(git rev-parse "${base}^{commit}" 2>/dev/null || true)" == "$(git rev-parse HEAD)" ]]; then
      local release_tag
      release_tag="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || true)"
      if [[ -n "${release_tag}" && -n "$(runtime_version_at "${release_tag}")" ]]; then
        base="${release_tag}"
      else
        local snapshot_commit
        snapshot_commit="$(git log -1 --format=%H HEAD -- "${CONSUMER_SNAPSHOT_PATHS[@]}")"
        base="$(git rev-parse "${snapshot_commit}^" 2>/dev/null || true)"
        if [[ -z "${base}" || -z "$(runtime_version_at "${base}")" ]]; then
          base=""
        fi
      fi
    fi
  fi
  if [[ -z "${base}" ]]; then
    echo "cannot determine sdkbridge consumer comparison base; set VERIFY_GENERATED_BASE_REF" >&2
    exit 1
  fi
  if ! git rev-parse --verify "${base}^{commit}" >/dev/null 2>&1; then
    echo "sdkbridge consumer comparison base ${base} is unavailable" >&2
    exit 1
  fi
  printf '%s\n' "${base}"
}
generated_version() {
  sed -nE 's/^[[:space:]]*CompatibilityVersion:[[:space:]]*([0-9]+),$/\1/p' "$1" | head -n 1
}

snapshot_has_untracked_files() {
  local paths_file
  paths_file="$(mktemp)"
  if ! git ls-files --others --exclude-standard -- "${CONSUMER_SNAPSHOT_PATHS[@]}" >"${paths_file}"; then
    rm -f "${paths_file}"
    echo "cannot enumerate untracked sdkbridge consumer files" >&2
    return 2
  fi
  if [[ -s "${paths_file}" ]]; then
    rm -f "${paths_file}"
    return 0
  fi
  rm -f "${paths_file}"
  return 1
}

snapshot_changed() {
  local base="$1"
  local status
  if git diff --quiet "${base}" -- "${CONSUMER_SNAPSHOT_PATHS[@]}"; then
    status=0
  else
    status=$?
  fi
  if (( status > 1 )); then
    echo "cannot compare sdkbridge consumer snapshot with ${base}" >&2
    return 2
  fi
  if (( status == 1 )); then
    return 0
  fi
  if snapshot_has_untracked_files; then
    return 0
  fi
  status=$?
  if (( status > 1 )); then
    return "${status}"
  fi
  return 1
}

require_epoch_increment() {
  local base="$1"
  local current="$2"
  local previous
  previous="$(runtime_version_at "${base}")"
  if [[ -z "${previous}" ]]; then
    echo "cannot determine sdkbridge compatibility version at ${base}" >&2
    exit 1
  fi
  if (( current <= previous )); then
    echo "sdkbridge consumer snapshot changed without a compatibility version increment (${previous} -> ${current})" >&2
    exit 1
  fi
}

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <base|changed|regen|verify>" >&2
  exit 2
fi
cd "${ROOT_DIR}"
runtime="$(runtime_version "${RUNTIME_FILE}")"
if [[ -z "${runtime}" ]]; then
  echo "cannot determine sdkbridge runtime compatibility version" >&2
  exit 1
fi

case "${1:-}" in
  base)
    comparison_base
    ;;
  changed)
    base="$(comparison_base)"
    snapshot_changed "${base}"
    ;;
  regen)
    base="$(comparison_base)"
    require_epoch_increment "${base}" "${runtime}"
    ;;
  verify)
    generated="$(generated_version "${CONSUMER_SERVER}")"
    if [[ -z "${generated}" ]]; then
      echo "cannot determine generated sdkbridge compatibility version" >&2
      exit 1
    fi
    if [[ "${runtime}" != "${generated}" ]]; then
      echo "sdkbridge consumer compatibility version ${generated} does not match runtime version ${runtime}" >&2
      exit 1
    fi

    base="$(comparison_base)"
    if snapshot_changed "${base}"; then
      require_epoch_increment "${base}" "${runtime}"
    else
      status=$?
      if (( status > 1 )); then
        exit "${status}"
      fi
    fi
    ;;
  *)
    echo "usage: $0 <base|changed|regen|verify>" >&2
    exit 2
    ;;
esac

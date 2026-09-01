#!/usr/bin/env bash
set -euo pipefail

profile="${1:-cover.out}"
if [[ ! -f "${profile}" ]]; then
  echo "coverage profile not found: ${profile}" >&2
  exit 1
fi

check_group() {
  local name="$1"
  local pattern="$2"
  local minimum="$3"
  local result

  result="$({
    awk -v pattern="${pattern}" '
      NR == 1 { next }
      {
        split($1, location, ":")
        path = location[1]
        statements = $2
        count = $3
        if (path ~ /\/gen\// || path ~ /\/mocks\// || path ~ /\/design\//) {
          next
        }
        if (path !~ pattern) {
          next
        }
        total += statements
        if (count > 0) {
          covered += statements
        }
      }
      END {
        if (total == 0) {
          exit 2
        }
        printf "%.1f %d %d", 100 * covered / total, covered, total
      }
    ' "${profile}"
  } || {
    echo "coverage group ${name} matched no statements" >&2
    exit 1
  })"

  local actual covered total
  read -r actual covered total <<<"${result}"
  awk -v name="${name}" -v actual="${actual}" -v minimum="${minimum}" -v covered="${covered}" -v total="${total}" 'BEGIN {
    if (actual + 0 < minimum + 0) {
      printf "%s coverage %.1f%% (%d/%d) is below required %.1f%%\n", name, actual, covered, total, minimum
      exit 1
    }
    printf "%s coverage %.1f%% (%d/%d) meets required %.1f%%\n", name, actual, covered, total, minimum
  }'
}

check_group "runtime" '/runtime/' "${COVERAGE_RUNTIME_MIN:-75.0}"
check_group "mcp" '/(codegen/mcp|runtime/mcp)/' "${COVERAGE_MCP_MIN:-78.0}"
check_group "codegen" '/codegen/' "${COVERAGE_CODEGEN_MIN:-70.0}"
check_group "temporal" '/(runtime/agent/engine/temporal|runtime/temporaltrace)/' "${COVERAGE_TEMPORAL_MIN:-73.0}"
check_group "registry-pulse" '/(registry|runtime/registry|features/stream/pulse)/' "${COVERAGE_REGISTRY_PULSE_MIN:-58.0}"
check_group "providers" '/features/model/(anthropic|bedrock|gateway|gemini|middleware|ollama|openai)/' "${COVERAGE_PROVIDERS_MIN:-70.0}"

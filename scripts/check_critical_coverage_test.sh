#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
checker="${script_dir}/check_critical_coverage.sh"
profile="$(mktemp)"
output="$(mktemp)"
trap 'rm -f "${profile}" "${output}"' EXIT

cat >"${profile}" <<'EOF'
mode: atomic
github.com/CaliLuke/loom-mcp/v2/runtime/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/runtime/mcp/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/codegen/mcp/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/runtime/registry/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/features/model/openai/core.go:1.1,1.2 1 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/runtime/agent/model/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/features/model/bedrock/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/features/model/gemini/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/owner.go:1.1,1.2 1 0
github.com/CaliLuke/loom-mcp/v2/registry/owner.go:1.1,1.2 1 0
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/runtime/agent/model/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/features/model/bedrock/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/features/model/gemini/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/decoy.go:1.1,1.2 99 1
example.com/decoy/github.com/CaliLuke/loom-mcp/v2/registry/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/model/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/model/bedrock/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/model/gemini/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/registry/ownerXgo:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/runtime/agent/model/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/model/bedrock/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/model/gemini/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/nested/decoy.go:1.1,1.2 99 1
github.com/CaliLuke/loom-mcp/v2/registry/nested/decoy.go:1.1,1.2 99 1
EOF

expect_floor_failure() {
  local name="$1"
  local mode="$2"
  shift 2

  if env "$@" bash "${checker}" "${profile}" "${mode}" >"${output}" 2>&1; then
    echo "coverage parser accepted decoy coverage for ${name}" >&2
    return 1
  fi
  if ! grep -Fq "${name} coverage 0.0% (0/1) is below required 50.0%" "${output}"; then
    echo "coverage parser did not isolate ${name}" >&2
    cat "${output}" >&2
    return 1
  fi
}

critical_floors=(
  COVERAGE_RUNTIME_MIN=0
  COVERAGE_MCP_MIN=0
  COVERAGE_CODEGEN_MIN=0
  COVERAGE_TEMPORAL_MIN=0
  COVERAGE_REGISTRY_PULSE_MIN=0
  COVERAGE_PROVIDERS_MIN=0
)

expect_floor_failure agent-runtime critical "${critical_floors[@]}" COVERAGE_AGENT_RUNTIME_MIN=50 COVERAGE_MODEL_MIN=0 COVERAGE_BEDROCK_MIN=0 COVERAGE_GEMINI_MIN=0
expect_floor_failure model critical "${critical_floors[@]}" COVERAGE_AGENT_RUNTIME_MIN=0 COVERAGE_MODEL_MIN=50 COVERAGE_BEDROCK_MIN=0 COVERAGE_GEMINI_MIN=0
expect_floor_failure bedrock critical "${critical_floors[@]}" COVERAGE_AGENT_RUNTIME_MIN=0 COVERAGE_MODEL_MIN=0 COVERAGE_BEDROCK_MIN=50 COVERAGE_GEMINI_MIN=0
expect_floor_failure gemini critical "${critical_floors[@]}" COVERAGE_AGENT_RUNTIME_MIN=0 COVERAGE_MODEL_MIN=0 COVERAGE_BEDROCK_MIN=0 COVERAGE_GEMINI_MIN=50

expect_floor_failure docker-mongo docker COVERAGE_DOCKER_MONGO_MIN=50 COVERAGE_DOCKER_PULSE_MIN=0 COVERAGE_DOCKER_REGISTRY_MIN=0
expect_floor_failure docker-pulse docker COVERAGE_DOCKER_MONGO_MIN=0 COVERAGE_DOCKER_PULSE_MIN=50 COVERAGE_DOCKER_REGISTRY_MIN=0
expect_floor_failure docker-registry docker COVERAGE_DOCKER_MONGO_MIN=0 COVERAGE_DOCKER_PULSE_MIN=0 COVERAGE_DOCKER_REGISTRY_MIN=50

echo "coverage owner patterns reject prefix, extension, and nested-package decoys"

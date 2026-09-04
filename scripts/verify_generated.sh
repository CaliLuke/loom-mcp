#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GENERATED_PATHS=(
  go.mod
  go.sum
  go.work
  go.work.sum
  quickstart
  registry/gen
  integration_tests/fixtures/assistant
  integration_tests/fixtures/agent_features
  integration_tests/fixtures/sdkbridge_consumer
)

bash "${ROOT_DIR}/scripts/sdkbridge_consumer_guard.sh" verify

before_state="$(mktemp)"
after_state="$(mktemp)"
untracked_paths="$(mktemp)"
trap 'rm -f "${before_state}" "${after_state}" "${untracked_paths}"' EXIT INT TERM

capture_state() {
  git -C "${ROOT_DIR}" diff --binary HEAD -- "${GENERATED_PATHS[@]}"
  if ! git -C "${ROOT_DIR}" ls-files --others --exclude-standard -z -- "${GENERATED_PATHS[@]}" >"${untracked_paths}"; then
    echo "cannot enumerate untracked generated files" >&2
    return 1
  fi
  while IFS= read -r -d '' path; do
    checksum="$(git -C "${ROOT_DIR}" hash-object -- "${path}")"
    printf 'untracked %s %s\n' "${checksum}" "${path}"
  done <"${untracked_paths}"
}

capture_state >"${before_state}"

make -C "${ROOT_DIR}" gen-registry
make -C "${ROOT_DIR}" regen-quickstart
make -C "${ROOT_DIR}" regen-assistant-fixture
make -C "${ROOT_DIR}" regen-progressive-discovery-fixture
make -C "${ROOT_DIR}" regen-agent-feature-fixture

if bash "${ROOT_DIR}/scripts/sdkbridge_consumer_guard.sh" changed; then
  make -C "${ROOT_DIR}" regen-sdkbridge-consumer-fixture
else
  consumer_status=$?
  if (( consumer_status > 1 )); then
    exit "${consumer_status}"
  fi
fi

capture_state >"${after_state}"

if cmp -s "${before_state}" "${after_state}"; then
  echo "generated outputs are current"
  exit 0
fi

echo "generated outputs changed during verification" >&2
diff -u "${before_state}" "${after_state}" >&2 || true
git -C "${ROOT_DIR}" status --short -- "${GENERATED_PATHS[@]}" >&2
exit 1

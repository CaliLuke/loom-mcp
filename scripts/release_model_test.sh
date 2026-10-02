#!/usr/bin/env bash
set -euo pipefail

jar="${1:-}"
if [[ ! -f "${jar}" ]]; then
  echo 'usage: make release-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar' >&2
  exit 1
fi
jar="$(cd "$(dirname "${jar}")" && pwd)/$(basename "${jar}")"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
# Keep TLC trace artifacts out of the source tree, including expected failures.
cp "${root}"/docs/formal/Release*.tla "${root}"/docs/formal/Release*.cfg "${work}/"
cd "${work}"
for model in Release ReleaseFailures ReleasePrePushCI ReleaseNoResume; do
  result=0
  java -XX:+UseParallelGC -cp "${jar}" tlc2.TLC -workers 1 \
    -metadir "${work}/states-${model}" -config "${model}.cfg" Release.tla >"${model}.log" 2>&1 || result=$?
  case "${model}" in
    Release|ReleaseFailures)
      if [[ "${result}" != 0 ]]; then
        cat "${model}.log" >&2
        exit 1
      fi
      grep 'distinct states found' "${model}.log"
      printf '%s: configured checks passed\n' "${model}"
      ;;
    *)
      if [[ "${result}" != 13 ]] || ! grep -q 'Temporal properties were violated' "${model}.log"; then
        cat "${model}.log" >&2
        echo "${model}: expected the old workflow to violate progress" >&2
        exit 1
      fi
      printf '%s: reproduced expected progress failure\n' "${model}"
      ;;
  esac
done

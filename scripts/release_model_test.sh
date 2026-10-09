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
# Keep TLC traces outside the checkout, including expected counterexamples.
cp "${root}"/internal/release/tla/Publication.tla "${root}"/internal/release/tla/*.cfg "${work}/"
cd "${work}"
for model in Publication Unsafe; do
  result=0
  java -XX:+UseParallelGC -cp "${jar}" tlc2.TLC -workers 1 \
    -metadir "${work}/states-${model}" -config "${model}.cfg" Publication.tla >"${model}.log" 2>&1 || result=$?
  if [[ "${model}" == Publication ]]; then
    if [[ "${result}" != 0 ]]; then
      cat "${model}.log" >&2
      exit 1
    fi
    grep 'distinct states found' "${model}.log"
    echo 'Publication: selected-source invariants passed'
  else
    if [[ "${result}" != 12 ]] || ! grep -Eq 'Invariant (TestedSource|PinnedSource) is violated' "${model}.log"; then
      cat "${model}.log" >&2
      echo 'Unsafe: expected moving-main source counterexample' >&2
      exit 1
    fi
    echo 'Unsafe: reproduced expected moving-main source failure'
  fi
done

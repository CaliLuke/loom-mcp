#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cat >"$work/first" <<'PROFILE'
mode: atomic
example.com/module/a.go:1.1,3.2 2 0
example.com/module/a.go:4.1,7.2 3 5
PROFILE
cat >"$work/second" <<'PROFILE'
mode: atomic
example.com/module/a.go:1.1,3.2 2 4
example.com/module/a.go:4.1,7.2 3 0
example.com/module/b.go:1.1,2.2 1 0
PROFILE
cat >"$work/expected" <<'PROFILE'
mode: atomic
example.com/module/a.go:1.1,3.2 2 4
example.com/module/a.go:4.1,7.2 3 5
example.com/module/b.go:1.1,2.2 1 0
PROFILE
bash "$root/scripts/merge_coverage.sh" "$work/first" "$work/second" >"$work/actual"
cmp "$work/expected" "$work/actual"
bash "$root/scripts/merge_coverage.sh" "$work/second" "$work/first" "$work/first" >"$work/repeated"
cmp "$work/expected" "$work/repeated"

reject() {
  if bash "$root/scripts/merge_coverage.sh" "$work/first" "$work/invalid" >"$work/rejected" 2>"$work/error"; then
    echo "invalid profile accepted: $1" >&2
    exit 1
  fi
  if ! grep -q "$1" "$work/error"; then
    cat "$work/error" >&2
    exit 1
  fi
}
printf 'mode: count\n' >"$work/invalid"
reject 'expected atomic'
printf 'mode: atomic\n' >"$work/invalid"
reject 'no blocks'
printf 'mode: atomic\nexample.com/module/a.go:1.1,3.2 99 1\n' >"$work/invalid"
reject 'inconsistent statement count'
printf 'mode: atomic\nexample.com/module/a.go:1.1,3.2 2 -1\n' >"$work/invalid"
reject 'malformed coverage block'
printf 'mode: atomic\nexample.com/module/a.go:1.1,3.2 2 1 extra\n' >"$work/invalid"
reject 'malformed coverage block'
: >"$work/invalid"
reject 'missing or empty'
rm "$work/invalid"
reject 'missing or empty'
echo 'coverage union, stable denominator and invalid-input checks passed'

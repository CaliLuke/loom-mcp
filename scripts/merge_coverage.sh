#!/usr/bin/env bash
# Merge coverage from independent fast checks without double-counting statements.
set -euo pipefail

if (( $# == 0 )); then
  echo 'usage: merge_coverage.sh profile.out [profile.out ...]' >&2
  exit 1
fi
for profile in "$@"; do
  if [[ ! -s "$profile" ]]; then
    echo "missing or empty coverage profile: $profile" >&2
    exit 1
  fi
done

LC_ALL=C awk '
  function fail(message) {
    print FILENAME ":" FNR ": " message > "/dev/stderr"
    invalid = 1
    exit 1
  }
  FNR == 1 {
    if ($0 != "mode: atomic") {
      fail("expected atomic coverage profile")
    }
    profiles[FILENAME] = 1
    next
  }
  {
    if (NF != 3 || $1 !~ /:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/ || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/) {
      fail("malformed coverage block")
    }
    if ($1 in statements && statements[$1] != $2) {
      fail("inconsistent statement count for " $1)
    }
    statements[$1] = $2
    # Coverage is the union of executed blocks, not the sum of profile totals.
    if ($3 > counts[$1]) {
      counts[$1] = $3
    }
    blocks[FILENAME]++
  }
  END {
    if (invalid) {
      exit 1
    }
    if (length(blocks) != length(profiles)) {
      print "coverage profile contains no blocks" > "/dev/stderr"
      exit 1
    }
    for (block in statements) {
      printf "%s %d %.0f\n", block, statements[block], counts[block]
    }
  }
' "$@" | LC_ALL=C sort | { printf 'mode: atomic\n'; cat; }

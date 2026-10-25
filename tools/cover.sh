#!/usr/bin/env bash
# Coverage gate on hand-written code: drop generated files and examples/
# from a coverage profile, then fail if the total is below the bar.
# Usage: tools/cover.sh [profile] [min-percent]
set -euo pipefail

profile=${1:-coverage.out}
min=${2:-90}
hand=${profile%.out}.hand.out

generated=$(go run ./tools/generated ./...)
{
  head -n 1 "$profile"
  grep -v '^mode:' "$profile" |
    { if [ -n "$generated" ]; then grep -vF "$generated"; else cat; fi; } |
    grep -v '/examples/' || true
} >"$hand"

go tool cover -func="$hand" | awk -v min="$min" '
  /^total:/ {
    sub("%", "", $3)
    printf "coverage (hand-written code): %s%%\n", $3
    if ($3 + 0 < min) { printf "coverage %s%% < %s%%\n", $3, min; exit 1 }
  }'

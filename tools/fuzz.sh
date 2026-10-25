#!/usr/bin/env bash
# Run every Fuzz* target in the module for a while each (FUZZTIME, default
# 30s). go test can only fuzz one target at a time, so loop over them.
# Usage: FUZZTIME=30s tools/fuzz.sh
set -euo pipefail

fuzztime=${FUZZTIME:-30s}
found=0
for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    found=1
    echo "fuzz: $pkg $target ($fuzztime)"
    go test -run='^$' -fuzz="^${target}\$" -fuzztime="$fuzztime" "$pkg"
  done
done
if [ "$found" -eq 0 ]; then
  echo "fuzz: no Fuzz targets found" >&2
  exit 1
fi

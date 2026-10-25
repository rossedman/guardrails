#!/usr/bin/env bash
# One cobra command per file: fail if any non-test file in internal/cli
# defines more than one *cobra.Command. Helper files (app.go, ...) define
# none, and each command file holds one constructor.
# Usage: tools/check-cmds.sh [dir]
set -euo pipefail

dir=${1:-internal/cli}
fail=0
for f in "$dir"/*.go; do
  case "$f" in *_test.go) continue ;; esac
  n=$(grep -c '&cobra.Command{' "$f" || true)
  if [ "$n" -gt 1 ]; then
    echo "$f defines $n commands; give each its own file"
    fail=1
  fi
done
exit "$fail"

---
name: cover
description: Raise test coverage to the Guardrails threshold by finding uncovered functions and writing meaningful tests for them. Use when the coverage or patch-coverage check fails, or when the user asks to improve coverage.
---

# Close the coverage gap

1. Run `guardrails gate --explain` (or `--all` for the whole repo) and read
   the `[coverage]` and `[patch-coverage]` sections. They list the functions
   with the most uncovered statements and the exact changed lines that no
   test runs, as `file:line`.
2. For more detail, run
   `go test -coverprofile=/tmp/cover.out ./... && go tool cover -func=/tmp/cover.out`
   and sort by the lowest percentages.
3. Work from the biggest gap down. For each function:
   - Read it and list its behaviors, including error paths and edge cases.
   - Write table-driven tests (`t.Run(tc.name, ...)`) that assert outputs
     and errors. Every case must check something; tests that only execute
     code are caught by mutation testing.
   - Prefer testing through the public API. Introduce a small interface
     seam only when a dependency (network, clock, process) blocks testing.
4. Re-run the gate until coverage meets the threshold, then stop.

Do not exclude files from coverage, lower thresholds, or delete code to
raise the percentage.

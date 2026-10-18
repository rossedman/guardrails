---
name: harden
description: Check that tests actually test the code you changed — mutation-test the changed Go functions, briefly fuzz their fuzz targets, and fix what survives. Use before committing, after writing tests, when coverage is high but you doubt the tests, or when the user asks to harden, mutation-test or fuzz.
---

# Harden the tests for your changes

Coverage proves code ran, not that it was checked. `guardrails harden`
makes small bugs ("mutants") in the lines you changed — `<` to `<=`, `+` to
`-`, `&&` to `||` — and runs the package's tests against each. A mutant the
tests still pass with is a behavior nothing checks. It also briefly runs the
fuzz targets that cover your changes. Source files are never modified.

1. Run it. Compare against `HEAD` for uncommitted work, or against the
   branch base to review a whole branch:

   ```bash
   guardrails harden                 # changes since HEAD
   guardrails harden --base main     # everything on this branch
   ```

   Useful flags: `--budget 3m` for more mutants, `--no-fuzz`,
   `--fuzztime 30s`, `--json`.

2. Read the report.
   - `[mutation]` lists surviving mutants as
     `file:line  Func  `>` changed to `>=`, tests still pass`.
   - `[fuzz]` lists crashes, with the failing input saved under
     `testdata/fuzz/` — it is now a regression test; keep it.
   - `suggestions (not failures)` lists functions that look like hot paths
     or parsers without a benchmark or fuzz target.

3. Fix survivors by adding the test case that tells the original apart
   from the mutant — usually a boundary (`limit` itself), an empty or
   zero input, or an error branch. Change tests, not code, unless the
   mutant revealed a real bug. Never weaken or delete a test.

4. For a fuzz crash, fix the code so the saved input passes.

5. Treat suggestions as judgment calls: add a `Benchmark*` (with
   `b.ReportAllocs()`) or a `Fuzz*` target (with an `f.Add` seed corpus and
   real invariants) when the function is genuinely hot or parses untrusted
   input; otherwise say in one line why not. Don't add
   `// guardrails:nobench` or `// guardrails:nofuzz` yourself — those are
   the human's.

6. Re-run until there are no survivors or crashes, then summarize for the
   user: mutation score, what you added, and any suggestions you declined
   and why.

If the report says it sampled mutants to fit the budget, re-run with a
larger `--budget` before calling the work done.

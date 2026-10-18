# Hardening tests

Coverage proves code ran, not that it was tested: a test that calls a
function and asserts nothing reaches 100%. `guardrails harden` checks the
tests behind the Go code you changed, **on demand** — before a commit, after
writing tests, or whenever coverage looks too good to be true.

```bash
guardrails harden                 # changes since HEAD, including uncommitted work
guardrails harden --base main     # everything on this branch
```

In Claude Code, `/guardrails:harden` runs it, fixes what it finds, and
reports back. It is deliberately not part of the Stop gate: it takes longer
than a turn should, and its suggestions are judgment calls.

```text
guardrails harden: found 1 problem (2 changed functions vs HEAD; ran mutation, fuzz)

[mutation] mutation score 50% < 70% (2 of 4 mutants killed)
  calc/calc.go:14  Window  `>` changed to `>=`, tests still pass
  calc/calc.go:17  Window  `+` changed to `-`, tests still pass
  fix: add assertions that tell the original behavior apart from each surviving mutant

suggestions (not failures):
  calc/codec.go:17  DecodeFrame  could use a Fuzz target (parses []byte input and can fail)
```

## Mutation testing

`harden` makes small, deliberate bugs ("mutants") in the lines you changed
and runs the package's tests against each one. A mutant the tests catch is
**killed**; one they miss **lived** — a behavior nothing checks.

- **Built in.** There's nothing to install. Mutants are written to temporary
  files and swapped in with `go test -overlay`, so your source is never
  modified, even if the run is interrupted.
- **Operators.** Conditional boundaries (`<` ↔ `<=`), negated conditions
  (`==` ↔ `!=`), arithmetic (`+` ↔ `-`, `*` ↔ `/`), increments, logic
  (`&&` ↔ `||`), boolean literals, and removed negation (`-x`, `!x`).
- **No impossible survivors.** Boundary moves that can't change behavior
  are skipped: in `if v < lo { return lo }`, `<` and `<=` return the same
  value when `v == lo`, so no test could ever tell them apart.
- **Fast.** Mutants run in parallel with `go test -short`, so integration
  tests that honor `testing.Short()` don't eat the budget. When more
  mutants exist than fit the budget, a deterministic sample runs and the
  report says so.
- **Score.** Killed ÷ (killed + lived). Below `go.mutation.min_score`
  (70%), `harden` exits 1. Mutants that don't compile don't count.

## Fuzzing

Every fuzz target that calls a changed function, or that lives in a changed
test file, runs for `go.fuzz.fuzztime` (10s) within a total budget. A crash
fails the run, and Go saves the failing input under `testdata/fuzz/`, where
it becomes a regression test.

## Suggestions

`harden` also lists changed functions that look like they deserve a
benchmark or a fuzz target but have none. These never fail the run.

| Suggests | When |
|---|---|
| A `Benchmark*` | The function is marked `// guardrails:hot`, lives in a package listed in `go.bench.packages`, or its name says it encodes, decodes, parses, hashes, compresses, sorts, scans or tokenizes |
| A `Fuzz*` target | Its name says it parses, decodes, unmarshals, scans or tokenizes, and it takes `[]byte`, an `io.Reader`, or a `string` of content |

Verbs match whole words (`ParseConfig`, `decodeRequest`), not nouns
(`Parser`, `Scanner`). A function opts out with
`// guardrails:nobench <reason>` or `// guardrails:nofuzz <reason>` —
written by you; the [policy guards](guards.md) deny Claude adding them.

The [Go rules](companion-tools.md#rules) teach Claude the same criteria, so
it writes these up front instead of after `harden` asks.

## Options

| Flag | Default | Meaning |
|---|---|---|
| `--base <ref>` | `HEAD` | What "changed" is measured against |
| `--budget <duration>` | `go.mutation.budget_seconds` (120s) | Time for mutation testing |
| `--min-score <pct>` | `go.mutation.min_score` (70) | Minimum mutation score |
| `--fuzztime <duration>` | `go.fuzz.fuzztime` (10s) | Fuzzing time per target |
| `--no-mutation`, `--no-fuzz` | | Skip a part |
| `--json` | | Machine-readable output |

```yaml
go:
  mutation: { min_score: 70, budget_seconds: 120 }
  fuzz: { fuzztime: 10s, budget_seconds: 60 }
  bench:
    packages: ["internal/codec/..."]   # suggest benchmarks for every changed function here
```

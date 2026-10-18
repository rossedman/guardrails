# Test depth

Coverage proves code ran, not that it was tested. A test that calls a
function and asserts nothing reaches 100% coverage. Guardrails closes that
gap with three checks at Stop, all scoped to the **functions changed this
session**:

| Check | Catches | Blocks when |
|---|---|---|
| [Mutation testing](#mutation-testing) | Tests that execute code without checking it | Mutation score on changed code < 70% |
| [Benchmarks](#benchmarks) | Hot paths with no benchmark, and performance regressions | A hot-path function has no benchmark; a benchmark is >10% slower than at the session's start with p < 0.05 |
| [Fuzzing](#fuzzing) | Parsers that crash on unexpected input | A parser has no fuzz target; a fuzz target finds a failing input |

They run only after build, tests, coverage and lint pass — there's no point
mutating code that doesn't compile — and each runs in its own time slice
so the gate stays within budget. Today they apply to Go.

## Mutation testing

Guardrails makes small, deliberate bugs ("mutants") in the lines you
changed and runs the package's tests against each one. A mutant the tests
catch is **killed**; one they miss **lived**, which means no test checks
that behavior.

```text
[mutation] mutation score 55% < 70% (11 of 20 mutants killed)
  internal/sched/queue.go:41  (*Queue).Drain  `>` changed to `>=`, tests still pass
  internal/sched/queue.go:47  (*Queue).Drain  `+` changed to `-`, tests still pass
  fix: add assertions that tell the original behavior apart from each surviving mutant
```

**It's built in.** There's no mutation tool to install. Mutants are written
to temporary files and swapped in with `go test -overlay`, so your source
files are never modified, even if the session is interrupted.

**Operators.** Conditional boundaries (`<` ↔ `<=`, `>` ↔ `>=`), negated
conditions (`==` ↔ `!=`), arithmetic (`+` ↔ `-`, `*` ↔ `/`),
increments (`++` ↔ `--`, `+=` ↔ `-=`), logic (`&&` ↔ `||`), boolean
literals, and removed negation (`-x` → `x`, `!x` → `x`). Only changed lines
are mutated.

**Fast tests only.** Mutants run with `go test -short`, so integration
tests that skip under `testing.Short()` don't eat the budget.

**Budget.** Mutants run in parallel within 45 seconds
(`go.mutation.budget_seconds`). When there are more than fit, a
deterministic sample runs and the gate says so — it never skips silently.
Mutants that don't compile are excluded from the score.

## Benchmarks

### When one is required

A changed function needs a `Benchmark*` that calls it when it's on a hot
path. Signals come from a static pass over the function:

| Signal | Strength |
|---|---|
| A `// guardrails:hot` comment on the function | strong |
| Its package is listed in `go.bench.packages` | strong |
| A verb in its name says it encodes, decodes, parses, hashes, compresses, sorts, scans or tokenizes | borderline |

Verbs are matched as whole words anywhere in the name — `ParseConfig`,
`decodeRequest` and `benchParse` match; the nouns `Parser`, `Scanner` and
`Sorter` don't. Using `encoding/*`, `sort` or `strconv`, or looping over
caller-sized input, is reported as supporting evidence but never flags a
function on its own: on real code that flagged far too much ordinary
orchestration.

**Strong** signals block until a benchmark exists. **Borderline** ones are
*asked*, once per session: Claude adds a benchmark or explains in one line
why one isn't useful, and the gate doesn't raise it again.

### Regressions

When benchmarks cover changed code, Stop runs them with `-benchmem
-count=6` on your working tree **and** on the commit the session started
from (checked out in a temporary git worktree, never your own), then
compares each metric with a Mann-Whitney U test — the same statistics
benchstat uses.

```text
[bench-regression] 1 benchmark regressed vs the session's base commit
  BenchmarkDecode  time/op  +23.4% (p=0.002)  1.20µs → 1.48µs
  BenchmarkDecode  allocs/op  +100.0% (p=0.002)  2 → 4
  fix: make the changed code at least as fast as before, or explain the trade-off to the human
```

A regression is a change of more than `go.bench.regression_pct` (10%) with
p < 0.05, so noise doesn't block.

## Fuzzing

### When a fuzz target is required

A changed function needs a `Fuzz*` target that calls it when its name says
it parses and it takes loosely structured input — `[]byte`, `io.Reader`,
`*bufio.Reader`, `*http.Request`, a file, or a `string`:

| Signal | Strength |
|---|---|
| A parse verb (`Parse`, `Decode`, `Unmarshal`, `Scan`, `Lex`, `Tokenize`…) with such input, and it can return an error | strong |
| The same without an error return | borderline |

`Read`, `Load` and `From` count only with byte or reader input: with a
`string` they almost always take a file path, not content.

### Short fuzz runs

Every fuzz target that covers changed code, or that lives in a test file
touched this session, runs for 10 seconds (`go.fuzz.fuzztime`) within a
30-second total budget. A crash blocks, and Go saves the failing input
under `testdata/fuzz/`, where it becomes a regression test:

```text
[fuzz] 1 fuzz target found a failing input
  FuzzDecode  panic: runtime error: index out of range [3] with length 3  →  internal/codec/testdata/fuzz/FuzzDecode/9f3c… (now a regression test)
  fix: fix the code so the saved input passes; keep the input file, it is now a regression test
```

Longer fuzzing belongs in CI or a nightly job.

## Opting out

Some functions don't need a benchmark or fuzz target. **You** can say so
with a reason, on the function:

```go
// guardrails:nobench called once at startup
func LoadConfig(path string) (*Config, error) {
```

```go
// guardrails:nofuzz input is generated by our own encoder
func decodeInternal(b []byte) (msg, error) {
```

Like `guardrails:allow`, these are yours: Claude adding one is denied by the
[policy guards](guards.md). Mark code as hot with `// guardrails:hot`, which
anyone may add.

## Configuration

```yaml
go:
  mutation:
    tool: builtin        # builtin | off
    min_score: 70
    budget_seconds: 45
  bench:
    require_on_hot_paths: true
    regression_pct: 10
    count: 6             # runs per side for the significance test
    benchtime: 100ms
    packages: ["internal/codec/..."]   # every changed function here is hot
    budget_seconds: 40
  fuzz:
    require_on_parsers: true
    fuzztime: 10s
    budget_seconds: 30

gate:
  timeout_seconds: 170   # room for the test-depth slices
```

Python mutation testing (mutmut) is planned; see the [roadmap](roadmap.md).

---
paths:
  - "**/*.go"
  - "**/go.mod"
---

# Go standards (enforced by Guardrails)

Guardrails formats every Go file you edit (gofumpt, goimports), lints the
package, and blocks the end of your turn until touched packages build, pass
`go test -race`, reach the coverage bar and are golangci-lint clean. Meet the
bar up front instead of on request.

## Code

- Wrap errors with context: `fmt.Errorf("load config: %w", err)`. Never drop
  an error; never `panic` for expected failures.
- `context.Context` is the first parameter of anything that does I/O or may
  block. Don't store contexts in structs.
- No package-level mutable state. Pass dependencies explicitly; accept
  interfaces, return concrete types.
- Keep exported identifiers documented with a sentence starting with the name.
- Prefer the standard library. Add a dependency only when it carries its weight.

## Tests

- Write tests in the same change as the code. Table-driven tests with
  `t.Run(tc.name, ...)` and `t.Parallel()` where safe.
- Assert behavior, not just execution: every test case checks outputs or
  errors. Coverage that only runs code will be caught by mutation testing.
- Use `t.TempDir()`, `t.Setenv()` and `t.Context()`; no sleeps for
  synchronization.
- Cover error paths: the coverage gate lists uncovered functions by
  file:line, and lines you changed this session must be covered too.

## Benchmarks, fuzzing and mutation testing

The Stop gate mutates the lines you changed (`>` to `>=`, `+` to `-`, ...)
and runs the tests: a mutant that survives means a behavior nobody checks.

- Add a `Benchmark*` for hot paths: parsing, encoding, hashing, sorting, loops
  over caller-sized input, or anything you were asked to optimize.
- Add a `Fuzz*` target for functions that parse untrusted or loosely
  structured input (bytes, strings, files, wire formats), especially where a
  round-trip property holds. Give it a seed corpus with `f.Add`.
- Mark code you know is hot with `// guardrails:hot`. Only the human may
  add `// guardrails:nobench` or `// guardrails:nofuzz`; if you think one
  is warranted, say so in one line instead.

## Never

- `//nolint`, `t.Skip` to get green, editing `go.sum` by hand, editing
  generated files, or touching `.guardrails.yaml` / `.golangci.yml`. These are
  denied; propose changes to the human instead.

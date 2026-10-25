# guardrails harden

Run mutation tests and fuzzing on the Go code you changed (on demand)

### Synopsis

Check how well the tests of the Go functions changed against --base
actually test them, on demand rather than in the Stop gate:

  mutation testing  flips operators and constants in each changed function
                    and checks a test fails; survivors are reported with
                    their line, against go.mutation.min_score
  fuzzing           runs the fuzz targets in touched packages for
                    go.fuzz.fuzztime each
  suggestions       benchmarks and fuzz targets worth adding (not failures)

It exits 1 when the mutation score is low or a fuzz target fails. The
/guardrails:harden skill runs it and has Claude fix what it finds.

```
guardrails harden [flags]
```

### Examples

```
  guardrails harden --base main
  guardrails harden --no-fuzz --min-score 80
  guardrails harden --budget 5m --json
```

### Options

```
      --base string         git ref to compare against, e.g. main to review a whole branch (default "HEAD")
      --budget duration     time budget for mutation testing (default go.mutation.budget_seconds)
      --fuzztime duration   fuzzing time per target (default go.fuzz.fuzztime)
  -h, --help                help for harden
      --json                print results as JSON
      --min-score float     minimum mutation score (default go.mutation.min_score) (default -1)
      --no-fuzz             skip fuzzing
      --no-mutation         skip mutation testing
```

### Options inherited from parent commands

```
      --config string     config file for all your repos (default ~/.config/guardrails/config.yaml)
      --data-dir string   where guardrails keeps state (default $CLAUDE_PLUGIN_DATA, else ~/.local/share/guardrails)
```

### SEE ALSO

* [guardrails](guardrails.md)	 - Quality gates for Claude Code


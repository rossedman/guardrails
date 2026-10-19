# Presets

A preset is a starting bar for a repository. Pick one, then override any key
you want. `/guardrails:setup` measures the repository and recommends one.

```yaml
# .guardrails.yaml
version: 1
preset: adopt
```

| | **strict** (default) | **standard** | **adopt** |
|---|---|---|---|
| For | New or already-clean repos | Repos close to the bar | Existing repos well below the bar |
| Total coverage of touched packages | 90% | 80% | no floor |
| Go per-package floor | 80% | 70% | no floor |
| Coverage of lines changed this session | 90% | 80% | 80% |
| Ratchet (never below where the session started) | on | on | on |

Everything else — linters, type checkers, scanners, the
[policy guards](guards.md) and the [dependency check](dependencies.md) —
is the same in every preset.

## strict

The full bar on every turn. Right for new code and for repositories that
already meet it; the default when there is no config.

## standard

The same checks with a lower coverage bar. Right for repositories within
about 10 points of strict, or teams that find 90% too costly for the code
they write.

## adopt

**Don't get worse, and test what you touch.** For an existing repository
well below the bar, strict would block on every turn over debt Claude
didn't create. adopt drops the absolute coverage floors and keeps the two
rules that stop things getting worse:

- **The ratchet.** No touched package may end below the coverage it had
  when the session started. Each passing gate raises the baseline, so
  coverage only goes up.
- **Patch coverage.** 80% of the lines Claude changes in the session must
  be run by a test, so new code arrives tested.

Pre-existing lint issues and coverage gaps in code Claude didn't touch never
block, in any preset.

## Overriding keys

A preset only sets defaults. Any key in `.guardrails.yaml` or the user-wide
`~/.config/guardrails/config.yaml` overrides it:

```yaml
version: 1
preset: standard
go:
  patch_coverage: 90   # standard's bar, but hold new code to 90%
```

The layers, each overriding the one before, key by key:

1. Built-in defaults.
2. The preset.
3. `~/.config/guardrails/config.yaml`.
4. The repository's `.guardrails.yaml`.

`guardrails config` prints the result. See [Configuration](configuration.md)
for every key.

## Moving up

Presets are meant to be outgrown. Once total coverage is within about 10
points of the next bar — `/guardrails:status` shows it — change `preset:
adopt` to `standard`, then `standard` to `strict`. `.guardrails.yaml` is
yours to edit; Claude can propose the change but can't make it.

# Shell module

The shell module owns `*.sh`, `*.bash` and `*.ksh` files, plus
extensionless scripts whose shebang names `sh`, `bash`, `dash` or `ksh`
(`#!/bin/sh`, `#!/usr/bin/env bash`, `#!/bin/bash -eu`). zsh and fish
scripts are not matched, because shellcheck doesn't support them.

| Stage | What runs |
|---|---|
| **On edit: auto-fix** | `shfmt -w` on the edited script. shfmt honors `.editorconfig` (indent style, `binary_next_line`, `switch_case_indent`, …). |
| **On edit: check** | `shellcheck -f json1 -S <severity>` on the edited script. |
| **At Stop: gate** | shellcheck over every touched script, then `shfmt -l` to confirm each one is formatted. |

Issues are reported with shellcheck's rule ids, so Claude can look them up:

```text
[shellcheck] 2 issues
  scripts/deploy.sh:14:6  SC2086  Double quote to prevent globbing and word splitting.
  scripts/deploy.sh:21:8  SC2050  This expression is constant. Did you forget the $ on a variable?
  fix: fix each issue; `# shellcheck disable` comments are not allowed

[format] 1 script not shfmt-formatted
  scripts/release
  fix: run `shfmt -w` on the files above
```

New `# shellcheck disable=...` comments are denied by the
[suppression guard](guards.md#anti-gaming), like every other suppression.

## Configuration

```yaml
shell:
  enabled: true      # default: on when shell scripts exist
  severity: style    # style | info | warning | error — the minimum shellcheck severity reported
```

shellcheck also reads `.shellcheckrc`, so repo-wide settings such as
`external-sources=true` or a default `shell=bash` apply as usual.

## Toolchain

| Tool | Required | Missing means |
|---|---|---|
| `shellcheck` | yes | Edits aren't blocked; the gate fails with a `tools` check |
| `shfmt` | yes | Scripts aren't formatted on edit; the gate fails with a `tools` check |

Both are **provisioned automatically**: when they aren't on `PATH`,
Guardrails installs the pinned, checksum-verified versions into its data
directory, in the background at SessionStart. Set `tools.install: never`
to use only what you've installed, or `tools.prefer: managed` to always use
the pinned versions. `guardrails tools list` shows where each tool
resolves from.

# CLI

The same binary that runs the hooks is a CLI, so you can run the checks
outside Claude and get identical results. Every command has `--help` with
examples, and `guardrails help` lists them in groups:

| Group | Commands |
|---|---|
| Claude Code | `hook`, `statusline` |
| Gates | `gate`, `harden`, `explain`, `insights`, `lessons` |
| Setup | `init`, `doctor`, `tools`, `config`, `schema` |
| More | `version`, `completion`, `help` |

Exit codes are the same everywhere: 0 on success, 1 when a command fails
(a gate that doesn't pass, a missing tool), and 2 for a usage mistake such as
an unknown command or flag.

## Global flags

| Flag | Environment | Meaning |
|---|---|---|
| `--data-dir` | `GUARDRAILS_DATA_DIR` | Where state, reports and managed tools live |
| `--config` | `GUARDRAILS_CONFIG` | The user-wide config file |

## Flags from the environment

Every command flag can also be set as `GUARDRAILS_<COMMAND>_<FLAG>`, with
dashes as underscores. A flag on the command line wins.

```bash
GUARDRAILS_GATE_BASE=origin/main guardrails gate
GUARDRAILS_HARDEN_NO_FUZZ=true guardrails harden
GUARDRAILS_TOOLS_INSTALL_QUIET=1 guardrails tools install
```

## `guardrails gate`

Run the full quality gate.

```bash
guardrails gate                 # files changed against HEAD, plus untracked files
guardrails gate --base main     # changes against another ref
guardrails gate --all           # every package
guardrails gate --explain       # full, untruncated report
guardrails gate --json          # machine-readable
guardrails gate --staged        # only files staged for commit (pre-commit)
```

Exits 0 when the gate passes and 1 when it fails. Patch coverage is
measured against `--base`. The result updates the status line cache.

## `guardrails harden`

Mutation-test the Go functions changed against a ref and briefly fuzz their
fuzz targets; list functions that could use a benchmark or fuzz target.
Exits 1 on surviving mutants below the minimum score or a fuzz crash. See
[Hardening tests](harden.md).

```bash
guardrails harden [--base main] [--budget 3m] [--min-score 80] [--fuzztime 30s] [--no-fuzz] [--json]
```

## `guardrails insights`

Summarize the gate history: first-pass rate, blocks per turn, gate time and
the rules that trip most, with rule candidates worth teaching Claude. See
[Insights](insights.md).

```bash
guardrails insights [--days 30] [--all-repos] [--min-sessions 5] [--json]
```

## `guardrails lessons`

List the corrections you've given Claude in this repository, recorded as
candidate rules. `/guardrails:learn` drafts them into `.claude/rules/`. See
[Lessons](lessons.md).

```bash
guardrails lessons [--all-repos] [--json]
guardrails lessons --clear [--id ID ...]   # remove handled lessons
```

## `guardrails explain`

Print the full report from the most recent gate in this repository, whether
it ran in a hook or from the CLI.

## `guardrails doctor`

Check the config and every module's tools, with versions. Exits 1 if the
config is invalid or a required tool is missing.

## `guardrails init`

Write `.guardrails.yaml` and `.claude/rules/*.md` into the repository, and
print a `permissions.deny` block for `.claude/settings.json`.

| Flag | Default | Meaning |
|---|---|---|
| `--force` | `false` | Overwrite existing files |
| `--rules` | `true` | Install path-scoped rules into `.claude/rules/` |

## `guardrails tools`

Manage the external tools Guardrails runs. See [Tool provisioning](tools.md).

```bash
guardrails tools list              # pinned version and where each tool resolves
guardrails tools install [names]   # install pinned tools (all when none named)
guardrails tools path <name>       # the command Guardrails runs for a tool
```

## `guardrails config`

Print the effective configuration after merging defaults, the global file
and `.guardrails.yaml`.

## `guardrails schema`

Print the JSON Schema for the config file.

## `guardrails statusline`

Print one line for Claude Code's `statusLine`, read from the cache the hooks
write. Never fails.

```text
⛨ gate ✓ · cov 92.6%/90% · lint 0
```

## `guardrails hook <event>`

Handle a Claude Code hook event: reads the event JSON on stdin and writes
the decision JSON to stdout. Event names are case- and dash-insensitive
(`Stop`, `stop`, `post-tool-use`). On internal errors it exits 2 for events
that can block, so they fail closed, and 1 otherwise.

## `guardrails completion <shell>`

Print a completion script for bash, zsh, fish or PowerShell. Hook events and
tool names complete too.

```bash
guardrails completion zsh > "${fpath[1]}/_guardrails"
```

## Environment

| Variable | Purpose |
|---|---|
| `GUARDRAILS_DATA_DIR` | State directory; overrides everything below |
| `CLAUDE_PLUGIN_DATA` | State directory (set by Claude Code); falls back to `$XDG_DATA_HOME/guardrails` or `~/.local/share/guardrails` |
| `GUARDRAILS_CONFIG` | The global config file |
| `XDG_CONFIG_HOME` | Location of the global config |
| `NO_COLOR` | Plain help output, even in a terminal |
| `GUARDRAILS_BIN` | The plugin launcher runs this binary instead of downloading one |

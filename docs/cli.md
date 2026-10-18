# CLI

The same binary that runs the hooks is a CLI, so you can run the checks
outside Claude and get identical results.

## `guardrails gate`

Run the full quality gate.

```bash
guardrails gate                 # files changed against HEAD, plus untracked files
guardrails gate --base main     # changes against another ref
guardrails gate --all           # every package
guardrails gate --explain       # full, untruncated report
guardrails gate --json          # machine-readable
```

Exits 0 when the gate passes and 1 when it fails. Patch coverage is
measured against `--base`. The result updates the status line cache.

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

## Environment

| Variable | Purpose |
|---|---|
| `CLAUDE_PLUGIN_DATA` | State directory (set by Claude Code); falls back to `$XDG_DATA_HOME/guardrails` or `~/.local/share/guardrails` |
| `XDG_CONFIG_HOME` | Location of the global config |
| `GUARDRAILS_BIN` | The plugin launcher runs this binary instead of downloading one |

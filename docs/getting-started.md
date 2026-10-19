# Getting started

Three steps: install the plugin, let it tell you it's on, and pick how strict
it should be for this repository.

## 1. Install the plugin

In Claude Code:

```text
/plugin marketplace add rossedman/guardrails
/plugin install guardrails@guardrails
```

That's all you install. On first use the plugin downloads its own binary
for your platform, verifies it against the release checksums, and caches
it. The linters and scanners it runs — golangci-lint, ruff, mypy,
shellcheck, tflint, trivy and the rest — are provisioned the same way:
pinned, checksum-verified, downloaded on first use. Tools you already have
on `PATH` are used first. You only need the runtime for the language you
write, such as Go or a Python project environment. See
[Tool provisioning](tools.md).

!!! note "Platforms"
    The plugin supports macOS and Linux (amd64 and arm64). On Windows, use
    WSL.

## 2. It tells you it's on

Start a session in a repository. The first time, Guardrails tells you what
it found and what it will hold Claude to:

```text
guardrails: on in this repo for Go, Shell · strict (90% coverage, 90% of
changed lines) · Claude's work is checked when it stops · /guardrails:status
for details, /guardrails:setup to tune
```

With no config, the **strict** preset applies to every language the repo
uses. Ask for `/guardrails:status` at any time to see what it's doing and
what to do next.

## 3. Set it up for this repository

In Claude Code, run:

```text
/guardrails:setup
```

Claude measures where the code is today (coverage, lint issues), recommends
a [preset](presets.md), and, once you agree, writes a short
`.guardrails.yaml` and the `.claude/rules/` files that teach Claude your
standards before it writes code:

| Preset | For | The bar |
|---|---|---|
| **strict** | New or already-clean repos | 90% coverage, 90% of changed lines tested |
| **standard** | Repos close to the bar | 80% coverage, 80% of changed lines tested |
| **adopt** | Existing repos well below the bar | Coverage may never drop; 80% of changed lines tested |

Setup also offers the status line and a `permissions.deny` block for
`.claude/settings.json`, which keeps the protections in place even if a hook
fails. Plugins cannot ship permission rules, so you add it yourself.

That's it. See [Day to day](daily.md) for what happens next.

## From a terminal (optional)

Everything also works as a `guardrails` command, so you can run the same
gate in your shell, CI or a pre-commit hook. Inside Claude Code it's already
on `PATH`. For your own terminal, either link the plugin's binary —
`guardrails status` (or `/guardrails:status`) prints the exact command:

```bash
ln -s <path printed by status> ~/.local/bin/guardrails
```

or install it with Go:

```bash
go install github.com/rossedman/guardrails/cmd/guardrails@latest
```

Then:

```bash
guardrails status          # what's on, the last gate, what to do next
guardrails init            # write .guardrails.yaml and .claude/rules (what setup runs)
guardrails gate            # the same gate Claude faces, on your changes
guardrails doctor          # config, runtimes and every tool
guardrails tools install   # download every tool up front, e.g. before going offline
```

`GUARDRAILS_BIN=/path/to/guardrails` makes the plugin use a binary you
built yourself (useful when developing Guardrails). See the [CLI](cli.md).

## Status line (optional)

Show the last gate result in Claude Code's status line:

```text
⛨ gate ✓ · cov 92.6%/90% · lint 0
```

`guardrails init --statusline` prints the snippet with the full path to
the binary; add it to `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/path/printed/by/init/guardrails statusline",
    "refreshInterval": 10
  }
}
```

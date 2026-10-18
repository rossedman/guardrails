# Getting started

## 1. Tools: nothing to install

Guardrails runs a lot of tools — golangci-lint, ruff, mypy, shellcheck,
yamllint, tflint, trivy and more — but it provisions them itself: pinned
versions, checksum-verified, downloaded on first use into the plugin's data
directory. Tools you already have on `PATH` are used first.

You only need the runtime for the language you write, such as Go or a
Python project environment. See [Tool provisioning](tools.md) for details.

## 2. Install the plugin

In Claude Code:

```text
/plugin marketplace add rossedman/guardrails
/plugin install guardrails@guardrails
```

On first use the plugin's launcher downloads the release binary for your
platform from GitHub, verifies it against `checksums.txt`, and caches it in
the plugin data directory. If you prefer to build it yourself:

```bash
go install github.com/rossedman/guardrails/cmd/guardrails@latest
```

The launcher falls back to a `guardrails` binary on your `PATH`, and
`GUARDRAILS_BIN=/path/to/guardrails` overrides everything (useful when
developing Guardrails itself).

!!! note "Platforms"
    The plugin launcher supports macOS and Linux (amd64 and arm64). On
    Windows, use WSL.

## 3. Check your setup

```bash
guardrails doctor        # config and runtimes
guardrails tools list    # every tool and where it resolves from
```

To download everything up front, for example before going offline:

```bash
guardrails tools install
```

## 4. Configure the repository (optional)

With no config, the defaults apply to every language the repo uses: 90%
coverage with a ratchet and 90% patch coverage, clean linters and type
checkers, and the policy guards. To tune them:

```bash
guardrails init
```

This writes:

- `.guardrails.yaml` with every default spelled out (see
  [Configuration](configuration.md)).
- `.claude/rules/*.md`, path-scoped rules for each language that teach
  Claude the standards up front, so it hits the gate less often.

It also prints a `permissions.deny` block for `.claude/settings.json`.
Plugins cannot ship permission rules, and those denies hold even if a hook
fails, so adding them is recommended.

## 5. Optional: status line

Show the last gate result in Claude Code's status line by adding this to
`~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "guardrails statusline",
    "refreshInterval": 10
  }
}
```

```text
⛨ gate ✓ · cov 92.6%/90% · lint 0
```

## 6. Work as usual

Ask Claude for a change. You'll see edits formatted as they land, lint
feedback within seconds, and, when Claude tries to finish, the gate. Run the
same gate yourself any time:

```bash
guardrails gate            # files changed against HEAD
guardrails gate --all      # every package
guardrails explain         # the full report from the last gate
```

Before committing, check that the tests behind your changes really assert
something — ask Claude to run `/guardrails:harden`, or run it yourself:

```bash
guardrails harden              # mutation-test and fuzz what changed since HEAD
guardrails harden --base main  # the whole branch
```

See [Hardening tests](harden.md).

# Getting started

## 1. Install the tools

Guardrails orchestrates the tools you already trust. For Go:

| Tool | Required | Install |
|---|---|---|
| Go | yes | <https://go.dev/doc/install> |
| golangci-lint v2 | yes | <https://golangci-lint.run/welcome/install/> |
| gofumpt | recommended | `go install mvdan.cc/gofumpt@latest` |
| goimports | recommended | `go install golang.org/x/tools/cmd/goimports@latest` |
| govulncheck | recommended | `go install golang.org/x/vuln/cmd/govulncheck@latest` |

Missing recommended tools degrade gracefully (gofmt replaces gofumpt; the
vulnerability check is skipped) and are reported to you, never to Claude.

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
guardrails doctor
```

```text
guardrails v0.1.0
data dir: /home/you/.local/share/guardrails
config:   ✓ /home/you/src/myrepo

go module
  ✓ go             /usr/local/go/bin/go go version go1.26.9 linux/amd64
  ✓ golangci-lint  /home/you/go/bin/golangci-lint golangci-lint has version 2.15.0
  ✓ gofumpt        /home/you/go/bin/gofumpt v0.9.1
  ✓ goimports      /home/you/go/bin/goimports
  ✓ govulncheck    /home/you/go/bin/govulncheck Go: go1.26.9
```

## 4. Configure the repository (optional)

With no config, the defaults apply: 90% coverage on touched packages, an 80%
per-package floor, a coverage ratchet, 90% patch coverage, race tests,
golangci-lint and govulncheck. To tune them:

```bash
guardrails init
```

This writes:

- `.guardrails.yaml` with every default spelled out (see
  [Configuration](configuration.md)).
- `.claude/rules/go.md`, a path-scoped rule that teaches Claude the
  standards up front, so it hits the gate less often.

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

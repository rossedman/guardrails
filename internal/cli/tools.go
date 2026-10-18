package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/tools"
)

const toolsUsage = `usage: guardrails tools <command>

  list                 Show every tool, its pinned version and where it resolves from
  install [names...]   Install pinned tools (all supported tools when none are named)
  path <name>          Print the command used to run a tool, installing it if needed
`

// tools handles `guardrails tools`.
func (a *App) tools(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, toolsUsage)
		return 2
	}
	m := a.toolManager()
	if m == nil {
		fmt.Fprintln(a.Stderr, "guardrails: tool provisioning is unavailable")
		return 1
	}
	if repo, err := a.repo(ctx); err == nil {
		if cfg, err := config.Load(a.globalConfig(), repo); err == nil {
			m.Prefer = cfg.Tools.Prefer
		}
	}
	switch args[0] {
	case "list":
		a.toolsList(m)
		return 0
	case "install":
		return a.toolsInstall(ctx, m, args[1:])
	case "path":
		if len(args) != 2 {
			fmt.Fprint(a.Stderr, toolsUsage)
			return 2
		}
		cmd, err := m.WithBlocking(true).Resolve(ctx, args[1])
		if err != nil {
			fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Stdout, strings.Join(append([]string{cmd.Path}, cmd.Args...), " "))
		return 0
	default:
		fmt.Fprintf(a.Stderr, "guardrails: unknown tools command %q\n\n%s", args[0], toolsUsage)
		return 2
	}
}

func (a *App) toolsList(m *tools.Manager) {
	fmt.Fprintf(a.Stdout, "%-14s %-10s %-8s %s\n", "TOOL", "PINNED", "SOURCE", "RESOLVES TO")
	for _, name := range m.Lock.Names() {
		t := m.Lock.Tools[name]
		where := "not installed"
		switch {
		case m.Prefer != tools.PreferManaged && onPath(m, name) != "":
			where = onPath(m, name) + " (PATH)"
		case m.Installed(name):
			where = "managed"
		case !m.Supported(name):
			where = "unsupported on " + tools.Platform()
		case onPath(m, name) != "":
			where = onPath(m, name) + " (PATH)"
		}
		fmt.Fprintf(a.Stdout, "%-14s %-10s %-8s %s\n", name, t.Version, t.Source, where)
	}
}

func onPath(m *tools.Manager, name string) string {
	p, err := m.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func (a *App) toolsInstall(ctx context.Context, m *tools.Manager, args []string) int {
	quiet := false
	var names []string
	for _, arg := range args {
		if arg == "--quiet" || arg == "-q" {
			quiet = true
			continue
		}
		names = append(names, arg)
	}
	if len(names) == 0 {
		for _, n := range m.Lock.Names() {
			if m.Supported(n) {
				names = append(names, n)
			}
		}
	}
	failed := false
	for _, name := range names {
		if _, ok := m.Lock.Tools[name]; !ok {
			fmt.Fprintf(a.Stderr, "guardrails: unknown tool %q\n", name)
			failed = true
			continue
		}
		if m.Installed(name) {
			if !quiet {
				fmt.Fprintf(a.Stdout, "  ✓ %-14s %s (already installed)\n", name, m.Lock.Tools[name].Version)
			}
			continue
		}
		if err := m.Installer.Install(ctx, name); err != nil {
			fmt.Fprintf(a.Stderr, "  ✗ %-14s %v\n", name, err)
			failed = true
			continue
		}
		if !quiet {
			fmt.Fprintf(a.Stdout, "  ✓ %-14s %s\n", name, m.Lock.Tools[name].Version)
		}
	}
	if failed {
		return 1
	}
	return 0
}

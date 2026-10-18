// Package cli implements the guardrails command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/engine"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/state"
	"github.com/rossedman/guardrails/internal/tools"
)

// Build metadata, set by goreleaser via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// App is the CLI with its dependencies injectable for tests.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Runner runner.Runner
	// Getenv reads environment variables.
	Getenv func(string) string
	// Getwd returns the working directory.
	Getwd func() (string, error)
	// Now is the clock.
	Now func() time.Time
	// NewTools builds the tool manager rooted at a directory. Nil disables
	// tool provisioning, so tools resolve from PATH only.
	NewTools func(root string) *tools.Manager
	// Modules overrides the engine's language modules when non-nil.
	Modules []module.Module
}

// New returns an App wired to the real process.
func New() *App {
	return &App{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Runner: runner.Exec{},
		Getenv: os.Getenv,
		Getwd:  os.Getwd,
		Now:    time.Now,
		NewTools: func(root string) *tools.Manager {
			lock, err := tools.DefaultLock()
			if err != nil {
				return nil
			}
			return tools.NewManager(lock, root)
		},
	}
}

const usage = `guardrails — quality gates for Claude Code

Usage:
  guardrails <command> [flags]

Commands:
  hook <event>   Handle a Claude Code hook event (reads JSON on stdin)
  gate           Run the full quality gate on changed files (or --all)
  harden         Mutation-test and fuzz the Go code you changed (on demand)
  explain        Print the full report from the last gate in this repo
  doctor         Check that required tools are installed
  init           Write .guardrails.yaml and Claude rules into this repo
  config         Print the effective configuration
  schema         Print the config JSON Schema
  tools          List, install or locate the external tools Guardrails runs
  statusline     Print a one-line status for Claude Code's status line
  version        Print version information

Run 'guardrails <command> -h' for command flags.
Docs: https://rossedman.github.io/guardrails/
`

// Run executes args (without the program name) and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "hook":
		return a.hook(ctx, rest)
	case "gate":
		return a.gate(ctx, rest)
	case "explain":
		err = a.explain(ctx)
	case "doctor":
		return a.doctor(ctx)
	case "init":
		err = a.init(rest)
	case "config":
		err = a.config(ctx)
	case "schema":
		_, err = a.Stdout.Write(config.Schema)
	case "tools":
		return a.tools(ctx, rest)
	case "harden":
		return a.harden(ctx, rest)
	case "statusline":
		a.statusline(ctx)
	case "version", "--version", "-v":
		fmt.Fprintf(a.Stdout, "guardrails %s (commit %s, built %s)\n", Version, Commit, Date)
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, usage)
	default:
		fmt.Fprintf(a.Stderr, "guardrails: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "guardrails: %v\n", err)
		return 1
	}
	return 0
}

// DataDir returns the plugin data dir, falling back to the XDG data dir so
// the CLI works outside Claude Code.
func (a *App) DataDir() string {
	if d := a.Getenv("CLAUDE_PLUGIN_DATA"); d != "" {
		return d
	}
	if d := a.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "guardrails")
	}
	if home := a.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "share", "guardrails")
	}
	return filepath.Join(os.TempDir(), "guardrails")
}

func (a *App) globalConfig() string {
	if d := a.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "guardrails", "config.yaml")
	}
	if home := a.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "guardrails", "config.yaml")
	}
	return ""
}

func (a *App) engine() *engine.Engine {
	e := engine.New(state.New(a.DataDir()), a.Runner, a.globalConfig())
	e.Now = a.Now
	if a.Modules != nil {
		e.Modules = a.Modules
	}
	e.Tools = a.toolManager()
	return e
}

// toolManager returns the tool manager rooted in the data dir, or nil when
// tool provisioning is disabled (tests construct App without NewTools).
func (a *App) toolManager() *tools.Manager {
	if a.NewTools == nil {
		return nil
	}
	return a.NewTools(filepath.Join(a.DataDir(), "tools"))
}

func (a *App) repo(ctx context.Context) (string, error) {
	wd, err := a.Getwd()
	if err != nil {
		return "", err
	}
	return a.engine().RepoRoot(ctx, wd), nil
}

func (a *App) config(ctx context.Context) error {
	repo, err := a.repo(ctx)
	if err != nil {
		return err
	}
	cfg, err := config.Load(a.globalConfig(), repo)
	if err != nil {
		return err
	}
	return printYAML(a.Stdout, cfg)
}

func (a *App) explain(ctx context.Context) error {
	repo, err := a.repo(ctx)
	if err != nil {
		return err
	}
	rc, err := state.New(a.DataDir()).LoadRepo(repo)
	if err != nil {
		return err
	}
	if rc.LastReport == "" {
		return errors.New("no gate has run in this repo yet")
	}
	data, err := os.ReadFile(rc.LastReport)
	if err != nil {
		return fmt.Errorf("read last report: %w", err)
	}
	_, err = a.Stdout.Write(data)
	return err
}

func newFlags(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("guardrails "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}

func trimLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

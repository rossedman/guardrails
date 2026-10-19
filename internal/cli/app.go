// Package cli is the guardrails command line, built with Cobra and Viper.
//
// Each command lives in its own file, in a constructor that takes the shared
// *App: root.go wires them together, and the tools group has a file for the
// parent command and one per subcommand. Every flag is bound to Viper, so it
// can also be set as GUARDRAILS_<COMMAND>_<FLAG> in the environment.
package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

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

// Viper keys for the global flags.
const (
	keyDataDir = "data_dir"
	keyConfig  = "config"
)

// App is what every command shares, with its dependencies injectable for
// tests.
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
	// Executable returns this binary's path; nil uses os.Executable.
	Executable func() (string, error)

	// V holds flag values and GUARDRAILS_* environment overrides. Run
	// replaces it on every invocation.
	V *viper.Viper
}

// New returns an App wired to the real process.
func New() *App {
	return &App{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Runner:     runner.Exec{},
		Getenv:     os.Getenv,
		Getwd:      os.Getwd,
		Now:        time.Now,
		Executable: os.Executable,
		NewTools: func(root string) *tools.Manager {
			lock, err := tools.DefaultLock()
			if err != nil {
				return nil
			}
			return tools.NewManager(lock, root)
		},
	}
}

// newViper returns a Viper reading GUARDRAILS_* variables: the key
// gate.base is GUARDRAILS_GATE_BASE, harden.no-fuzz GUARDRAILS_HARDEN_NO_FUZZ.
func newViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("GUARDRAILS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	return v
}

// setting reads a Viper key, or "" before Run has set Viper up.
func (a *App) setting(key string) string {
	if a.V == nil {
		return ""
	}
	return a.V.GetString(key)
}

// DataDir returns where guardrails keeps state: --data-dir or
// GUARDRAILS_DATA_DIR, then the plugin data dir, then the XDG data dir so
// the CLI works outside Claude Code.
func (a *App) DataDir() string {
	if d := a.setting(keyDataDir); d != "" {
		return d
	}
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

// globalConfig is the user-wide config file: --config or GUARDRAILS_CONFIG,
// else ~/.config/guardrails/config.yaml.
func (a *App) globalConfig() string {
	if p := a.setting(keyConfig); p != "" {
		return p
	}
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

// launcher is the command that runs guardrails: the plugin's launcher
// script when it started us (it sets GUARDRAILS_LAUNCHER), else this binary.
func (a *App) launcher() string {
	if p := a.Getenv("GUARDRAILS_LAUNCHER"); p != "" {
		return p
	}
	if a.Executable != nil {
		if p, err := a.Executable(); err == nil {
			return p
		}
	}
	return "guardrails"
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

func printYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}

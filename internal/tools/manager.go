package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Preferences for where a tool comes from.
const (
	// PreferPath uses a tool already on PATH, falling back to the managed
	// copy. This is the default: it respects what the human installed.
	PreferPath = "path"
	// PreferManaged always uses the pinned version, for results that match
	// CI and teammates exactly.
	PreferManaged = "managed"
)

// Errors returned by Resolve.
var (
	// ErrMissing means the tool is not installed and will not be.
	ErrMissing = errors.New("not installed")
	// ErrInstalling means a background install has started; the caller
	// should skip the check now rather than wait.
	ErrInstalling = errors.New("installing in the background")
)

// Command is how to invoke a resolved tool: Path followed by Args, then the
// caller's own arguments.
type Command struct {
	Path string
	Args []string
	// Managed reports whether the command is a Guardrails-managed copy.
	Managed bool
}

// Resolver finds tools. Modules depend on this interface so tests can
// supply fixed answers.
type Resolver interface {
	Resolve(ctx context.Context, name string) (Command, error)
}

// Manager resolves and installs tools.
type Manager struct {
	Lock *Lock
	// Root is where managed tools live, e.g. ${CLAUDE_PLUGIN_DATA}/tools.
	Root string
	// Prefer is PreferPath or PreferManaged.
	Prefer string
	// AutoInstall allows installing missing tools.
	AutoInstall bool
	// Blocking installs synchronously; otherwise a missing tool starts a
	// background install and Resolve returns ErrInstalling.
	Blocking bool
	// Platform is the "os/arch" key; empty means the current platform.
	Platform string

	// LookPath finds executables on PATH.
	LookPath func(string) (string, error)
	// Installer performs installs.
	Installer *Installer
	// Spawn starts a detached `guardrails tools install <names>` process.
	Spawn func(names ...string) error
}

// NewManager returns a Manager with real dependencies.
func NewManager(lock *Lock, root string) *Manager {
	m := &Manager{
		Lock:        lock,
		Root:        root,
		Prefer:      PreferPath,
		AutoInstall: true,
		LookPath:    exec.LookPath,
	}
	m.Installer = NewInstaller(m)
	m.Spawn = spawnDetached
	return m
}

// WithBlocking returns a copy of m with Blocking set.
func (m *Manager) WithBlocking(b bool) *Manager {
	c := *m
	c.Blocking = b
	return &c
}

func (m *Manager) platform() string {
	if m.Platform != "" {
		return m.Platform
	}
	return Platform()
}

// Dir is the install directory of a locked tool.
func (m *Manager) Dir(name string) string {
	t := m.Lock.Tools[name]
	return filepath.Join(m.Root, name, t.Version)
}

// managedPath returns where the executable of a managed tool lives.
func (m *Manager) managedPath(name string) string {
	t := m.Lock.Tools[name]
	dir := m.Dir(name)
	switch t.Source {
	case SourceUV:
		return filepath.Join(dir, "venv", "bin", t.Entrypoint)
	case SourceGo:
		return filepath.Join(dir, "bin", filepath.Base(t.Module))
	default:
		return filepath.Join(dir, name)
	}
}

// Installed reports whether the managed copy of name is complete.
func (m *Manager) Installed(name string) bool {
	if _, ok := m.Lock.Tools[name]; !ok {
		return false
	}
	_, err := os.Stat(filepath.Join(m.Dir(name), ".complete"))
	return err == nil
}

// installing reports whether another process holds the install lock.
func (m *Manager) installing(name string) bool {
	_, err := os.Stat(m.Dir(name) + ".lock")
	return err == nil
}

// Supported reports whether name can be installed on this platform.
func (m *Manager) Supported(name string) bool {
	t, ok := m.Lock.Tools[name]
	if !ok {
		return false
	}
	switch t.Source {
	case SourceArchive, SourcePyPI:
		_, ok := t.Platforms[m.platform()]
		return ok
	case SourceUV:
		return m.Supported("uv")
	case SourceGo:
		_, err := m.LookPath("go")
		return err == nil
	}
	return false
}

// Resolve implements Resolver.
func (m *Manager) Resolve(ctx context.Context, name string) (Command, error) {
	onPath := func() (Command, bool) {
		if p, err := m.LookPath(name); err == nil {
			return Command{Path: p}, true
		}
		return Command{}, false
	}
	if m.Prefer != PreferManaged {
		if c, ok := onPath(); ok {
			return c, nil
		}
	}
	if m.Installed(name) {
		return Command{Path: m.managedPath(name), Managed: true}, nil
	}
	if !m.AutoInstall || !m.Supported(name) {
		// A managed preference still falls back to PATH rather than fail.
		if c, ok := onPath(); ok {
			return c, nil
		}
		return Command{}, fmt.Errorf("%s: %w", name, ErrMissing)
	}
	if !m.Blocking {
		if m.installing(name) {
			return Command{}, fmt.Errorf("%s: %w", name, ErrInstalling)
		}
		if err := m.Spawn(name); err != nil {
			return Command{}, fmt.Errorf("%s: start install: %w", name, err)
		}
		return Command{}, fmt.Errorf("%s: %w", name, ErrInstalling)
	}
	if err := m.Installer.Install(ctx, name); err != nil {
		return Command{}, fmt.Errorf("install %s: %w", name, err)
	}
	return Command{Path: m.managedPath(name), Managed: true}, nil
}

// Prefetch starts one background install for every tool in names that is
// missing and installable. It never blocks.
func (m *Manager) Prefetch(names ...string) error {
	var want []string
	for _, n := range names {
		if m.Prefer != PreferManaged {
			if _, err := m.LookPath(n); err == nil {
				continue
			}
		}
		if !m.Installed(n) && !m.installing(n) && m.AutoInstall && m.Supported(n) {
			want = append(want, n)
		}
	}
	if len(want) == 0 {
		return nil
	}
	return m.Spawn(want...)
}

// PathResolver resolves tools from PATH only. It is the fallback when no
// Manager is configured.
type PathResolver struct {
	LookPath func(string) (string, error)
}

// Resolve implements Resolver.
func (p PathResolver) Resolve(_ context.Context, name string) (Command, error) {
	path, err := p.LookPath(name)
	if err != nil {
		return Command{}, fmt.Errorf("%s: %w", name, ErrMissing)
	}
	return Command{Path: path}, nil
}

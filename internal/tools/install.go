package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rossedman/guardrails/internal/state"
)

// maxDownload bounds an artifact download.
var maxDownload int64 = 1 << 30

// Installer downloads, verifies and unpacks tools.
type Installer struct {
	m *Manager
	// HTTP fetches artifacts.
	HTTP *http.Client
	// Exec runs a command with extra environment, for uv and go installs.
	Exec func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
	// LockTimeout bounds waiting for a concurrent install of the same tool.
	LockTimeout time.Duration
}

// NewInstaller returns an Installer for m.
func NewInstaller(m *Manager) *Installer {
	return &Installer{
		m:           m,
		HTTP:        &http.Client{Timeout: 10 * time.Minute},
		Exec:        execEnv,
		LockTimeout: 10 * time.Minute,
	}
}

func execEnv(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

// Install installs the locked version of name, if not already installed.
// Concurrent installs of the same tool wait for each other.
func (in *Installer) Install(ctx context.Context, name string) error {
	m := in.m
	t, ok := m.Lock.Tools[name]
	if !ok {
		return fmt.Errorf("%s is not in the tools lock", name)
	}
	if m.Installed(name) {
		return nil
	}
	parent := filepath.Dir(m.Dir(name))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	unlock, err := state.Lock(m.Dir(name)+".lock", in.LockTimeout)
	if err != nil {
		return err
	}
	defer unlock()
	if m.Installed(name) {
		return nil
	}

	// Build in a staging directory and rename into place, so a crash never
	// leaves a half-installed tool that looks complete. Virtualenvs embed
	// their own path, so uv tools build in place instead; the .complete
	// marker, written last, is what makes them visible.
	stage := m.Dir(name)
	if t.Source == SourceUV {
		_ = os.RemoveAll(stage)
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return err
		}
	} else {
		stage, err = os.MkdirTemp(parent, "."+t.Version+"-*")
		if err != nil {
			return err
		}
	}
	defer func() {
		if !m.Installed(name) {
			_ = os.RemoveAll(stage)
		}
	}()

	switch t.Source {
	case SourceArchive, SourcePyPI:
		err = in.installArtifact(ctx, name, t, stage)
	case SourceUV:
		err = in.installUV(ctx, t, stage)
	case SourceGo:
		err = in.installGo(ctx, t, stage)
	default:
		err = fmt.Errorf("unknown source %q", t.Source)
	}
	if err != nil {
		return err
	}
	if stage != m.Dir(name) {
		_ = os.RemoveAll(m.Dir(name))
		if err := os.Rename(stage, m.Dir(name)); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(m.Dir(name), ".complete"), []byte(t.Version+"\n"), 0o644)
}

func (in *Installer) installArtifact(ctx context.Context, name string, t Tool, stage string) error {
	a, ok := t.Platforms[in.m.platform()]
	if !ok {
		return fmt.Errorf("%s has no artifact for %s", name, in.m.platform())
	}
	tmp := filepath.Join(stage, "download")
	if err := in.download(ctx, a, tmp); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	return extract(tmp, a.Format, a.Path, filepath.Join(stage, name))
}

// download fetches a.URL to dst and verifies its SHA-256.
func (in *Installer) download(ctx context.Context, a Artifact, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	resp, err := in.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", a.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", a.URL, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", a.URL, err)
	}
	if n > maxDownload {
		return fmt.Errorf("download %s: larger than %d bytes", a.URL, maxDownload)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", a.URL, got, a.SHA256)
	}
	return nil
}

// installUV creates an isolated venv with uv and installs the hash-pinned
// requirements into it. uv provisions a Python interpreter if the host has
// none, under the tools root so it is shared across tools.
func (in *Installer) installUV(ctx context.Context, t Tool, stage string) error {
	uv, err := in.m.WithBlocking(true).Resolve(ctx, "uv")
	if err != nil {
		return err
	}
	env := []string{
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(in.m.Root, "python"),
		"UV_CACHE_DIR=" + filepath.Join(in.m.Root, "uv-cache"),
		"UV_NO_CONFIG=1",
	}
	venv := filepath.Join(stage, "venv")
	if out, err := in.Exec(ctx, env, uv.Path, "venv", "--quiet", "--python", ">=3.10", venv); err != nil {
		return fmt.Errorf("uv venv: %w: %s", err, out)
	}
	req := filepath.Join(stage, "requirements.txt")
	if err := os.WriteFile(req, []byte(t.Requirements), 0o644); err != nil {
		return err
	}
	if out, err := in.Exec(ctx, env, uv.Path, "pip", "install", "--quiet", "--require-hashes",
		"--python", filepath.Join(venv, "bin", "python"), "-r", req); err != nil {
		return fmt.Errorf("uv pip install: %w: %s", err, out)
	}
	return nil
}

// installGo builds a Go tool with `go install`; the Go checksum database
// verifies the module contents.
func (in *Installer) installGo(ctx context.Context, t Tool, stage string) error {
	goBin, err := in.m.LookPath("go")
	if err != nil {
		return fmt.Errorf("go is required to install this tool: %w", err)
	}
	env := []string{"GOBIN=" + filepath.Join(stage, "bin"), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=auto"}
	if out, err := in.Exec(ctx, env, goBin, "install", t.Module+"@v"+t.Version); err != nil {
		return fmt.Errorf("go install: %w: %s", err, out)
	}
	return nil
}

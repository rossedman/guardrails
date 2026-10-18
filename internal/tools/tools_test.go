package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
)

const testPlatform = "testos/testarch"

var binary = []byte("#!/bin/sh\necho hello\n")

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func tarBytes(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// A directory and an unrelated file precede the member.
	if err := tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"dir/README", []byte("readme")}, {name, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(tarBytes(t, name, content)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarXz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(tarBytes(t, name, content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipBytes(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		data []byte
	}{{"other.txt", []byte("x")}, {name, content}} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// server serves files by path and counts requests.
type server struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  atomic.Int32
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		data, ok := s.files[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) add(path string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = data
	return s.URL + path
}

func newTestManager(t *testing.T, lock *Lock) *Manager {
	t.Helper()
	m := NewManager(lock, t.TempDir())
	m.Platform = testPlatform
	m.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	m.Spawn = func(...string) error { return nil }
	return m
}

func archiveTool(url, format, path string, data []byte) Tool {
	return Tool{Version: "1.0.0", Source: SourceArchive, Platforms: map[string]Artifact{
		testPlatform: {URL: url, SHA256: sum(data), Format: format, Path: path},
	}}
}

func TestInstallFormats(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	cases := []struct {
		name, format, member string
		data                 []byte
	}{
		{"targz", FormatTarGz, "dir/bin/tool", tarGz(t, "dir/bin/tool", binary)},
		{"tarxz", FormatTarXz, "tool-1.0/tool", tarXz(t, "tool-1.0/tool", binary)},
		{"zip", FormatZip, "pkg.data/scripts/tool", zipBytes(t, "pkg.data/scripts/tool", binary)},
		{"raw", FormatRaw, "", binary},
	}
	lock := &Lock{Tools: map[string]Tool{}}
	for _, c := range cases {
		lock.Tools[c.name] = archiveTool(srv.add("/"+c.name, c.data), c.format, c.member, c.data)
	}
	m := newTestManager(t, lock)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if m.Installed(c.name) {
				t.Fatal("installed before install")
			}
			cmd, err := m.WithBlocking(true).Resolve(context.Background(), c.name)
			if err != nil {
				t.Fatal(err)
			}
			if !cmd.Managed || cmd.Path != filepath.Join(m.Dir(c.name), c.name) {
				t.Fatalf("cmd = %+v", cmd)
			}
			got, err := os.ReadFile(cmd.Path)
			if err != nil || !bytes.Equal(got, binary) {
				t.Fatalf("content = %q, %v", got, err)
			}
			info, _ := os.Stat(cmd.Path)
			if info.Mode()&0o111 == 0 {
				t.Errorf("not executable: %v", info.Mode())
			}
			if !m.Installed(c.name) {
				t.Error("not marked installed")
			}
			// A second install is a no-op.
			before := srv.hits.Load()
			if err := m.Installer.Install(context.Background(), c.name); err != nil {
				t.Fatal(err)
			}
			if srv.hits.Load() != before {
				t.Error("reinstalled an installed tool")
			}
		})
	}
}

func TestInstallFailures(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	good := tarGz(t, "tool", binary)
	cases := []struct {
		name string
		tool Tool
		want string
	}{
		{"checksum", Tool{Version: "1", Source: SourceArchive, Platforms: map[string]Artifact{
			testPlatform: {URL: srv.add("/sum", good), SHA256: strings.Repeat("0", 64), Format: FormatTarGz, Path: "tool"},
		}}, "checksum mismatch"},
		{"404", archiveTool(srv.URL+"/missing", FormatRaw, "", binary), "404"},
		{"member", archiveTool(srv.add("/member", good), FormatTarGz, "nope", good), "not found in archive"},
		{"zip member", archiveTool(srv.add("/zipm", zipBytes(t, "a", binary)), FormatZip, "nope", zipBytes(t, "a", binary)), "not found in archive"},
		{"bad gzip", archiveTool(srv.add("/badgz", binary), FormatTarGz, "tool", binary), "gzip"},
		{"bad xz", archiveTool(srv.add("/badxz", binary), FormatTarXz, "tool", binary), "xz"},
		{"bad zip", archiveTool(srv.add("/badzip", binary), FormatZip, "tool", binary), "zip"},
		{"bad tar", archiveTool(srv.add("/badtar", gzOf(t, []byte("not a tar archive at all, definitely not"))), FormatTarGz, "tool", gzOf(t, []byte("not a tar archive at all, definitely not"))), "tar"},
		{"no platform", Tool{Version: "1", Source: SourceArchive, Platforms: map[string]Artifact{}}, "no artifact"},
		{"unknown source", Tool{Version: "1", Source: "magic"}, "unknown source"},
		{"bad url", archiveTool("http://[::1]:namedport", FormatRaw, "", binary), "invalid port"},
		{"unreachable", archiveTool("http://127.0.0.1:1/x", FormatRaw, "", binary), "download"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := newTestManager(t, &Lock{Tools: map[string]Tool{"tool": c.tool}})
			err := m.Installer.Install(context.Background(), "tool")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if m.Installed("tool") {
				t.Error("failed install marked complete")
			}
			// Staging directories are cleaned up.
			entries, _ := os.ReadDir(filepath.Join(m.Root, "tool"))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					t.Errorf("staging left behind: %s", e.Name())
				}
			}
		})
	}
	t.Run("unknown tool", func(t *testing.T) {
		m := newTestManager(t, &Lock{Tools: map[string]Tool{}})
		if err := m.Installer.Install(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "not in the tools lock") {
			t.Fatalf("err = %v", err)
		}
	})
}

func gzOf(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(b)
	_ = gz.Close()
	return buf.Bytes()
}

func TestSizeLimits(t *testing.T) {
	// Mutates package limits; not parallel.
	srv := newServer(t)
	big := bytes.Repeat([]byte("a"), 64)
	oldD, oldB := maxDownload, maxBinary
	t.Cleanup(func() { maxDownload, maxBinary = oldD, oldB })

	maxDownload = 16
	m := newTestManager(t, &Lock{Tools: map[string]Tool{"big": archiveTool(srv.add("/big", big), FormatRaw, "", big)}})
	if err := m.Installer.Install(context.Background(), "big"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("download limit: %v", err)
	}
	maxDownload = oldD
	maxBinary = 16
	if err := m.Installer.Install(context.Background(), "big"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("binary limit: %v", err)
	}
}

func TestExtractErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := Extract(filepath.Join(dir, "missing"), FormatRaw, "", filepath.Join(dir, "out")); err == nil {
		t.Error("raw missing source")
	}
	if err := Extract(filepath.Join(dir, "missing"), FormatTarGz, "x", filepath.Join(dir, "out")); err == nil {
		t.Error("targz missing source")
	}
	if err := Extract(filepath.Join(dir, "missing"), "rar", "x", filepath.Join(dir, "out")); err == nil || !strings.Contains(err.Error(), "unknown archive format") {
		t.Errorf("format: %v", err)
	}
	src := filepath.Join(dir, "raw")
	if err := os.WriteFile(src, binary, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Extract(src, FormatRaw, "", filepath.Join(dir, "no", "such", "dir", "out")); err == nil {
		t.Error("unwritable destination")
	}
}

func TestConcurrentInstall(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	data := tarGz(t, "tool", binary)
	m := newTestManager(t, &Lock{Tools: map[string]Tool{"tool": archiveTool(srv.add("/tool", data), FormatTarGz, "tool", data)}})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.Installer.Install(context.Background(), "tool")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !m.Installed("tool") {
		t.Fatal("not installed")
	}
	if hits := srv.hits.Load(); hits != 1 {
		t.Errorf("downloaded %d times, want 1", hits)
	}
}

func TestInstallLockTimeout(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, &Lock{Tools: map[string]Tool{"tool": archiveTool("http://unused", FormatRaw, "", binary)}})
	m.Installer.LockTimeout = 30_000_000 // 30ms
	if err := os.MkdirAll(filepath.Dir(m.Dir("tool")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Dir("tool")+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Installer.Install(context.Background(), "tool"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

type execCall struct {
	env  []string
	name string
	args []string
}

func TestInstallUV(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	lock := &Lock{Tools: map[string]Tool{
		"uv":   archiveTool(srv.add("/uv", binary), FormatRaw, "", binary),
		"ruff": {Version: "0.1.0", Source: SourceUV, Requirements: "ruff==0.1.0 --hash=sha256:abc\n", Entrypoint: "ruff"},
	}}
	m := newTestManager(t, lock)
	var calls []execCall
	m.Installer.Exec = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		calls = append(calls, execCall{env, name, args})
		if args[0] == "venv" {
			bin := filepath.Join(args[len(args)-1], "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(bin, "ruff"), binary, 0o755)
		}
		return nil, nil
	}
	cmd, err := m.WithBlocking(true).Resolve(context.Background(), "ruff")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Dir("ruff"), "venv", "bin", "ruff"); cmd.Path != want {
		t.Errorf("path = %s, want %s", cmd.Path, want)
	}
	if !m.Installed("uv") {
		t.Error("uv dependency not installed")
	}
	if len(calls) != 2 || calls[0].name != m.managedPath("uv") || calls[1].args[0] != "pip" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(strings.Join(calls[1].args, " "), "--require-hashes") {
		t.Error("pip install without --require-hashes")
	}
	if !strings.Contains(strings.Join(calls[0].env, " "), "UV_PYTHON_INSTALL_DIR="+filepath.Join(m.Root, "python")) {
		t.Errorf("env = %v", calls[0].env)
	}
	req, _ := os.ReadFile(filepath.Join(m.Dir("ruff"), "requirements.txt"))
	if string(req) != lock.Tools["ruff"].Requirements {
		t.Errorf("requirements = %q", req)
	}

	t.Run("failures", func(t *testing.T) {
		for _, failOn := range []string{"venv", "pip"} {
			m := newTestManager(t, lock)
			m.Installer.Exec = func(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
				if args[0] == failOn {
					return []byte("boom"), errors.New("exit 1")
				}
				return nil, nil
			}
			err := m.Installer.Install(context.Background(), "ruff")
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Errorf("%s: err = %v", failOn, err)
			}
			if _, statErr := os.Stat(m.Dir("ruff")); !os.IsNotExist(statErr) {
				t.Errorf("%s: failed uv install left %s", failOn, m.Dir("ruff"))
			}
		}
		// uv itself cannot be resolved.
		m := newTestManager(t, &Lock{Tools: map[string]Tool{"ruff": lock.Tools["ruff"]}})
		if err := m.Installer.Install(context.Background(), "ruff"); err == nil {
			t.Error("expected error without uv")
		}
	})
}

func TestInstallGo(t *testing.T) {
	t.Parallel()
	lock := &Lock{Tools: map[string]Tool{"goimports": {Version: "0.1.0", Source: SourceGo, Module: "golang.org/x/tools/cmd/goimports"}}}
	m := newTestManager(t, lock)
	if m.Supported("goimports") {
		t.Error("go tool supported without go")
	}
	if err := m.Installer.Install(context.Background(), "goimports"); err == nil || !strings.Contains(err.Error(), "go is required") {
		t.Fatalf("err = %v", err)
	}
	m.LookPath = func(n string) (string, error) {
		if n == "go" {
			return "/usr/bin/go", nil
		}
		return "", errors.New("no")
	}
	var got execCall
	m.Installer.Exec = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		got = execCall{env, name, args}
		for _, e := range env {
			if bin, ok := strings.CutPrefix(e, "GOBIN="); ok {
				_ = os.MkdirAll(bin, 0o755)
				return nil, os.WriteFile(filepath.Join(bin, "goimports"), binary, 0o755)
			}
		}
		return nil, nil
	}
	cmd, err := m.WithBlocking(true).Resolve(context.Background(), "goimports")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != filepath.Join(m.Dir("goimports"), "bin", "goimports") {
		t.Errorf("path = %s", cmd.Path)
	}
	if got.name != "/usr/bin/go" || strings.Join(got.args, " ") != "install golang.org/x/tools/cmd/goimports@v0.1.0" {
		t.Errorf("call = %+v", got)
	}
	m2 := newTestManager(t, lock)
	m2.LookPath = m.LookPath
	m2.Installer.Exec = func(context.Context, []string, string, ...string) ([]byte, error) {
		return []byte("compile error"), errors.New("exit 1")
	}
	if err := m2.Installer.Install(context.Background(), "goimports"); err == nil || !strings.Contains(err.Error(), "compile error") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveOrder(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	lock := &Lock{Tools: map[string]Tool{"tool": archiveTool(srv.add("/tool", binary), FormatRaw, "", binary)}}
	onPath := func(n string) (string, error) {
		if n == "tool" {
			return "/usr/local/bin/tool", nil
		}
		return "", errors.New("no")
	}
	ctx := context.Background()

	t.Run("path preferred", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.LookPath = onPath
		cmd, err := m.Resolve(ctx, "tool")
		if err != nil || cmd.Path != "/usr/local/bin/tool" || cmd.Managed {
			t.Fatalf("cmd = %+v, %v", cmd, err)
		}
	})
	t.Run("managed preferred installs over path", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.LookPath = onPath
		m.Prefer = PreferManaged
		cmd, err := m.WithBlocking(true).Resolve(ctx, "tool")
		if err != nil || !cmd.Managed {
			t.Fatalf("cmd = %+v, %v", cmd, err)
		}
		// Installed now: non-blocking resolution finds it.
		cmd, err = m.Resolve(ctx, "tool")
		if err != nil || !cmd.Managed {
			t.Fatalf("second = %+v, %v", cmd, err)
		}
	})
	t.Run("managed without install falls back to path", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.LookPath = onPath
		m.Prefer = PreferManaged
		m.AutoInstall = false
		cmd, err := m.Resolve(ctx, "tool")
		if err != nil || cmd.Path != "/usr/local/bin/tool" {
			t.Fatalf("cmd = %+v, %v", cmd, err)
		}
	})
	t.Run("no auto install", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.AutoInstall = false
		if _, err := m.Resolve(ctx, "tool"); !errors.Is(err, ErrMissing) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unsupported platform", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.Platform = "plan9/mips"
		if m.Supported("tool") {
			t.Error("supported on plan9")
		}
		if _, err := m.WithBlocking(true).Resolve(ctx, "tool"); !errors.Is(err, ErrMissing) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown tool", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		if m.Supported("nope") || m.Installed("nope") {
			t.Error("unknown tool reported available")
		}
		if _, err := m.Resolve(ctx, "nope"); !errors.Is(err, ErrMissing) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("blocking install failure", func(t *testing.T) {
		t.Parallel()
		bad := &Lock{Tools: map[string]Tool{"tool": archiveTool(srv.URL+"/nothing", FormatRaw, "", binary)}}
		m := newTestManager(t, bad)
		if _, err := m.WithBlocking(true).Resolve(ctx, "tool"); err == nil || !strings.Contains(err.Error(), "install tool") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("background install", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		var spawned [][]string
		m.Spawn = func(names ...string) error {
			spawned = append(spawned, names)
			return nil
		}
		if _, err := m.Resolve(ctx, "tool"); !errors.Is(err, ErrInstalling) {
			t.Fatalf("err = %v", err)
		}
		if len(spawned) != 1 || spawned[0][0] != "tool" {
			t.Fatalf("spawned = %v", spawned)
		}
		// While an install holds the lock, no second spawn.
		if err := os.MkdirAll(filepath.Dir(m.Dir("tool")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.Dir("tool")+".lock", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Resolve(ctx, "tool"); !errors.Is(err, ErrInstalling) {
			t.Fatalf("err = %v", err)
		}
		if len(spawned) != 1 {
			t.Errorf("spawned again: %v", spawned)
		}
	})
	t.Run("spawn failure", func(t *testing.T) {
		t.Parallel()
		m := newTestManager(t, lock)
		m.Spawn = func(...string) error { return errors.New("fork bomb") }
		if _, err := m.Resolve(ctx, "tool"); err == nil || !strings.Contains(err.Error(), "fork bomb") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSupportedUVDependsOnUV(t *testing.T) {
	t.Parallel()
	lock := &Lock{Tools: map[string]Tool{"ruff": {Version: "1", Source: SourceUV, Requirements: "x", Entrypoint: "ruff"}}}
	m := newTestManager(t, lock)
	if m.Supported("ruff") {
		t.Error("uv tool supported without uv in lock")
	}
	m.Lock.Tools["weird"] = Tool{Version: "1", Source: "weird"}
	if m.Supported("weird") {
		t.Error("unknown source supported")
	}
}

func TestPrefetch(t *testing.T) {
	t.Parallel()
	srv := newServer(t)
	lock := &Lock{Tools: map[string]Tool{
		"onpath":    archiveTool(srv.add("/a", binary), FormatRaw, "", binary),
		"installed": archiveTool(srv.add("/b", binary), FormatRaw, "", binary),
		"busy":      archiveTool(srv.add("/c", binary), FormatRaw, "", binary),
		"wanted":    archiveTool(srv.add("/d", binary), FormatRaw, "", binary),
	}}
	m := newTestManager(t, lock)
	m.LookPath = func(n string) (string, error) {
		if n == "onpath" {
			return "/bin/onpath", nil
		}
		return "", errors.New("no")
	}
	if err := m.Installer.Install(context.Background(), "installed"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.Dir("busy")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Dir("busy")+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var spawned []string
	m.Spawn = func(names ...string) error {
		spawned = append(spawned, names...)
		return nil
	}
	if err := m.Prefetch("onpath", "installed", "busy", "wanted", "unknown"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(spawned, ",") != "wanted" {
		t.Errorf("spawned = %v, want [wanted]", spawned)
	}
	spawned = nil
	if err := m.Prefetch("onpath"); err != nil || spawned != nil {
		t.Errorf("nothing to fetch: spawned %v, err %v", spawned, err)
	}
	// A managed preference fetches even what is on PATH.
	m.Prefer = PreferManaged
	if err := m.Prefetch("onpath"); err != nil || strings.Join(spawned, ",") != "onpath" {
		t.Errorf("managed prefetch: spawned %v, err %v", spawned, err)
	}
}

func TestPathResolver(t *testing.T) {
	t.Parallel()
	r := PathResolver{LookPath: func(n string) (string, error) {
		if n == "ok" {
			return "/bin/ok", nil
		}
		return "", errors.New("no")
	}}
	if c, err := r.Resolve(context.Background(), "ok"); err != nil || c.Path != "/bin/ok" {
		t.Errorf("ok: %+v %v", c, err)
	}
	if _, err := r.Resolve(context.Background(), "nope"); !errors.Is(err, ErrMissing) {
		t.Errorf("nope: %v", err)
	}
}

func TestManagerBasics(t *testing.T) {
	t.Parallel()
	m := NewManager(&Lock{Tools: map[string]Tool{}}, "/root")
	if m.Prefer != PreferPath || !m.AutoInstall || m.Blocking || m.Installer == nil || m.Spawn == nil {
		t.Errorf("defaults = %+v", m)
	}
	if m.platform() != Platform() {
		t.Error("default platform")
	}
	b := m.WithBlocking(true)
	if !b.Blocking || m.Blocking {
		t.Error("WithBlocking must copy")
	}
	if Platform() == "" || !strings.Contains(Platform(), "/") {
		t.Errorf("Platform() = %q", Platform())
	}
}

func TestParseLock(t *testing.T) {
	t.Parallel()
	good := strings.Repeat("a", 64)
	cases := []struct {
		name, json, want string
	}{
		{"bad json", `{`, "parse tools lock"},
		{"empty", `{}`, ""},
		{"no version", `{"tools":{"x":{"source":"go","module":"m"}}}`, "missing version"},
		{"unknown source", `{"tools":{"x":{"version":"1","source":"npm"}}}`, "unknown source"},
		{"no sha", `{"tools":{"x":{"version":"1","source":"archive","platforms":{"a/b":{"url":"u","format":"raw"}}}}}`, "missing url or sha256"},
		{"archive no path", `{"tools":{"x":{"version":"1","source":"archive","platforms":{"a/b":{"url":"u","sha256":"` + good + `","format":"zip"}}}}}`, "needs a path"},
		{"bad format", `{"tools":{"x":{"version":"1","source":"pypi","platforms":{"a/b":{"url":"u","sha256":"` + good + `","format":"7z"}}}}}`, "unknown format"},
		{"raw ok", `{"tools":{"x":{"version":"1","source":"archive","platforms":{"a/b":{"url":"u","sha256":"` + good + `","format":"raw"}}}}}`, ""},
		{"uv incomplete", `{"tools":{"x":{"version":"1","source":"uv"}}}`, "requirements and entrypoint"},
		{"go incomplete", `{"tools":{"x":{"version":"1","source":"go"}}}`, "needs a module"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l, err := ParseLock([]byte(c.json))
			if c.want == "" {
				if err != nil || l.Tools == nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// TestDefaultLockMatchesSpec keeps the embedded lock complete: every tool
// in tools.yaml is locked at its version, for every platform.
func TestDefaultLockMatchesSpec(t *testing.T) {
	t.Parallel()
	lock, err := DefaultLock()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Platforms []string `yaml:"platforms"`
		Tools     map[string]struct {
			Version string `yaml:"version"`
			Source  string `yaml:"source"`
		} `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Platforms) != 4 {
		t.Errorf("platforms = %v", spec.Platforms)
	}
	names := lock.Names()
	if len(names) != len(spec.Tools) {
		t.Errorf("lock has %d tools, spec %d", len(names), len(spec.Tools))
	}
	for name, s := range spec.Tools {
		got, ok := lock.Tools[name]
		if !ok {
			t.Errorf("%s not locked", name)
			continue
		}
		if got.Version != s.Version || got.Source != s.Source {
			t.Errorf("%s: lock %s/%s, spec %s/%s", name, got.Version, got.Source, s.Version, s.Source)
		}
		if s.Source == SourceArchive || s.Source == SourcePyPI {
			for _, p := range spec.Platforms {
				if _, ok := got.Platforms[p]; !ok {
					t.Errorf("%s: no artifact for %s", name, p)
				}
			}
		}
	}
	b, _ := json.Marshal(lock)
	if _, err := ParseLock(b); err != nil {
		t.Errorf("round trip: %v", err)
	}
}

func TestExecEnv(t *testing.T) {
	t.Parallel()
	out, err := execEnv(context.Background(), []string{"GUARDRAILS_TEST_VAR=hello"}, "sh", "-c", "echo $GUARDRAILS_TEST_VAR")
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// TestMain lets spawnDetached target the test binary: the spawned child is
// invoked as `<test binary> tools install ...` and exits at once instead of
// running the tests again.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "tools" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSpawnDetached(t *testing.T) {
	t.Parallel()
	if err := spawnDetached("nothing"); err != nil {
		t.Fatalf("spawn: %v", err)
	}
}

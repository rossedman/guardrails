package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/tools"
)

var toolBinary = []byte("#!/bin/sh\necho tool\n")

// withToolManager wires a manager over a lock with: "good" (downloadable),
// "bad" (404), "onpath" (found on PATH), "elsewhere" (no artifact for this
// platform).
func withToolManager(t *testing.T, f *fixture) *tools.Manager {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/good" {
			_, _ = w.Write(toolBinary)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	h := sha256.Sum256(toolBinary)
	sum := hex.EncodeToString(h[:])
	raw := func(url string) map[string]tools.Artifact {
		return map[string]tools.Artifact{tools.Platform(): {URL: url, SHA256: sum, Format: tools.FormatRaw}}
	}
	lock := &tools.Lock{Tools: map[string]tools.Tool{
		"good":      {Version: "1.0", Source: tools.SourceArchive, Platforms: raw(srv.URL + "/good")},
		"bad":       {Version: "2.0", Source: tools.SourceArchive, Platforms: raw(srv.URL + "/bad")},
		"onpath":    {Version: "3.0", Source: tools.SourceArchive, Platforms: raw(srv.URL + "/good")},
		"elsewhere": {Version: "4.0", Source: tools.SourceArchive, Platforms: map[string]tools.Artifact{"plan9/mips": {URL: "x", SHA256: sum, Format: tools.FormatRaw}}},
	}}
	var built *tools.Manager
	f.app.NewTools = func(root string) *tools.Manager {
		m := tools.NewManager(lock, root)
		m.LookPath = func(n string) (string, error) {
			if n == "onpath" {
				return "/usr/bin/onpath", nil
			}
			return "", errors.New("no")
		}
		m.Spawn = func(...string) error { return nil }
		built = m
		return m
	}
	f.app.toolManager()
	return built
}

func TestToolsCommand(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "tools", "list"); code != 1 || !strings.Contains(f.stderr.String(), "unavailable") {
		t.Fatalf("no manager: code %d, %q", code, f.stderr.String())
	}
	m := withToolManager(t, f)
	if m.Root != filepath.Join(f.data, "tools") {
		t.Errorf("root = %s", m.Root)
	}

	if code := f.run("", "tools"); code != 2 || !strings.Contains(f.stderr.String(), "usage: guardrails tools") {
		t.Errorf("no args: %d %q", code, f.stderr.String())
	}
	if code := f.run("", "tools", "frobnicate"); code != 2 || !strings.Contains(f.stderr.String(), `unknown tools command "frobnicate"`) {
		t.Errorf("unknown: %d %q", code, f.stderr.String())
	}

	// list before installing
	if code := f.run("", "tools", "list"); code != 0 {
		t.Fatalf("list: %d", code)
	}
	list := f.stdout.String()
	for _, want := range []string{"TOOL", "good           1.0", "not installed", "/usr/bin/onpath (PATH)", "unsupported on " + tools.Platform()} {
		if !strings.Contains(list, want) {
			t.Errorf("list missing %q:\n%s", want, list)
		}
	}

	// install specific tools: one good, one failing, one unknown
	code := f.run("", "tools", "install", "good", "bad", "nope")
	if code != 1 {
		t.Errorf("install code = %d", code)
	}
	if !strings.Contains(f.stdout.String(), "✓ good") || !strings.Contains(f.stderr.String(), "✗ bad") || !strings.Contains(f.stderr.String(), `unknown tool "nope"`) {
		t.Errorf("install output:\n%s\n%s", f.stdout.String(), f.stderr.String())
	}
	// again: already installed; quiet prints nothing
	if code := f.run("", "tools", "install", "good"); code != 0 || !strings.Contains(f.stdout.String(), "already installed") {
		t.Errorf("reinstall: %d %q", code, f.stdout.String())
	}
	if code := f.run("", "tools", "install", "--quiet", "good"); code != 0 || f.stdout.String() != "" {
		t.Errorf("quiet: %d %q", code, f.stdout.String())
	}
	if code := f.run("", "tools", "list"); code != 0 || !strings.Contains(f.stdout.String(), "managed") {
		t.Errorf("list after install:\n%s", f.stdout.String())
	}
	// install everything supported: "bad" fails, "elsewhere" is skipped
	if code := f.run("", "tools", "install", "-q"); code != 1 || strings.Contains(f.stderr.String(), "elsewhere") {
		t.Errorf("install all: %d %q", code, f.stderr.String())
	}

	// path
	if code := f.run("", "tools", "path", "good"); code != 0 || !strings.HasSuffix(strings.TrimSpace(f.stdout.String()), filepath.Join("good", "1.0", "good")) {
		t.Errorf("path good: %d %q", code, f.stdout.String())
	}
	if code := f.run("", "tools", "path", "onpath"); code != 0 || strings.TrimSpace(f.stdout.String()) != "/usr/bin/onpath" {
		t.Errorf("path onpath: %d %q", code, f.stdout.String())
	}
	if code := f.run("", "tools", "path", "bad"); code != 1 || !strings.Contains(f.stderr.String(), "install bad") {
		t.Errorf("path bad: %d %q", code, f.stderr.String())
	}
	if code := f.run("", "tools", "path"); code != 2 {
		t.Errorf("path without name: %d", code)
	}
}

func TestToolsListManagedPreference(t *testing.T) {
	f := newFixture(t)
	withToolManager(t, f)
	f.write(".guardrails.yaml", "version: 1\ntools:\n  prefer: managed\n")
	if code := f.run("", "tools", "list"); code != 0 {
		t.Fatalf("list: %d", code)
	}
	// With a managed preference an uninstalled tool on PATH still shows PATH
	// as the fallback.
	if !strings.Contains(f.stdout.String(), "onpath         3.0        archive  /usr/bin/onpath (PATH)") {
		t.Errorf("list:\n%s", f.stdout.String())
	}
}

func TestNewToolsBuildsManager(t *testing.T) {
	a := New()
	m := a.NewTools(t.TempDir())
	if m == nil || len(m.Lock.Tools) == 0 {
		t.Fatalf("NewTools() = %+v", m)
	}
}

func TestDoctorResolvesManagedTools(t *testing.T) {
	f := newFixture(t)
	withToolManager(t, f)
	f.mod.tools = []module.Tool{
		{Name: "good", Required: true},
		{Name: "elsewhere"},
	}

	// Fresh data dir: a supported tool installs on first use, which is not a
	// failure; an unsupported optional tool is reported but doesn't fail.
	if code := f.run("", "doctor"); code != 0 {
		t.Fatalf("doctor = %d\n%s", code, f.stdout.String())
	}
	out := f.stdout.String()
	if !strings.Contains(out, "○ good") || !strings.Contains(out, "installs v1.0 on first use") {
		t.Errorf("want installable good:\n%s", out)
	}
	if !strings.Contains(out, "✗ elsewhere") {
		t.Errorf("want unsupported elsewhere:\n%s", out)
	}

	// After installing, the managed copy is found.
	if code := f.run("", "tools", "install", "--quiet", "good"); code != 0 {
		t.Fatalf("install = %d: %s", code, f.stderr.String())
	}
	f.stdout.Reset()
	if code := f.run("", "doctor"); code != 0 {
		t.Fatalf("doctor = %d", code)
	}
	if out := f.stdout.String(); !strings.Contains(out, "✓ good") || !strings.Contains(out, "(managed)") {
		t.Errorf("want managed good:\n%s", out)
	}

	// With managed preferred, the managed copy wins over PATH.
	f.fake.Install("good")
	f.write(".guardrails.yaml", "version: 1\ntools:\n  prefer: managed\n")
	f.stdout.Reset()
	f.run("", "doctor")
	if out := f.stdout.String(); !strings.Contains(out, "(managed)") {
		t.Errorf("managed preference should use the managed copy:\n%s", out)
	}
}

func TestDoctorFailsWhenRequiredToolCannotInstall(t *testing.T) {
	f := newFixture(t)
	withToolManager(t, f)
	f.mod.tools = []module.Tool{{Name: "good", Required: true}}
	f.write(".guardrails.yaml", "version: 1\ntools:\n  install: never\n")
	if code := f.run("", "doctor"); code != 1 {
		t.Fatalf("doctor = %d, want 1\n%s", code, f.stdout.String())
	}
	if out := f.stdout.String(); !strings.Contains(out, "✗ good") {
		t.Errorf("want missing good:\n%s", out)
	}

	// Found on PATH with a managed preference but nothing managed installed:
	// PATH is the fallback.
	f.fake.Install("good")
	f.write(".guardrails.yaml", "version: 1\ntools:\n  install: never\n  prefer: managed\n")
	f.stdout.Reset()
	if code := f.run("", "doctor"); code != 0 {
		t.Fatalf("doctor = %d\n%s", code, f.stdout.String())
	}
	if out := f.stdout.String(); !strings.Contains(out, "(PATH)") {
		t.Errorf("want PATH fallback:\n%s", out)
	}
}

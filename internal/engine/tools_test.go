package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/hook"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/tools"
)

// withTools gives the harness a real tool manager over a small lock, with
// PATH lookups and background spawns faked. It returns the spawned names.
func withTools(t *testing.T, h *harness) *[]string {
	t.Helper()
	sha := strings.Repeat("a", 64)
	art := tools.Artifact{URL: "http://unused", SHA256: sha, Format: tools.FormatRaw}
	lock := &tools.Lock{Tools: map[string]tools.Tool{
		"tool-b":    {Version: "1", Source: tools.SourceArchive, Platforms: map[string]tools.Artifact{tools.Platform(): art}},
		"tool-done": {Version: "1", Source: tools.SourceArchive, Platforms: map[string]tools.Artifact{tools.Platform(): art}},
	}}
	m := tools.NewManager(lock, filepath.Join(h.data, "tools"))
	m.LookPath = func(string) (string, error) { return "", errors.New("no") }
	var spawned []string
	m.Spawn = func(names ...string) error {
		spawned = append(spawned, names...)
		return nil
	}
	// tool-done is already installed.
	if err := os.MkdirAll(m.Dir("tool-done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir("tool-done"), ".complete"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.eng.Tools = m
	h.mod.tools = []module.Tool{
		{Name: "tool-a", Required: true},                   // on PATH via the fake runner
		{Name: "tool-b", Required: true, Install: "get b"}, // installable
		{Name: "tool-done"},                                // managed copy present
		{Name: "tool-c", Install: "get c"},                 // not in the lock
	}
	return &spawned
}

func TestSessionStartPrefetchesTools(t *testing.T) {
	h := newHarness(t)
	spawned := withTools(t, h)
	out := h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "startup"}))
	msg := out.SystemMessage
	if !strings.Contains(msg, "installing pinned tools in the background: tool-b") {
		t.Errorf("message = %q", msg)
	}
	if !strings.Contains(msg, "missing tools: tool-c (stub, optional: get c)") {
		t.Errorf("message = %q", msg)
	}
	if strings.Contains(msg, "tool-a") || strings.Contains(msg, "tool-done") {
		t.Errorf("available tools reported: %q", msg)
	}
	if strings.Join(*spawned, ",") != "tool-b" {
		t.Errorf("spawned = %v", *spawned)
	}
}

func TestSessionStartSpawnFailure(t *testing.T) {
	h := newHarness(t)
	withTools(t, h)
	h.eng.Tools.Spawn = func(...string) error { return errors.New("no fork") }
	out := h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "startup"}))
	if !strings.Contains(out.SystemMessage, "could not start tool install: no fork") {
		t.Errorf("message = %q", out.SystemMessage)
	}
}

func TestSessionStartInstallNever(t *testing.T) {
	h := newHarness(t)
	spawned := withTools(t, h)
	h.config("version: 1\ntools:\n  install: never\n")
	out := h.handle(hook.SessionStart, h.in(hook.SessionStart, map[string]any{"source": "startup"}))
	if !strings.Contains(out.SystemMessage, "tool-b (stub, required: get b)") || strings.Contains(out.SystemMessage, "installing") {
		t.Errorf("message = %q", out.SystemMessage)
	}
	if len(*spawned) != 0 {
		t.Errorf("spawned = %v", *spawned)
	}
}

func TestManagerFollowsConfig(t *testing.T) {
	h := newHarness(t)
	if h.eng.manager(config.Default(), true) != nil {
		t.Fatal("manager without Tools")
	}
	env := h.eng.env(h.repo, config.Default(), true)
	if env.Tools != nil {
		t.Fatal("env.Tools set without a manager")
	}
	withTools(t, h)
	cfg := config.Default()
	cfg.Tools.Prefer = config.PreferManaged
	cfg.Tools.Install = config.InstallNever
	m := h.eng.manager(cfg, true)
	if m == nil || !m.Blocking || m.Prefer != tools.PreferManaged || m.AutoInstall {
		t.Fatalf("manager = %+v", m)
	}
	if h.eng.Tools.Blocking || h.eng.Tools.Prefer != tools.PreferPath {
		t.Error("manager() mutated the engine's manager")
	}
	env = h.eng.env(h.repo, config.Default(), false)
	mm, ok := env.Tools.(*tools.Manager)
	if !ok || mm.Blocking || !mm.AutoInstall {
		t.Fatalf("env.Tools = %#v", env.Tools)
	}
	// The non-blocking edit path spawns and reports installing.
	if _, err := env.Tool(context.Background(), "tool-b"); !errors.Is(err, tools.ErrInstalling) {
		t.Errorf("err = %v", err)
	}
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "guardrails_removed.md")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 0 {
		t.Fatalf("run = %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale page kept: %v", err)
	}
	gate, err := os.ReadFile(filepath.Join(dir, "guardrails_gate.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## guardrails gate", "--staged", "[guardrails](guardrails.md)"} {
		if !strings.Contains(string(gate), want) {
			t.Errorf("gate page lacks %q:\n%s", want, gate)
		}
	}
	if strings.Contains(string(gate), "Auto generated") {
		t.Error("pages carry a date stamp, so they drift every day")
	}
	if _, err := os.Stat(filepath.Join(dir, "guardrails_explain.md")); !os.IsNotExist(err) {
		t.Error("hidden commands get pages")
	}
}

func TestRunError(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{file}, &stderr); code != 1 || !strings.Contains(stderr.String(), "gendocs:") {
		t.Errorf("run into a file = %d %q", code, stderr.String())
	}
	if code := run([]string{"[bad"}, &stderr); code != 1 {
		t.Errorf("bad glob = %d", code)
	}
}

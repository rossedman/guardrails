package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

// TestIntegration runs the real ruff, mypy, uv and pytest over a tiny
// project. It needs the tools on PATH and network access for uv to fetch
// pytest, so it is skipped in -short mode or when a tool is missing.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	for _, tool := range []string{"ruff", "mypy", "uv"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	dir := t.TempDir()
	files := map[string]string{
		"pyproject.toml": "[tool.ruff]\nline-length = 100\n",
		"calc.py":        "import os\n\n\ndef add(a: int, b: int) -> int:\n    return a + b\n\n\ndef div(a: int, b: int) -> float:\n    if b == 0:\n        raise ValueError(\"b\")\n    return a / b\n\n\ndef bad() -> int:\n    return \"x\"\n",
		"test_calc.py":   "from calc import add\n\n\ndef test_add() -> None:\n    assert add(1, 2) == 3\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := module.Env{Runner: runner.Exec{}, Config: config.Default(), Repo: dir, DataDir: t.TempDir()}
	calc := filepath.Join(dir, "calc.py")
	ctx := context.Background()

	checks := New().Check(ctx, env, []string{calc})
	ruff, ok := find(checks, "ruff")
	if !ok || ruff.Issues[0].Rule != "F401" {
		t.Fatalf("ruff should flag the unused import: %+v", checks)
	}
	my, ok := find(checks, "mypy")
	if !ok || !strings.Contains(my.Issues[0].Message, "Incompatible return value") {
		t.Fatalf("mypy should flag bad(): %+v", checks)
	}

	res := New().Gate(ctx, env, module.GateInput{Files: []string{calc}})
	cov, ok := find(res.Checks, "coverage")
	if !ok || !strings.Contains(strings.Join(cov.Lines, "\n"), "calc.py") {
		t.Fatalf("coverage should fail and list calc.py: %+v", res.Checks)
	}
	if res.Total <= 0 || res.Total >= 90 {
		t.Fatalf("total %v", res.Total)
	}
	for _, litter := range []string{".coverage", ".mypy_cache", ".pytest_cache", "coverage.json"} {
		if _, err := os.Stat(filepath.Join(dir, litter)); err == nil {
			t.Errorf("gate left %s in the project", litter)
		}
	}
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	if !cfg.Enforcing() || !cfg.GoEnabled() {
		t.Error("defaults should enforce with Go enabled")
	}
	if cfg.Go.Coverage.Total != 90 || cfg.Gate.MaxConsecutiveBlocks != 3 {
		t.Errorf("unexpected defaults: %+v", cfg.Go.Coverage)
	}
}

func TestMergeKeyByKey(t *testing.T) {
	t.Parallel()
	cfg := Default()
	err := Merge(&cfg, []byte(`
mode: warn
go:
  coverage:
    total: 75
  lint:
    on_edit: full
guards:
  kubectl_safe_contexts: ["minikube"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeWarn || cfg.Enforcing() {
		t.Error("mode not merged")
	}
	if cfg.Go.Coverage.Total != 75 {
		t.Errorf("total = %v", cfg.Go.Coverage.Total)
	}
	// Siblings of overridden keys keep their defaults.
	if cfg.Go.Coverage.PerPackageFloor != 80 || !cfg.Go.Coverage.Ratchet {
		t.Errorf("siblings lost: %+v", cfg.Go.Coverage)
	}
	if cfg.Go.Lint.OnEdit != "full" || cfg.Go.Lint.Config != ".golangci.yml" {
		t.Errorf("lint = %+v", cfg.Go.Lint)
	}
	// Lists are replaced, not appended.
	if got := cfg.Guards.KubectlSafeContexts; len(got) != 1 || got[0] != "minikube" {
		t.Errorf("contexts = %v", got)
	}
	if cfg.Guards.AllowSuppressionTag != "guardrails:allow" {
		t.Error("unrelated guard key changed")
	}
}

func TestMergeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, data string
	}{
		{"unknown top-level key", "modee: warn\n"},
		{"unknown nested key", "go:\n  coverge: {total: 1}\n"},
		{"wrong type", "go:\n  coverage:\n    total: lots\n"},
		{"malformed", "go: [\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			if err := Merge(&cfg, []byte(tc.data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestMergeEmpty(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if err := Merge(&cfg, nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if cfg.Hash() != Default().Hash() {
		t.Error("empty merge changed config")
	}
}

func TestEnabled(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if err := Merge(&cfg, []byte("go:\n  enabled: false\n")); err != nil {
		t.Fatal(err)
	}
	if cfg.GoEnabled() {
		t.Error("go should be disabled")
	}
	if err := Merge(&cfg, []byte("go:\n  enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	if !cfg.GoEnabled() {
		t.Error("go should be enabled")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"version", func(c *Config) { c.Version = 2 }, "unsupported version 2"},
		{"mode", func(c *Config) { c.Mode = "loud" }, `mode: "loud"`},
		{"total high", func(c *Config) { c.Go.Coverage.Total = 101 }, "go.coverage.total"},
		{"floor negative", func(c *Config) { c.Go.Coverage.PerPackageFloor = -1 }, "go.coverage.per_package_floor"},
		{"patch", func(c *Config) { c.Go.PatchCoverage = 200 }, "go.patch_coverage"},
		{"mutation", func(c *Config) { c.Go.Mutation.MinScore = 101 }, "go.mutation.min_score"},
		{"python total", func(c *Config) { c.Python.Coverage.Total = -5 }, "python.coverage.total"},
		{"python patch", func(c *Config) { c.Python.PatchCoverage = 101 }, "python.patch_coverage"},
		{"on_edit", func(c *Config) { c.Go.Lint.OnEdit = "sometimes" }, "go.lint.on_edit"},
		{"typecheck", func(c *Config) { c.Python.Typecheck = "pytype" }, "python.typecheck"},
		{"binary", func(c *Config) { c.Terraform.Binary = "pulumi" }, "terraform.binary"},
		{"blocks", func(c *Config) { c.Gate.MaxConsecutiveBlocks = 0 }, "gate.max_consecutive_blocks"},
		{"timeout", func(c *Config) { c.Gate.TimeoutSeconds = 0 }, "gate.timeout_seconds"},
		{"tag", func(c *Config) { c.Guards.AllowSuppressionTag = "" }, "guards.allow_suppression_tag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Version, cfg.Mode = 9, "x"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "version") || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestHash(t *testing.T) {
	t.Parallel()
	a, b := Default(), Default()
	if a.Hash() != b.Hash() || len(a.Hash()) != 16 {
		t.Errorf("hash unstable or wrong length: %q", a.Hash())
	}
	b.Go.Coverage.Total = 50
	if a.Hash() == b.Hash() {
		t.Error("hash ignores changes")
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLayers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yaml")
	repo := filepath.Join(dir, "repo")
	write(t, global, "go:\n  coverage:\n    total: 85\n    per_package_floor: 70\ngate:\n  timeout_seconds: 60\n")
	write(t, filepath.Join(repo, RepoFile), "go:\n  coverage:\n    total: 95\n")

	cfg, err := Load(global, repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Go.Coverage.Total != 95 {
		t.Errorf("repo should win: total = %v", cfg.Go.Coverage.Total)
	}
	if cfg.Go.Coverage.PerPackageFloor != 70 || cfg.Gate.TimeoutSeconds != 60 {
		t.Errorf("global lost: %+v %+v", cfg.Go.Coverage, cfg.Gate)
	}
}

func TestLoadMissingFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.yaml"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hash() != Default().Hash() {
		t.Error("missing files should yield defaults")
	}
	cfg, err = Load("", "")
	if err != nil || cfg.Hash() != Default().Hash() {
		t.Errorf("empty paths: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(dir string) (global, repo string)
		want  string
	}{
		{"unknown key in repo", func(dir string) (string, string) {
			write(t, filepath.Join(dir, RepoFile), "colour: blue\n")
			return "", dir
		}, RepoFile},
		{"unknown key in global", func(dir string) (string, string) {
			p := filepath.Join(dir, "g.yaml")
			write(t, p, "colour: blue\n")
			return p, ""
		}, "g.yaml"},
		{"invalid values", func(dir string) (string, string) {
			write(t, filepath.Join(dir, RepoFile), "mode: loud\n")
			return "", dir
		}, "mode"},
		{"unreadable", func(dir string) (string, string) {
			// A directory where the file should be cannot be read.
			if err := os.MkdirAll(filepath.Join(dir, RepoFile), 0o755); err != nil {
				t.Fatal(err)
			}
			return "", dir
		}, "read"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, r := tc.setup(t.TempDir())
			_, err := Load(g, r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestGlobalPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := GlobalPath(); got != filepath.Join("/xdg", "guardrails", "config.yaml") {
		t.Errorf("xdg: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/me")
	if got := GlobalPath(); got != filepath.Join("/home/me", ".config", "guardrails", "config.yaml") {
		t.Errorf("home: %q", got)
	}
	t.Setenv("HOME", "")
	if got := GlobalPath(); got != "" {
		t.Errorf("no home: %q", got)
	}
}

// TestSchemaMatchesConfig keeps the JSON Schema and the Go structs in sync:
// every yaml key in Default() must be a schema property.
func TestSchemaMatchesConfig(t *testing.T) {
	t.Parallel()
	var schema map[string]any
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	var cfg map[string]any
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var walk func(prefix string, cfg, schema map[string]any)
	walk = func(prefix string, cfg, schema map[string]any) {
		props, _ := schema["properties"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Errorf("%s: schema must set additionalProperties: false", prefix)
		}
		for k, v := range cfg {
			sub, ok := props[k].(map[string]any)
			if !ok {
				t.Errorf("%s%s: missing from schema", prefix, k)
				continue
			}
			if m, ok := v.(map[string]any); ok {
				walk(prefix+k+".", m, sub)
			}
		}
	}
	walk("", cfg, schema)
}

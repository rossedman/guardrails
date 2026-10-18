// Package config loads and validates Guardrails configuration.
//
// Configuration is layered: built-in defaults, then the global file
// (~/.config/guardrails/config.yaml), then the repository's .guardrails.yaml.
// Each layer overrides the previous one key by key. Unknown keys are an
// error so a typo can never silently disable a check.
package config

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RepoFile is the name of the per-repository config file.
const RepoFile = ".guardrails.yaml"

// Mode values.
const (
	ModeEnforce = "enforce"
	ModeWarn    = "warn"
)

// Schema is the JSON Schema for the config file, for editors and CI.
//
//go:embed schema.json
var Schema []byte

// Config is the full Guardrails configuration.
type Config struct {
	Version   int       `yaml:"version" json:"version"`
	Mode      string    `yaml:"mode" json:"mode"`
	Go        Go        `yaml:"go" json:"go"`
	Python    Python    `yaml:"python" json:"python"`
	Terraform Terraform `yaml:"terraform" json:"terraform"`
	Docker    Docker    `yaml:"docker" json:"docker"`
	YAML      YAMLLint  `yaml:"yaml" json:"yaml"`
	Shell     Shell     `yaml:"shell" json:"shell"`
	Tools     Tools     `yaml:"tools" json:"tools"`
	Guards    Guards    `yaml:"guards" json:"guards"`
	Gate      Gate      `yaml:"gate" json:"gate"`
}

// Go configures the Go module.
type Go struct {
	Enabled       *bool      `yaml:"enabled" json:"enabled,omitempty"`
	Coverage      GoCoverage `yaml:"coverage" json:"coverage"`
	PatchCoverage float64    `yaml:"patch_coverage" json:"patch_coverage"`
	Lint          GoLint     `yaml:"lint" json:"lint"`
	Race          bool       `yaml:"race" json:"race"`
	Vuln          bool       `yaml:"vuln" json:"vuln"`
	Mutation      Mutation   `yaml:"mutation" json:"mutation"`
	Bench         Bench      `yaml:"bench" json:"bench"`
	Fuzz          Fuzz       `yaml:"fuzz" json:"fuzz"`
}

// GoCoverage configures Go coverage thresholds.
type GoCoverage struct {
	Total           float64  `yaml:"total" json:"total"`
	PerPackageFloor float64  `yaml:"per_package_floor" json:"per_package_floor"`
	Ratchet         bool     `yaml:"ratchet" json:"ratchet"`
	Exclude         []string `yaml:"exclude" json:"exclude"`
}

// GoLint configures golangci-lint.
type GoLint struct {
	Config string `yaml:"config" json:"config"`
	OnEdit string `yaml:"on_edit" json:"on_edit"`
}

// Mutation tools.
const (
	MutationBuiltin = "builtin"
	MutationMutmut  = "mutmut"
	MutationOff     = "off"
)

// Mutation configures mutation testing of changed functions.
type Mutation struct {
	// Tool is builtin (Go) or mutmut (Python, planned), or off.
	Tool          string  `yaml:"tool" json:"tool"`
	MinScore      float64 `yaml:"min_score" json:"min_score"`
	BudgetSeconds int     `yaml:"budget_seconds" json:"budget_seconds"`
}

// Bench configures benchmark requirements and regression checks.
type Bench struct {
	RequireOnHotPaths bool    `yaml:"require_on_hot_paths" json:"require_on_hot_paths"`
	RegressionPct     float64 `yaml:"regression_pct" json:"regression_pct"`
	Count             int     `yaml:"count" json:"count"`
	// Benchtime is passed to -benchtime for each run.
	Benchtime string `yaml:"benchtime" json:"benchtime"`
	// Packages are performance-critical: every changed function in them
	// is a hot path. Patterns are module-relative, e.g. "internal/codec/...".
	Packages      []string `yaml:"packages" json:"packages"`
	BudgetSeconds int      `yaml:"budget_seconds" json:"budget_seconds"`
}

// Fuzz configures fuzzing requirements and short fuzz runs.
type Fuzz struct {
	RequireOnParsers bool   `yaml:"require_on_parsers" json:"require_on_parsers"`
	Fuzztime         string `yaml:"fuzztime" json:"fuzztime"`
	// BudgetSeconds caps the total time spent fuzzing at Stop.
	BudgetSeconds int `yaml:"budget_seconds" json:"budget_seconds"`
}

// Python configures the Python module.
type Python struct {
	Enabled       *bool          `yaml:"enabled" json:"enabled,omitempty"`
	Coverage      PythonCoverage `yaml:"coverage" json:"coverage"`
	PatchCoverage float64        `yaml:"patch_coverage" json:"patch_coverage"`
	Typecheck     string         `yaml:"typecheck" json:"typecheck"`
	Strict        bool           `yaml:"strict" json:"strict"`
	Mutation      Mutation       `yaml:"mutation" json:"mutation"`
}

// PythonCoverage configures Python coverage thresholds.
type PythonCoverage struct {
	Total float64 `yaml:"total" json:"total"`
	// Ratchet keeps each package at or above its SessionStart coverage.
	Ratchet bool `yaml:"ratchet" json:"ratchet"`
}

// Terraform configures the Terraform module.
type Terraform struct {
	Enabled *bool  `yaml:"enabled" json:"enabled,omitempty"`
	Binary  string `yaml:"binary" json:"binary"`
	// Scan is the config scanner: trivy or off.
	Scan string `yaml:"scan" json:"scan"`
	// Tflint runs tflint on edit and at the gate.
	Tflint bool `yaml:"tflint" json:"tflint"`
	// RequireVersionConstraints fails providers without a version.
	RequireVersionConstraints bool `yaml:"require_version_constraints" json:"require_version_constraints"`
}

// Docker configures the Docker module.
type Docker struct {
	Enabled           *bool `yaml:"enabled" json:"enabled,omitempty"`
	RequireDigestPins bool  `yaml:"require_digest_pins" json:"require_digest_pins"`
	RequireNonRoot    bool  `yaml:"require_non_root" json:"require_non_root"`
	// BuildCheck runs `docker build --check` when a Docker daemon is
	// available.
	BuildCheck bool `yaml:"build_check" json:"build_check"`
}

// YAMLLint configures the YAML module.
type YAMLLint struct {
	Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
	// Actionlint checks GitHub Actions workflows.
	Actionlint  bool        `yaml:"actionlint" json:"actionlint"`
	Kubeconform Kubeconform `yaml:"kubeconform" json:"kubeconform"`
}

// Kubeconform configures manifest validation.
type Kubeconform struct {
	Enabled           bool   `yaml:"enabled" json:"enabled"`
	KubernetesVersion string `yaml:"kubernetes_version" json:"kubernetes_version"`
}

// Shell configures the shell module.
type Shell struct {
	Enabled *bool `yaml:"enabled" json:"enabled,omitempty"`
	// Severity is the minimum shellcheck severity reported.
	Severity string `yaml:"severity" json:"severity"`
}

// Tool provisioning values.
const (
	InstallAuto   = "auto"
	InstallNever  = "never"
	PreferPath    = "path"
	PreferManaged = "managed"
)

// Tools configures how Guardrails finds and installs external tools.
type Tools struct {
	// Install is auto (download pinned, checksum-verified tools on demand)
	// or never (use only what is installed).
	Install string `yaml:"install" json:"install"`
	// Prefer is path (use installed tools first) or managed (always use
	// the pinned versions).
	Prefer string `yaml:"prefer" json:"prefer"`
}

// Guards configures the PreToolUse policy guards.
type Guards struct {
	AllowSuppressionTag string   `yaml:"allow_suppression_tag" json:"allow_suppression_tag"`
	KubectlSafeContexts []string `yaml:"kubectl_safe_contexts" json:"kubectl_safe_contexts"`
	ProtectedBranches   []string `yaml:"protected_branches" json:"protected_branches"`
}

// Gate configures the Stop gate.
type Gate struct {
	MaxConsecutiveBlocks int `yaml:"max_consecutive_blocks" json:"max_consecutive_blocks"`
	TimeoutSeconds       int `yaml:"timeout_seconds" json:"timeout_seconds"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Version: 1,
		Mode:    ModeEnforce,
		Go: Go{
			Coverage: GoCoverage{
				Total:           90,
				PerPackageFloor: 80,
				Ratchet:         true,
				Exclude:         []string{"**/mocks/**", "**/*_mock.go", "**/*.pb.go", "cmd/**/main.go"},
			},
			PatchCoverage: 90,
			Lint:          GoLint{Config: ".golangci.yml", OnEdit: "fast"},
			Race:          true,
			Vuln:          true,
			Mutation:      Mutation{Tool: MutationBuiltin, MinScore: 70, BudgetSeconds: 45},
			Bench:         Bench{RequireOnHotPaths: true, RegressionPct: 10, Count: 6, Benchtime: "100ms", BudgetSeconds: 40},
			Fuzz:          Fuzz{RequireOnParsers: true, Fuzztime: "10s", BudgetSeconds: 30},
		},
		Python: Python{
			Coverage:      PythonCoverage{Total: 90, Ratchet: true},
			PatchCoverage: 90,
			Typecheck:     "mypy",
			Strict:        true,
			Mutation:      Mutation{Tool: MutationOff, MinScore: 70, BudgetSeconds: 45},
		},
		Terraform: Terraform{Binary: "terraform", Scan: "trivy", Tflint: true, RequireVersionConstraints: true},
		Docker:    Docker{RequireDigestPins: true, RequireNonRoot: true, BuildCheck: true},
		YAML:      YAMLLint{Actionlint: true, Kubeconform: Kubeconform{Enabled: true, KubernetesVersion: "1.31"}},
		Shell:     Shell{Severity: "style"},
		Tools:     Tools{Install: InstallAuto, Prefer: PreferPath},
		Guards: Guards{
			AllowSuppressionTag: "guardrails:allow",
			KubectlSafeContexts: []string{"kind-*", "*-dev"},
			ProtectedBranches:   []string{"main", "master"},
		},
		Gate: Gate{MaxConsecutiveBlocks: 3, TimeoutSeconds: 170},
	}
}

// Enforcing reports whether failures should block.
func (c Config) Enforcing() bool { return c.Mode != ModeWarn }

// GoEnabled reports whether the Go module is on. A nil Enabled means on.
func (c Config) GoEnabled() bool { return on(c.Go.Enabled) }

// PythonEnabled reports whether the Python module is on.
func (c Config) PythonEnabled() bool { return on(c.Python.Enabled) }

// ShellEnabled reports whether the shell module is on.
func (c Config) ShellEnabled() bool { return on(c.Shell.Enabled) }

// YAMLEnabled reports whether the YAML module is on.
func (c Config) YAMLEnabled() bool { return on(c.YAML.Enabled) }

// TerraformEnabled reports whether the Terraform module is on.
func (c Config) TerraformEnabled() bool { return on(c.Terraform.Enabled) }

// DockerEnabled reports whether the Docker module is on.
func (c Config) DockerEnabled() bool { return on(c.Docker.Enabled) }

func on(b *bool) bool { return b == nil || *b }

// Hash returns a stable digest of the effective config, used to key caches.
func (c Config) Hash() string {
	b, _ := yaml.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// GlobalPath returns the global config path, honoring XDG_CONFIG_HOME.
func GlobalPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "guardrails", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "guardrails", "config.yaml")
}

// Load returns the effective config for repoRoot, layering the global file
// at globalPath (if it exists) and the repo file (if it exists) over the
// defaults.
func Load(globalPath, repoRoot string) (Config, error) {
	cfg := Default()
	if globalPath != "" {
		if err := mergeFile(&cfg, globalPath); err != nil {
			return Config{}, err
		}
	}
	if repoRoot != "" {
		if err := mergeFile(&cfg, filepath.Join(repoRoot, RepoFile)); err != nil {
			return Config{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := Merge(cfg, data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Merge decodes data over cfg. Keys present in data replace the values in
// cfg; absent keys keep their current value. Unknown keys are an error.
func Merge(cfg *Config, data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}

// Validate checks value ranges and enums.
func (c Config) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if c.Version != 1 {
		add("version: unsupported version %d (want 1)", c.Version)
	}
	if c.Mode != ModeEnforce && c.Mode != ModeWarn {
		add("mode: %q must be enforce or warn", c.Mode)
	}
	pct := func(name string, v float64) {
		if v < 0 || v > 100 {
			add("%s: %v must be between 0 and 100", name, v)
		}
	}
	pct("go.coverage.total", c.Go.Coverage.Total)
	pct("go.coverage.per_package_floor", c.Go.Coverage.PerPackageFloor)
	pct("go.patch_coverage", c.Go.PatchCoverage)
	pct("go.mutation.min_score", c.Go.Mutation.MinScore)
	pct("python.coverage.total", c.Python.Coverage.Total)
	pct("python.patch_coverage", c.Python.PatchCoverage)
	oneOf := func(name, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		add("%s: %q must be one of %s", name, v, strings.Join(allowed, ", "))
	}
	oneOf("go.lint.on_edit", c.Go.Lint.OnEdit, "fast", "full", "off")
	oneOf("python.typecheck", c.Python.Typecheck, "mypy", "pyright", "off")
	oneOf("go.mutation.tool", c.Go.Mutation.Tool, MutationBuiltin, MutationOff)
	oneOf("python.mutation.tool", c.Python.Mutation.Tool, MutationMutmut, MutationOff)
	if _, err := time.ParseDuration(c.Go.Fuzz.Fuzztime); err != nil {
		add("go.fuzz.fuzztime: %q is not a duration", c.Go.Fuzz.Fuzztime)
	}
	if _, err := time.ParseDuration(c.Go.Bench.Benchtime); err != nil && !strings.HasSuffix(c.Go.Bench.Benchtime, "x") {
		add("go.bench.benchtime: %q is not a duration or count like 100x", c.Go.Bench.Benchtime)
	}
	if c.Go.Bench.Count < 2 {
		add("go.bench.count: must be at least 2 for a significance test")
	}
	oneOf("terraform.binary", c.Terraform.Binary, "terraform", "tofu")
	oneOf("terraform.scan", c.Terraform.Scan, "trivy", "off")
	oneOf("shell.severity", c.Shell.Severity, "style", "info", "warning", "error")
	oneOf("tools.install", c.Tools.Install, InstallAuto, InstallNever)
	oneOf("tools.prefer", c.Tools.Prefer, PreferPath, PreferManaged)
	if c.Gate.MaxConsecutiveBlocks < 1 {
		add("gate.max_consecutive_blocks: must be at least 1")
	}
	if c.Gate.TimeoutSeconds < 1 {
		add("gate.timeout_seconds: must be at least 1")
	}
	if c.Guards.AllowSuppressionTag == "" {
		add("guards.allow_suppression_tag: must not be empty")
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

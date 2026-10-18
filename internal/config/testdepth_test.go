package config

import (
	"strings"
	"testing"
)

func TestValidateTestDepth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string // empty means valid
	}{
		{"defaults", func(*Config) {}, ""},
		{"go mutation off", func(c *Config) { c.Go.Mutation.Tool = MutationOff }, ""},
		{"go mutation gremlins", func(c *Config) { c.Go.Mutation.Tool = "gremlins" }, "go.mutation.tool"},
		{"go mutation mutmut", func(c *Config) { c.Go.Mutation.Tool = MutationMutmut }, "go.mutation.tool"},
		{"python mutmut", func(c *Config) { c.Python.Mutation.Tool = MutationMutmut }, ""},
		{"python builtin", func(c *Config) { c.Python.Mutation.Tool = MutationBuiltin }, "python.mutation.tool"},
		{"fuzztime minutes", func(c *Config) { c.Go.Fuzz.Fuzztime = "1m" }, ""},
		{"fuzztime bad", func(c *Config) { c.Go.Fuzz.Fuzztime = "ten seconds" }, "go.fuzz.fuzztime"},
		{"fuzztime empty", func(c *Config) { c.Go.Fuzz.Fuzztime = "" }, "go.fuzz.fuzztime"},
		{"benchtime count", func(c *Config) { c.Go.Bench.Benchtime = "1000x" }, ""},
		{"benchtime duration", func(c *Config) { c.Go.Bench.Benchtime = "2s" }, ""},
		{"benchtime bad", func(c *Config) { c.Go.Bench.Benchtime = "fast" }, "go.bench.benchtime"},
		{"bench count 2", func(c *Config) { c.Go.Bench.Count = 2 }, ""},
		{"bench count 1", func(c *Config) { c.Go.Bench.Count = 1 }, "go.bench.count"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestTestDepthDefaults(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.Go.Mutation.Tool != MutationBuiltin || c.Python.Mutation.Tool != MutationOff {
		t.Errorf("mutation tools = %q / %q", c.Go.Mutation.Tool, c.Python.Mutation.Tool)
	}
	if c.Go.Bench.Count < 2 || c.Go.Bench.BudgetSeconds <= 0 || c.Go.Fuzz.BudgetSeconds <= 0 || c.Go.Bench.Benchtime == "" {
		t.Errorf("bench/fuzz defaults = %+v / %+v", c.Go.Bench, c.Go.Fuzz)
	}
}

func TestMergeTestDepthConfig(t *testing.T) {
	t.Parallel()
	cfg := Default()
	data := []byte(`go:
  mutation: { tool: off }
  bench:
    packages: ["internal/codec/..."]
    benchtime: 500x
    budget_seconds: 20
  fuzz: { budget_seconds: 15, fuzztime: 5s }
`)
	if err := Merge(&cfg, data); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Go.Mutation.Tool != MutationOff || cfg.Go.Mutation.MinScore != 70 {
		t.Errorf("mutation = %+v", cfg.Go.Mutation)
	}
	if len(cfg.Go.Bench.Packages) != 1 || cfg.Go.Bench.Benchtime != "500x" || cfg.Go.Bench.BudgetSeconds != 20 || cfg.Go.Bench.Count != 6 {
		t.Errorf("bench = %+v", cfg.Go.Bench)
	}
	if cfg.Go.Fuzz.BudgetSeconds != 15 || cfg.Go.Fuzz.Fuzztime != "5s" || !cfg.Go.Fuzz.RequireOnParsers {
		t.Errorf("fuzz = %+v", cfg.Go.Fuzz)
	}
}

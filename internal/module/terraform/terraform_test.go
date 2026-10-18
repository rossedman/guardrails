package terraform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// fixture is a repo with one Terraform module in infra/.
type fixture struct {
	repo, dir, main, vars string
	env                   module.Env
	fake                  *runner.Fake
}

func newFixture(t *testing.T, installed ...string) *fixture {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "infra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{repo: repo, dir: dir, main: filepath.Join(dir, "main.tf"), vars: filepath.Join(dir, "dev.tfvars")}
	write(t, f.main, "resource \"aws_s3_bucket\" \"b\" {}\n")
	write(t, f.vars, "x = 1\n")
	f.fake = runner.NewFake(installed...)
	f.env = module.Env{Runner: f.fake, Config: config.Default(), Repo: repo, DataDir: filepath.Join(t.TempDir(), "data")}
	return f
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func all() []string { return []string{"terraform", "tofu", "tflint", "trivy"} }

const tflintJSON = `{"issues":[
 {"rule":{"name":"terraform_typed_variables","severity":"warning"},"message":"x has no type","range":{"filename":"main.tf","start":{"line":1,"column":1}}},
 {"rule":{"name":"terraform_naming","severity":"notice"},"message":"ignored notice","range":{"filename":"main.tf","start":{"line":2,"column":1}}},
 {"rule":{"name":"terraform_unused","severity":"error"},"message":"other file","range":{"filename":"other.tf","start":{"line":3,"column":1}}}
],"errors":[]}`

const validateJSON = `{"format_version":"1.0","valid":false,"diagnostics":[
 {"severity":"error","summary":"Reference to undeclared input variable","detail":"An input variable named z has not been declared. Did you mean x?","range":{"filename":"main.tf","start":{"line":3,"column":11}}},
 {"severity":"warning","summary":"Deprecated","detail":"ignored"},
 {"severity":"error","summary":"No range","detail":""}
]}`

const trivyJSON = `{"Results":[
 {"Target":".","Misconfigurations":null},
 {"Target":"main.tf","Misconfigurations":[
  {"ID":"AWS-0086","AVDID":"AVD-AWS-0086","Title":"S3 should block public ACL","Message":"No public access block","Severity":"HIGH","Status":"FAIL","CauseMetadata":{"StartLine":1}},
  {"ID":"AWS-0001","Title":"Passed","Severity":"HIGH","Status":"PASS","CauseMetadata":{"StartLine":1}},
  {"ID":"AWS-0099","Title":"No AVD id","Message":"m","Severity":"CRITICAL","CauseMetadata":{"StartLine":2}}
 ]},
 {"Target":"other.tf","Misconfigurations":[{"ID":"AWS-0002","Title":"untouched","Severity":"HIGH","Status":"FAIL","CauseMetadata":{"StartLine":1}}]}
]}`

func TestBasics(t *testing.T) {
	m := New()
	if m.Name() != "terraform" {
		t.Errorf("Name = %q", m.Name())
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Error("enabled by default")
	}
	off := false
	cfg.Terraform.Enabled = &off
	if m.Enabled(cfg) {
		t.Error("enabled: false must disable")
	}
	for path, want := range map[string]bool{
		"main.tf": true, "envs/dev.tfvars": true, "main.tf.json": false, "x.go": false,
		"mod/.terraform/modules/x/main.tf": false, "README.md": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
	var names []string
	for _, tl := range m.Tools() {
		names = append(names, tl.Name)
		if !tl.Required || tl.Install == "" {
			t.Errorf("tool %s should be required with an install hint", tl.Name)
		}
	}
	if !slices.Equal(names, []string{"terraform", "tflint", "trivy"}) {
		t.Errorf("Tools = %v", names)
	}
	if got := m.tool("tofu"); got.Name != "tofu" || !strings.Contains(got.Install, "opentofu") {
		t.Errorf("tool(tofu) = %+v", got)
	}
}

func TestStandards(t *testing.T) {
	m := New()
	cfg := config.Default()
	got := m.Standards(cfg)
	for _, want := range []string{"terraform fmt", "tflint", "init -backend=false", "trivy", "version constraint", "Plan only"} {
		if !strings.Contains(got, want) {
			t.Errorf("Standards missing %q: %s", want, got)
		}
	}
	cfg.Terraform = config.Terraform{Binary: "tofu", Scan: "off"}
	got = m.Standards(cfg)
	for _, absent := range []string{"tflint", "trivy", "version constraint"} {
		if strings.Contains(got, absent) {
			t.Errorf("Standards should not mention %q: %s", absent, got)
		}
	}
	if !strings.Contains(got, "tofu fmt") {
		t.Errorf("Standards should name tofu: %s", got)
	}
}

func TestFix(t *testing.T) {
	t.Run("formats each existing file", func(t *testing.T) {
		f := newFixture(t, all()...)
		res := New().Fix(context.Background(), f.env, []string{f.main, filepath.Join(f.dir, "gone.tf")})
		if len(res.Notes) != 0 {
			t.Errorf("notes = %v", res.Notes)
		}
		if !f.fake.Ran("terraform fmt -no-color " + f.main) {
			t.Errorf("fmt not run: %v", f.fake.Calls())
		}
		if f.fake.Ran("terraform fmt -no-color " + filepath.Join(f.dir, "gone.tf")) {
			t.Error("fmt ran on a deleted file")
		}
	})
	t.Run("missing binary is a note", func(t *testing.T) {
		f := newFixture(t)
		res := New().Fix(context.Background(), f.env, []string{f.main, f.vars})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "terraform is not installed") {
			t.Errorf("notes = %v", res.Notes)
		}
	})
	t.Run("tofu binary", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Binary = "tofu"
		New().Fix(context.Background(), f.env, []string{f.main})
		if !f.fake.Ran("tofu fmt") {
			t.Errorf("tofu fmt not run: %v", f.fake.Calls())
		}
	})
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name      string
		installed []string
		tflint    bool
		res       runner.Result
		err       error
		wantRules []string
	}{
		{"issues in edited file only, notices dropped", all(), true, runner.Result{Stdout: tflintJSON, ExitCode: 2}, nil, []string{"terraform_typed_variables"}},
		{"clean", all(), true, runner.Result{Stdout: `{"issues":[],"errors":[]}`}, nil, nil},
		{"plugins need init", all(), true, runner.Result{Stdout: `{"issues":[],"errors":[{"message":"Plugin aws not found. Did you run tflint --init?"}]}`, ExitCode: 1}, nil, nil},
		{"unparseable output", all(), true, runner.Result{Stdout: "panic", ExitCode: 1}, nil, nil},
		{"tflint fails to start", all(), true, runner.Result{}, errors.New("boom"), nil},
		{"tflint missing", []string{"terraform"}, true, runner.Result{}, nil, nil},
		{"tflint disabled", all(), false, runner.Result{Stdout: tflintJSON}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.installed...)
			f.env.Config.Terraform.Tflint = tc.tflint
			if tc.err != nil {
				f.fake.OnError("tflint", tc.err)
			} else {
				f.fake.On("tflint", tc.res)
			}
			checks := New().Check(context.Background(), f.env, []string{f.main})
			var rules []string
			for _, c := range checks {
				if c.Name != "tflint" || !c.Failed {
					t.Errorf("unexpected check %+v", c)
				}
				for _, is := range c.Issues {
					rules = append(rules, is.Rule)
					if is.File != "infra/main.tf" {
						t.Errorf("issue file = %q", is.File)
					}
				}
			}
			if !slices.Equal(rules, tc.wantRules) {
				t.Errorf("rules = %v, want %v", rules, tc.wantRules)
			}
			if f.fake.Ran("tflint --init") || f.fake.Ran("tflint --chdir="+f.dir+" --init") {
				t.Error("Check must never run tflint --init")
			}
		})
	}
}

// tfScript answers terraform commands run through `env`.
type tfScript struct {
	init, validate runner.Result
	initErr        error
	validateErr    error
	calls          [][]string
}

func (s *tfScript) on(fake *runner.Fake) {
	fake.OnFunc("env", func(c runner.Call) (runner.Result, error) {
		s.calls = append(s.calls, c.Args)
		switch {
		case slices.Contains(c.Args, "init"):
			if s.initErr != nil {
				return runner.Result{}, s.initErr
			}
			return s.init, nil
		case slices.Contains(c.Args, "validate"):
			if s.validateErr != nil {
				return runner.Result{}, s.validateErr
			}
			return s.validate, nil
		}
		return runner.Result{}, nil
	})
}

func checkNames(checks []feedback.Check) []string {
	var out []string
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return out
}

func findCheck(checks []feedback.Check, name string) (feedback.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return feedback.Check{}, false
}

func TestGateAllChecksFail(t *testing.T) {
	f := newFixture(t, all()...)
	write(t, f.main, `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}
resource "aws_s3_bucket" "b" {}
resource "google_storage_bucket" "g" {}
`)
	f.fake.On("tflint --chdir="+f.dir+" --init", runner.Result{})
	f.fake.On("tflint --chdir="+f.dir+" --format=json", runner.Result{Stdout: tflintJSON, ExitCode: 2})
	f.fake.On("trivy", runner.Result{Stdout: trivyJSON})
	s := &tfScript{validate: runner.Result{Stdout: validateJSON, ExitCode: 1}}
	s.on(f.fake)

	res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
	if got := checkNames(res.Checks); !slices.Equal(got, []string{"validate", "tflint", "trivy", "versions"}) {
		t.Fatalf("checks = %v (notes %v)", got, res.Notes)
	}
	if !slices.Equal(res.Ran, []string{"tflint", "init", "validate", "trivy", "versions"}) {
		t.Errorf("Ran = %v", res.Ran)
	}
	if res.Total != -1 {
		t.Errorf("Total = %v, want -1", res.Total)
	}

	v, _ := findCheck(res.Checks, "validate")
	if len(v.Issues) != 2 || v.Issues[0].File != "infra/main.tf" || v.Issues[0].Line != 3 ||
		!strings.Contains(v.Issues[0].Message, "has not been declared.") || strings.Contains(v.Issues[0].Message, "Did you mean") {
		t.Errorf("validate issues = %+v", v.Issues)
	}
	if v.Issues[1].File != "infra" || v.Issues[1].Line != 0 {
		t.Errorf("rangeless diagnostic = %+v", v.Issues[1])
	}

	tr, _ := findCheck(res.Checks, "trivy")
	var ids []string
	for _, is := range tr.Issues {
		ids = append(ids, is.Rule)
	}
	if !slices.Equal(ids, []string{"AVD-AWS-0086", "AWS-0099"}) {
		t.Errorf("trivy ids = %v (untouched other.tf and PASS must be dropped)", ids)
	}

	vc, _ := findCheck(res.Checks, "versions")
	if len(vc.Issues) != 2 || !strings.Contains(vc.Issues[0].Message, `"aws" in required_providers has no version`) ||
		!strings.Contains(vc.Issues[1].Message, `"google" is used but not declared`) || vc.Issues[1].File != "infra/main.tf" {
		t.Errorf("version issues = %+v", vc.Issues)
	}

	// Every terraform call uses data dirs outside the repo.
	for _, args := range s.calls {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "TF_DATA_DIR="+filepath.Join(f.env.DataDir, "terraform")) ||
			!strings.Contains(joined, "TF_PLUGIN_CACHE_DIR=") || strings.Contains(joined, "-lockfile=readonly") {
			t.Errorf("terraform call = %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".terraform.lock.hcl")); !os.IsNotExist(err) {
		t.Error("lock file left behind")
	}
}

func TestGateLockfile(t *testing.T) {
	t.Run("existing lock is read only and kept", func(t *testing.T) {
		f := newFixture(t, all()...)
		lock := filepath.Join(f.dir, ".terraform.lock.hcl")
		write(t, lock, "# lock\n")
		f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan, f.env.Config.Terraform.RequireVersionConstraints = false, "off", false
		s := &tfScript{validate: runner.Result{Stdout: `{"valid":true,"diagnostics":[]}`}}
		s.on(f.fake)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		if len(res.Checks) != 0 {
			t.Errorf("checks = %+v", res.Checks)
		}
		if !slices.Contains(s.calls[0], "-lockfile=readonly") {
			t.Errorf("init args = %v", s.calls[0])
		}
		if _, err := os.Stat(lock); err != nil {
			t.Error("existing lock must be kept")
		}
	})
	t.Run("new lock written by init is removed", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan = false, "off"
		f.fake.OnFunc("env", func(c runner.Call) (runner.Result, error) {
			if slices.Contains(c.Args, "init") {
				write(t, filepath.Join(c.Dir, ".terraform.lock.hcl"), "# new\n")
			}
			return runner.Result{Stdout: `{"diagnostics":[]}`}, nil
		})
		New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		if _, err := os.Stat(filepath.Join(f.dir, ".terraform.lock.hcl")); !os.IsNotExist(err) {
			t.Error("lock written by the gate must be removed")
		}
	})
}

func TestGateInitAndValidateFailures(t *testing.T) {
	cases := []struct {
		name      string
		script    tfScript
		wantCheck string
		wantNote  string
	}{
		{"network failure is a note", tfScript{init: runner.Result{Stderr: "Error: Failed to query available provider packages", ExitCode: 1}}, "", "could not reach a registry"},
		{"config failure blocks", tfScript{init: runner.Result{Stderr: "\nError: Unsupported block type\n\nmore detail\n", ExitCode: 1}}, "init", ""},
		{"init cannot start", tfScript{initErr: errors.New("exec failed")}, "", "init could not run"},
		{"validate cannot start", tfScript{validateErr: errors.New("exec failed")}, "", "validate could not run"},
		{"validate output unparseable", tfScript{validate: runner.Result{Stdout: "not json", ExitCode: 1}}, "", "could not parse validate output"},
		{"validate bad json", tfScript{validate: runner.Result{Stdout: "{nope", ExitCode: 1}}, "", "could not parse validate output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, all()...)
			f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan, f.env.Config.Terraform.RequireVersionConstraints = false, "off", false
			s := tc.script
			s.on(f.fake)
			res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
			if got := checkNames(res.Checks); tc.wantCheck == "" && len(got) != 0 || tc.wantCheck != "" && !slices.Equal(got, []string{tc.wantCheck}) {
				t.Errorf("checks = %v", got)
			}
			if tc.wantCheck == "init" {
				c := res.Checks[0]
				if len(c.Lines) != 1 || c.Lines[0] != "infra: Error: Unsupported block type" {
					t.Errorf("init lines = %v", c.Lines)
				}
			}
			if tc.wantNote != "" && (len(res.Notes) != 1 || !strings.Contains(res.Notes[0], tc.wantNote)) {
				t.Errorf("notes = %v, want %q", res.Notes, tc.wantNote)
			}
		})
	}
}

func TestGateToolsAndScanFailures(t *testing.T) {
	t.Run("missing tools fail one tools check", func(t *testing.T) {
		f := newFixture(t)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		c, ok := findCheck(res.Checks, "tools")
		if !ok || !strings.Contains(c.Summary, "terraform is not installed") || !strings.Contains(c.Summary, "tflint") ||
			!strings.Contains(c.Summary, "trivy") || !strings.Contains(c.Fix, "tools.install: auto") {
			t.Errorf("tools check = %+v", c)
		}
		// Version constraints need no tool and still run.
		if !slices.Contains(res.Ran, "versions") || slices.Contains(res.Ran, "init") {
			t.Errorf("Ran = %v", res.Ran)
		}
	})
	t.Run("tflint init and lint failures are notes", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Scan, f.env.Config.Terraform.RequireVersionConstraints = "off", false
		f.fake.On("tflint --chdir="+f.dir+" --init", runner.Result{ExitCode: 1})
		f.fake.On("tflint --chdir="+f.dir+" --format=json", runner.Result{Stdout: "boom", ExitCode: 1})
		(&tfScript{validate: runner.Result{Stdout: `{"diagnostics":[]}`}}).on(f.fake)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		if len(res.Checks) != 0 || len(res.Notes) != 2 {
			t.Errorf("checks = %v notes = %v", res.Checks, res.Notes)
		}
	})
	t.Run("trivy failures are notes", func(t *testing.T) {
		for name, setup := range map[string]func(*runner.Fake){
			"exit code": func(fk *runner.Fake) {
				fk.On("trivy", runner.Result{Stderr: "FATAL failed to download checks bundle", ExitCode: 1})
			},
			"start error": func(fk *runner.Fake) { fk.OnError("trivy", errors.New("no exec")) },
			"bad json":    func(fk *runner.Fake) { fk.On("trivy", runner.Result{Stdout: "{"}) },
		} {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t, all()...)
				f.env.Config.Terraform.Tflint, f.env.Config.Terraform.RequireVersionConstraints = false, false
				(&tfScript{validate: runner.Result{Stdout: `{"diagnostics":[]}`}}).on(f.fake)
				setup(f.fake)
				res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
				if len(res.Checks) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "trivy") {
					t.Errorf("checks = %v notes = %v", res.Checks, res.Notes)
				}
			})
		}
	})
	t.Run("unparseable module is a note", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan = false, "off"
		write(t, f.main, "resource {")
		(&tfScript{validate: runner.Result{Stdout: `{"diagnostics":[]}`}}).on(f.fake)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "could not parse infra") {
			t.Errorf("notes = %v", res.Notes)
		}
	})
	t.Run("data dir cannot be created", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan, f.env.Config.Terraform.RequireVersionConstraints = false, "off", false
		blocker := filepath.Join(t.TempDir(), "file")
		write(t, blocker, "x")
		f.env.DataDir = blocker
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{f.main}})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "init could not run") {
			t.Errorf("notes = %v", res.Notes)
		}
	})
}

func TestGateScopeAndAll(t *testing.T) {
	t.Run("no existing modules", func(t *testing.T) {
		f := newFixture(t, all()...)
		res := New().Gate(context.Background(), f.env, module.GateInput{Files: []string{filepath.Join(f.repo, "gone", "x.tf")}})
		if len(res.Checks) != 0 || len(res.Ran) != 0 {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("all mode walks the repo and keeps every issue", func(t *testing.T) {
		f := newFixture(t, all()...)
		f.env.Config.Terraform.Tflint, f.env.Config.Terraform.Scan = false, "trivy"
		write(t, filepath.Join(f.repo, ".hidden", "x.tf"), "")
		write(t, filepath.Join(f.repo, "node_modules", "x.tf"), "")
		write(t, filepath.Join(f.repo, "envs", "prod", "main.tf"), "resource \"aws_x\" \"y\" {}\n")
		f.fake.On("trivy", runner.Result{Stdout: trivyJSON})
		s := &tfScript{validate: runner.Result{Stdout: `{"diagnostics":[]}`}}
		s.on(f.fake)
		res := New().Gate(context.Background(), f.env, module.GateInput{All: true})
		if len(s.calls) != 4 { // init + validate for envs/prod and infra
			t.Errorf("terraform calls = %d", len(s.calls))
		}
		tr, _ := findCheck(res.Checks, "trivy")
		if len(tr.Issues) != 6 { // three findings per module, untouched files included
			t.Errorf("trivy issues = %d", len(tr.Issues))
		}
		vc, _ := findCheck(res.Checks, "versions")
		if len(vc.Issues) != 2 {
			t.Errorf("version issues = %+v", vc.Issues)
		}
	})
}

func TestVersionConstraints(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			"all declared with versions",
			map[string]string{"versions.tf": `
terraform {
  required_version = ">= 1.6"
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 5.0" }
    random = "~> 3.0"
    null   = { "version" = "3.2.0" }
  }
}`, "main.tf": `
provider "aws" { region = "us-east-1" }
resource "aws_s3_bucket" "b" {}
resource "random_id" "r" { byte_length = 2 }
data "null_data_source" "n" {}
resource "terraform_data" "t" {}
`},
			nil,
		},
		{
			"missing version and undeclared provider",
			map[string]string{"main.tf": `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
    empty = ""
  }
}
provider "google" {}
resource "aws_s3_bucket" "b" {}
resource "azurerm_thing" "a" { provider = azurerm.west }
ephemeral "vault_token" "v" {}
`},
			[]string{
				`main.tf:4 provider "aws" in required_providers has no version constraint`,
				`main.tf:5 provider "empty" in required_providers has no version constraint`,
				`main.tf:10 provider "azurerm" is used but not declared in required_providers`,
				`main.tf:8 provider "google" is used but not declared in required_providers`,
				`main.tf:11 provider "vault" is used but not declared in required_providers`,
			},
		},
		{
			"provider alias meta-argument maps to its provider",
			map[string]string{"main.tf": `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}
resource "awsx_thing" "x" { provider = aws.east }
`},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				write(t, filepath.Join(dir, name), content)
			}
			issues, err := VersionConstraints(dir)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, is := range issues {
				got = append(got, filepath.Base(is.File)+":"+strconv.Itoa(is.Line)+" "+is.Message)
			}
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("issues:\n got %q\nwant %q", got, want)
			}
		})
	}
	t.Run("parse and read errors", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "bad.tf"), "resource {")
		write(t, filepath.Join(dir, "good.tf"), `provider "aws" {}`)
		if err := os.Mkdir(filepath.Join(dir, "dir.tf"), 0o755); err != nil {
			t.Fatal(err)
		}
		issues, err := VersionConstraints(dir)
		if err == nil {
			t.Fatal("want parse error")
		}
		if len(issues) != 1 || !strings.Contains(issues[0].Message, `"aws" is used`) {
			t.Errorf("issues from parseable files should still be reported: %+v", issues)
		}
	})
	t.Run("bad glob", func(t *testing.T) {
		if _, err := VersionConstraints("["); err == nil {
			t.Error("want glob error")
		}
	})
}

func TestHelpers(t *testing.T) {
	if got := errorLines("one\n\ntwo\nthree", 2); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("errorLines fallback = %v", got)
	}
	if got := errorLines("noise\n│ Error: bad\nError: worse", 5); !slices.Equal(got, []string{"Error: bad", "Error: worse"}) {
		t.Errorf("errorLines = %v", got)
	}
	if got := firstSentence("One. Two."); got != "One." {
		t.Errorf("firstSentence = %q", got)
	}
	if got := firstSentence("no period"); got != "no period" {
		t.Errorf("firstSentence = %q", got)
	}
	if display("/repo", "/elsewhere/x.tf") != "/elsewhere/x.tf" {
		t.Error("paths outside the repo stay absolute")
	}
	if plural(1) != "" || plural(2) != "s" {
		t.Error("plural")
	}
	if got := appendUnique([]string{"a"}, "a", "b", "b"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("appendUnique = %v", got)
	}
	if skipUnreadable(nil) != nil {
		t.Error("unreadable file is ignored")
	}
	dir := t.TempDir()
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !errors.Is(skipUnreadable(e), filepath.SkipDir) {
			t.Error("unreadable directory is skipped")
		}
	}
}

// TestIntegration runs the real terraform binary on a module with no
// providers: fmt fixes formatting, and validate reports an undeclared
// variable through the gate.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		return
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Log("terraform not on PATH; integration test not run")
		return
	}
	repo := t.TempDir()
	dir := filepath.Join(repo, "mod")
	main := filepath.Join(dir, "main.tf")
	write(t, main, "variable \"x\" {\ntype=string\n}\noutput \"y\" {\n  value = var.z\n}\n")
	cfg := config.Default()
	cfg.Terraform.Tflint, cfg.Terraform.Scan = false, "off"
	env := module.Env{
		Runner: runner.Exec{}, Config: cfg, Repo: repo, DataDir: t.TempDir(),
		Tools: tools.PathResolver{LookPath: exec.LookPath},
	}
	m := New()
	if res := m.Fix(context.Background(), env, []string{main}); len(res.Notes) != 0 {
		t.Fatalf("fix notes = %v", res.Notes)
	}
	data, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "  type = string") {
		t.Errorf("fmt did not format:\n%s", data)
	}
	res := m.Gate(context.Background(), env, module.GateInput{Files: []string{main}})
	v, ok := findCheck(res.Checks, "validate")
	if !ok || len(v.Issues) != 1 || v.Issues[0].File != "mod/main.tf" || v.Issues[0].Line != 5 {
		t.Fatalf("validate = %+v (checks %v, notes %v)", v, checkNames(res.Checks), res.Notes)
	}
	if _, err := os.Stat(filepath.Join(dir, ".terraform")); !os.IsNotExist(err) {
		t.Error("the gate must not write .terraform into the module")
	}
}

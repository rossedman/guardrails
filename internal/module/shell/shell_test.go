package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

// fakeTools resolves every tool to its bare name, except those listed in
// errs.
type fakeTools struct{ errs map[string]error }

func (f fakeTools) Resolve(_ context.Context, name string) (tools.Command, error) {
	if err, ok := f.errs[name]; ok {
		return tools.Command{}, err
	}
	return tools.Command{Path: name}, nil
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newEnv(t *testing.T, r runner.Runner, res tools.Resolver) module.Env {
	t.Helper()
	return module.Env{Runner: r, Config: config.Default(), Repo: t.TempDir(), Tools: res}
}

func report(comments ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"comments": comments})
	return string(b)
}

func comment(file string, line, code int, msg string) map[string]any {
	return map[string]any{"file": file, "line": line, "column": 3, "level": "warning", "code": code, "message": msg}
}

func TestMetadata(t *testing.T) {
	t.Parallel()
	m := New()
	if m.Name() != "shell" {
		t.Errorf("Name = %q", m.Name())
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Error("enabled by default")
	}
	off := false
	cfg.Shell.Enabled = &off
	if m.Enabled(cfg) {
		t.Error("enabled: false must disable")
	}
	var names []string
	for _, tl := range m.Tools() {
		if !tl.Required {
			t.Errorf("%s should be required", tl.Name)
		}
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "shellcheck,shfmt" {
		t.Errorf("tools = %v", names)
	}
	if got := m.tool("shfmt"); got.Name != "shfmt" || got.Install == "" {
		t.Errorf("tool(shfmt) = %+v", got)
	}
	if got := m.tool("nope"); got.Name != "nope" || got.Install != "" {
		t.Errorf("tool(nope) = %+v", got)
	}
	std := m.Standards(config.Default())
	for _, want := range []string{"shfmt", "shellcheck", "severity style"} {
		if !strings.Contains(std, want) {
			t.Errorf("Standards missing %q: %s", want, std)
		}
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := New()
	cases := []struct {
		name, file, content string
		want                bool
	}{
		{"sh ext", "a.sh", "", true},
		{"bash ext", "a.bash", "", true},
		{"ksh ext", "a.ksh", "", true},
		{"zsh ext", "a.zsh", "#!/bin/zsh\n", false},
		{"go file", "a.go", "#!/bin/sh\n", false},
		{"bin sh", "s1", "#!/bin/sh\necho\n", true},
		{"env bash", "s2", "#!/usr/bin/env bash\n", true},
		{"bash flags", "s3", "#!/bin/bash -eu\n", true},
		{"dash", "s4", "#!/bin/dash\n", true},
		{"env -S", "s5", "#!/usr/bin/env -S bash -e\n", true},
		{"zsh shebang", "s6", "#!/bin/zsh\n", false},
		{"fish", "s7", "#!/usr/bin/env fish\n", false},
		{"python", "s8", "#!/usr/bin/env python3\n", false},
		{"no shebang", "s9", "echo hi\n", false},
		{"empty shebang", "s10", "#!\n", false},
		{"bare env", "s11", "#!/usr/bin/env\n", false},
		{"env only flags", "s12", "#!/usr/bin/env -i\n", false},
		{"empty file", "s13", "", false},
	}
	for _, tc := range cases {
		p := write(t, dir, tc.file, tc.content)
		if got := m.Match(p); got != tc.want {
			t.Errorf("%s: Match = %v, want %v", tc.name, got, tc.want)
		}
	}
	if m.Match(filepath.Join(dir, "missing")) {
		t.Error("missing extensionless file must not match")
	}
	if m.Match(dir) {
		t.Error("a directory must not match")
	}
}

func TestFix(t *testing.T) {
	t.Parallel()
	m := New()

	f := runner.NewFake()
	env := newEnv(t, f, fakeTools{})
	p := write(t, env.Repo, "a.sh", "echo\n")
	res := m.Fix(context.Background(), env, []string{p, filepath.Join(env.Repo, "gone.sh")})
	if len(res.Notes) != 0 || !f.Ran("shfmt -w "+p) {
		t.Errorf("notes=%v calls=%v", res.Notes, f.Calls())
	}
	if strings.Contains(f.Calls()[0].String(), "gone.sh") {
		t.Error("missing files must not be passed to shfmt")
	}

	f = runner.NewFake()
	env = newEnv(t, f, fakeTools{errs: map[string]error{"shfmt": fmt.Errorf("shfmt: %w", tools.ErrInstalling)}})
	p = write(t, env.Repo, "a.sh", "echo\n")
	res = m.Fix(context.Background(), env, []string{p})
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "being installed") || !strings.Contains(res.Notes[0], "not formatted") {
		t.Errorf("notes = %v", res.Notes)
	}

	f = runner.NewFake()
	env = newEnv(t, f, fakeTools{})
	if res := m.Fix(context.Background(), env, nil); len(res.Notes) != 0 || len(f.Calls()) != 0 {
		t.Errorf("no files: notes=%v calls=%v", res.Notes, f.Calls())
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	m := New()
	ctx := context.Background()

	t.Run("issues in the file", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		p := write(t, env.Repo, "bin/a.sh", "echo $1\n")
		other := filepath.Join(env.Repo, "other.sh")
		f.On("shellcheck", runner.Result{ExitCode: 1, Stdout: report(
			comment(p, 1, 2086, "Double quote to prevent globbing and word splitting."),
			comment(other, 4, 2034, "unused"),
			comment("bin/a.sh", 2, 2050, "This expression is constant."),
		)})
		checks := m.Check(ctx, env, []string{p})
		if len(checks) != 1 {
			t.Fatalf("checks = %+v", checks)
		}
		c := checks[0]
		if c.Name != "shellcheck" || !c.Failed || c.Summary != "2 issues" || len(c.Issues) != 2 {
			t.Fatalf("check = %+v", c)
		}
		want := feedback.Issue{File: "bin/a.sh", Line: 1, Col: 3, Rule: "SC2086", Message: "Double quote to prevent globbing and word splitting."}
		if c.Issues[0] != want {
			t.Errorf("issue = %+v", c.Issues[0])
		}
		if c.Issues[1].Rule != "SC2050" || c.Issues[1].File != "bin/a.sh" {
			t.Errorf("relative path issue = %+v", c.Issues[1])
		}
		if !f.Ran("shellcheck -f json1 -S style " + p) {
			t.Errorf("calls = %v", f.Calls())
		}
	})

	cases := []struct {
		name  string
		res   runner.Result
		err   error
		tools fakeTools
		want  string // summary of the single expected check, or "" for none
	}{
		{name: "clean", res: runner.Result{Stdout: `{"comments":[]}`}},
		{name: "only other files", res: runner.Result{ExitCode: 1, Stdout: report(comment("/elsewhere/x.sh", 1, 1000, "x"))}},
		{name: "installing", tools: fakeTools{errs: map[string]error{"shellcheck": fmt.Errorf("x: %w", tools.ErrInstalling)}}},
		{name: "missing", tools: fakeTools{errs: map[string]error{"shellcheck": fmt.Errorf("x: %w", tools.ErrMissing)}}},
		{name: "unparseable but exit 0", res: runner.Result{Stdout: "garbage"}},
		{name: "unparseable failure", res: runner.Result{ExitCode: 3, Stderr: "boom\nmore"}, want: "shellcheck failed to run"},
		{name: "start error", err: errors.New("exec format error"), want: "could not run: exec format error"},
		{name: "single issue", res: runner.Result{ExitCode: 1, Stdout: report(comment("a.sh", 1, 2086, "q"))}, want: "1 issue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := runner.NewFake().On("shellcheck", tc.res)
			if tc.err != nil {
				f.OnError("shellcheck", tc.err)
			}
			env := newEnv(t, f, tc.tools)
			p := write(t, env.Repo, "a.sh", "echo\n")
			checks := m.Check(ctx, env, []string{p})
			if tc.want == "" {
				if len(checks) != 0 {
					t.Fatalf("checks = %+v", checks)
				}
				return
			}
			if len(checks) != 1 || checks[0].Summary != tc.want {
				t.Fatalf("checks = %+v", checks)
			}
		})
	}

	t.Run("no existing files", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		if checks := m.Check(ctx, env, []string{filepath.Join(env.Repo, "gone.sh"), env.Repo}); checks != nil {
			t.Errorf("checks = %+v", checks)
		}
		if len(f.Calls()) != 0 {
			t.Errorf("calls = %v", f.Calls())
		}
	})

	t.Run("severity from config", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		env.Config.Shell.Severity = "error"
		p := write(t, env.Repo, "a.sh", "echo\n")
		m.Check(ctx, env, []string{p})
		if !f.Ran("shellcheck -f json1 -S error") {
			t.Errorf("calls = %v", f.Calls())
		}
	})

	t.Run("PATH fallback without a resolver", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("shellcheck").On("shellcheck", runner.Result{ExitCode: 1, Stdout: report(comment("a.sh", 1, 2086, "q"))})
		env := newEnv(t, f, nil)
		p := write(t, env.Repo, "a.sh", "echo\n")
		if checks := m.Check(ctx, env, []string{p}); len(checks) != 1 {
			t.Fatalf("checks = %+v", checks)
		}
	})
}

func names(checks []feedback.Check) string {
	var out []string
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

func TestGate(t *testing.T) {
	t.Parallel()
	m := New()
	ctx := context.Background()

	t.Run("passes", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake().On("shellcheck", runner.Result{Stdout: `{"comments":[]}`})
		env := newEnv(t, f, fakeTools{})
		p := write(t, env.Repo, "a.sh", "echo\n")
		res := m.Gate(ctx, env, module.GateInput{Files: []string{p}})
		if len(res.Checks) != 0 || len(res.Notes) != 0 || res.Total != -1 {
			t.Fatalf("res = %+v", res)
		}
		if strings.Join(res.Ran, ",") != "shellcheck,format" {
			t.Errorf("ran = %v", res.Ran)
		}
		if !f.Ran("shfmt -l " + p) {
			t.Errorf("calls = %v", f.Calls())
		}
	})

	t.Run("lint and format failures", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		a := write(t, env.Repo, "a.sh", "echo $1\n")
		b := write(t, env.Repo, "scripts/b", "#!/bin/sh\necho\n")
		f.On("shellcheck", runner.Result{ExitCode: 1, Stdout: report(comment(a, 1, 2086, "q"))})
		f.On("shfmt", runner.Result{ExitCode: 1, Stdout: a + "\n" + b + "\n"})
		res := m.Gate(ctx, env, module.GateInput{Files: []string{a, b}})
		if names(res.Checks) != "shellcheck,format" {
			t.Fatalf("checks = %+v", res.Checks)
		}
		fmtc := res.Checks[1]
		if fmtc.Summary != "2 scripts not shfmt-formatted" || strings.Join(fmtc.Lines, ",") != "a.sh,scripts/b" || !strings.Contains(fmtc.Fix, "shfmt -w") {
			t.Errorf("format check = %+v", fmtc)
		}
	})

	t.Run("one unformatted file", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake().On("shfmt", runner.Result{ExitCode: 1, Stdout: "a.sh\n"})
		env := newEnv(t, f, fakeTools{})
		a := write(t, env.Repo, "a.sh", "echo\n")
		res := m.Gate(ctx, env, module.GateInput{Files: []string{a}})
		if len(res.Checks) != 1 || res.Checks[0].Summary != "1 script not shfmt-formatted" {
			t.Fatalf("checks = %+v", res.Checks)
		}
	})

	t.Run("tools unavailable", func(t *testing.T) {
		t.Parallel()
		missing := fmt.Errorf("x: %w", tools.ErrMissing)
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{errs: map[string]error{"shellcheck": missing, "shfmt": missing}})
		a := write(t, env.Repo, "a.sh", "echo\n")
		res := m.Gate(ctx, env, module.GateInput{Files: []string{a}})
		if names(res.Checks) != "tools,tools" {
			t.Fatalf("checks = %+v", res.Checks)
		}
		for i, tool := range []string{"shellcheck", "shfmt"} {
			c := res.Checks[i]
			if !strings.Contains(c.Summary, tool+" is not installed") || c.Fix != "the human must install "+tool+" or set tools.install: auto" {
				t.Errorf("check %d = %+v", i, c)
			}
		}
	})

	t.Run("shellcheck start error", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake().OnError("shellcheck", errors.New("boom"))
		env := newEnv(t, f, fakeTools{})
		a := write(t, env.Repo, "a.sh", "echo\n")
		res := m.Gate(ctx, env, module.GateInput{Files: []string{a}})
		if len(res.Checks) != 1 || res.Checks[0].Summary != "could not run: boom" {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("shfmt start error and odd failure", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake().OnError("shfmt", errors.New("boom"))
		env := newEnv(t, f, fakeTools{})
		a := write(t, env.Repo, "a.sh", "echo\n")
		res := m.Gate(ctx, env, module.GateInput{Files: []string{a}})
		if len(res.Checks) != 0 || len(res.Notes) != 1 || res.Notes[0] != "shfmt could not run: boom" {
			t.Fatalf("res = %+v", res)
		}

		f = runner.NewFake().On("shfmt", runner.Result{ExitCode: 2, Stderr: "a.sh:1:1: parse error\nmore"})
		env = newEnv(t, f, fakeTools{})
		a = write(t, env.Repo, "a.sh", "echo\n")
		res = m.Gate(ctx, env, module.GateInput{Files: []string{a}})
		if len(res.Checks) != 0 || len(res.Notes) != 1 || res.Notes[0] != "shfmt failed: a.sh:1:1: parse error" {
			t.Fatalf("res = %+v", res)
		}
	})

	t.Run("nothing to gate", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		res := m.Gate(ctx, env, module.GateInput{Files: []string{filepath.Join(env.Repo, "gone.sh")}})
		if len(res.Ran) != 0 || len(f.Calls()) != 0 {
			t.Fatalf("res = %+v calls = %v", res, f.Calls())
		}
	})

	t.Run("all mode walks the repo", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env := newEnv(t, f, fakeTools{})
		a := write(t, env.Repo, "a.sh", "echo\n")
		b := write(t, env.Repo, "tools/run", "#!/usr/bin/env bash\n")
		write(t, env.Repo, "tools/notes.txt", "x\n")
		write(t, env.Repo, ".git/hooks/pre-commit.sh", "x\n")
		write(t, env.Repo, "vendor/v.sh", "x\n")
		write(t, env.Repo, "node_modules/n.sh", "x\n")
		res := m.Gate(ctx, env, module.GateInput{All: true})
		if len(res.Ran) != 2 {
			t.Fatalf("res = %+v", res)
		}
		got := f.Calls()[0].Args
		joined := strings.Join(got, " ")
		if !strings.Contains(joined, a) || !strings.Contains(joined, b) {
			t.Errorf("shellcheck args = %v", got)
		}
		for _, skipped := range []string{".git", "vendor", "node_modules", "notes.txt"} {
			if strings.Contains(joined, skipped) {
				t.Errorf("%s should be skipped: %v", skipped, got)
			}
		}
	})
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := rel("/repo", "/repo/a/b.sh"); got != "a/b.sh" {
		t.Errorf("rel inside = %q", got)
	}
	if got := rel("/repo", "/other/b.sh"); got != "/other/b.sh" {
		t.Errorf("rel outside = %q", got)
	}
	if got := rel("/repo", "x/y.sh"); got != "x/y.sh" {
		t.Errorf("rel relative = %q", got)
	}
	if got := firstLine("  one\ntwo"); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("solo"); got != "solo" {
		t.Errorf("firstLine = %q", got)
	}
	if err := skipUnreadable(nil); err != nil {
		t.Errorf("skipUnreadable(nil) = %v", err)
	}
	dir := t.TempDir()
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if e.IsDir() && !errors.Is(skipUnreadable(e), filepath.SkipDir) {
			t.Error("directories are skipped")
		}
	}
	if New().all(filepath.Join(dir, "missing")) != nil {
		t.Error("walking a missing dir finds nothing")
	}
}

// TestIntegration runs the real tools when they are installed.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	for _, tool := range []string{"shellcheck", "shfmt"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	m := New()
	ctx := context.Background()
	env := module.Env{Runner: runner.Exec{}, Config: config.Default(), Repo: t.TempDir(), Tools: tools.PathResolver{LookPath: exec.LookPath}}
	p := write(t, env.Repo, "deploy.sh", "#!/bin/sh\nif [ x = y ];then\necho  $1\nfi\n")

	checks := m.Check(ctx, env, []string{p})
	if len(checks) != 1 {
		t.Fatalf("checks = %+v", checks)
	}
	rules := map[string]bool{}
	for _, is := range checks[0].Issues {
		rules[is.Rule] = true
		if is.File != "deploy.sh" {
			t.Errorf("file = %q", is.File)
		}
	}
	if !rules["SC2086"] {
		t.Errorf("expected SC2086, got %+v", checks[0].Issues)
	}

	res := m.Gate(ctx, env, module.GateInput{Files: []string{p}})
	if names(res.Checks) != "shellcheck,format" {
		t.Fatalf("gate checks = %+v", res.Checks)
	}

	if fix := m.Fix(ctx, env, []string{p}); len(fix.Notes) != 0 {
		t.Fatalf("fix notes = %v", fix.Notes)
	}
	write(t, env.Repo, "deploy.sh", "#!/bin/sh\nset -eu\necho \"$1\"\n")
	if res := m.Gate(ctx, env, module.GateInput{Files: []string{p}}); len(res.Checks) != 0 {
		t.Fatalf("clean script should pass: %+v", res.Checks)
	}
}

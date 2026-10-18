package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/config"
	"github.com/rossedman/guardrails/internal/feedback"
	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

const clean = "FROM " + pinned + "\nUSER app\n"

func newEnv(t *testing.T, f *runner.Fake) (module.Env, string) {
	t.Helper()
	repo := t.TempDir()
	return module.Env{Runner: f, Config: config.Default(), Repo: repo}, repo
}

func write(t *testing.T, repo, rel, content string) string {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func find(checks []feedback.Check, name string) (feedback.Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return feedback.Check{}, false
}

func TestMatch(t *testing.T) {
	t.Parallel()
	m := New()
	for path, want := range map[string]bool{
		"Dockerfile":             true,
		"svc/Dockerfile.dev":     true,
		"api.Dockerfile":         true,
		"build/web.dockerfile":   true,
		"Containerfile":          true,
		"Containerfile.prod":     true,
		"dockerfile":             false,
		"Dockerfiles/readme.md":  false,
		"docker-compose.yml":     false,
		"src/Dockerfile_helpers": false,
	} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestMetadata(t *testing.T) {
	t.Parallel()
	m := New()
	if m.Name() != "docker" {
		t.Errorf("Name = %q", m.Name())
	}
	cfg := config.Default()
	if !m.Enabled(cfg) {
		t.Error("enabled by default")
	}
	off := false
	cfg.Docker.Enabled = &off
	if m.Enabled(cfg) {
		t.Error("disabled by config")
	}
	if tl := m.Tools(); len(tl) != 1 || tl[0].Name != "docker" || tl[0].Required {
		t.Errorf("Tools = %+v", tl)
	}
	s := m.Standards(config.Default())
	for _, want := range []string{"pin every base image by digest", "non-root USER", "COPY", "secrets", "docker build --check"} {
		if !strings.Contains(s, want) {
			t.Errorf("Standards missing %q: %s", want, s)
		}
	}
	cfg = config.Default()
	cfg.Docker.RequireDigestPins, cfg.Docker.RequireNonRoot, cfg.Docker.BuildCheck = false, false, false
	s = m.Standards(cfg)
	if !strings.Contains(s, "non-latest image tags") {
		t.Errorf("Standards = %s", s)
	}
	for _, absent := range []string{"digest", "USER", "--check"} {
		if strings.Contains(s, absent) {
			t.Errorf("Standards should not mention %q: %s", absent, s)
		}
	}
	if res := m.Fix(context.Background(), module.Env{}, []string{"Dockerfile"}); len(res.Notes) != 0 {
		t.Errorf("Fix = %+v", res)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reports native rule violations without running tools", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake()
		env, repo := newEnv(t, f)
		file := write(t, repo, "svc/Dockerfile", "FROM alpine:3.20\nADD . /app\n")
		checks := New().Check(ctx, env, []string{file})
		var names []string
		for _, c := range checks {
			names = append(names, c.Name)
		}
		if strings.Join(names, ",") != "digest-pins,non-root,add-vs-copy" {
			t.Errorf("checks = %v", names)
		}
		if is := checks[0].Issues[0]; is.File != "svc/Dockerfile" || is.Rule != "GR-DOCKER-001" {
			t.Errorf("issue = %+v", is)
		}
		if len(f.Calls()) != 0 {
			t.Errorf("Check ran tools: %v", f.Calls())
		}
	})

	t.Run("clean file", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		if checks := New().Check(ctx, env, []string{write(t, repo, "Dockerfile", clean)}); checks != nil {
			t.Errorf("checks = %+v", checks)
		}
	})

	t.Run("issues from several files merge by rule", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		a := write(t, repo, "a/Dockerfile", "FROM alpine\nUSER app\n")
		b := write(t, repo, "b/Dockerfile", "FROM debian\nUSER app\n")
		missing := filepath.Join(repo, "gone/Dockerfile")
		checks := New().Check(ctx, env, []string{a, missing, b})
		if len(checks) != 1 || len(checks[0].Issues) != 2 || checks[0].Summary != "2 issues" {
			t.Fatalf("checks = %+v", checks)
		}
		if checks[0].Issues[0].File != "a/Dockerfile" || checks[0].Issues[1].File != "b/Dockerfile" {
			t.Errorf("issues = %+v", checks[0].Issues)
		}
	})
}

func TestGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no existing files", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		if err := os.Mkdir(filepath.Join(repo, "Dockerfile"), 0o750); err != nil {
			t.Fatal(err)
		}
		res := New().Gate(ctx, env, module.GateInput{Files: []string{filepath.Join(repo, "Dockerfile"), filepath.Join(repo, "x/Dockerfile")}})
		if len(res.Checks) != 0 || len(res.Ran) != 0 || res.Total != -1 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("native rules and ran list", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		env.Config.Docker.BuildCheck = false
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", "FROM alpine:3\nUSER root\n")}})
		if _, ok := find(res.Checks, "non-root"); !ok {
			t.Errorf("checks = %+v", res.Checks)
		}
		if strings.Join(res.Ran, ",") != "dockerfile-rules" || len(res.Notes) != 0 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("all mode walks the repo", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		env.Config.Docker.BuildCheck = false
		write(t, repo, "Dockerfile", clean)
		write(t, repo, "svc/api.Dockerfile", "FROM alpine\nUSER app\n")
		write(t, repo, ".git/Dockerfile", "FROM alpine\n")
		write(t, repo, "node_modules/x/Dockerfile", "FROM alpine\n")
		write(t, repo, "vendor/Dockerfile", "FROM alpine\n")
		write(t, repo, "README.md", "")
		res := New().Gate(ctx, env, module.GateInput{All: true})
		c, ok := find(res.Checks, "digest-pins")
		if !ok || len(c.Issues) != 1 || c.Issues[0].File != "svc/api.Dockerfile" || len(res.Checks) != 1 {
			t.Errorf("checks = %+v", res.Checks)
		}
	})
}

const buildCheckOut = `Check complete, 2 warnings have been found!

WARNING: FromAsCasing - https://docs.docker.com/go/dockerfile/rule/from-as-casing/
'as' and 'FROM' keywords' casing do not match (line 1)

Dockerfile:1
--------------------
   1 | >>> FROM alpine as build
--------------------

WARNING: LegacyKeyValueFormat - https://docs.docker.com/go/dockerfile/rule/legacy-key-value-format/
Dockerfile:2
"ENV key=value" should be used instead of legacy "ENV key value" format
`

func TestBuildCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("docker CLI missing", func(t *testing.T) {
		t.Parallel()
		env, repo := newEnv(t, runner.NewFake())
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", clean)}})
		if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "docker build --check skipped: docker is not installed") {
			t.Errorf("notes = %v", res.Notes)
		}
		if len(res.Checks) != 0 {
			t.Errorf("checks = %+v", res.Checks)
		}
	})

	t.Run("no daemon", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("docker").On("docker info", runner.Result{ExitCode: 1, Stderr: "Cannot connect"})
		env, repo := newEnv(t, f)
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", clean)}})
		if len(res.Notes) != 1 || res.Notes[0] != "docker build --check skipped: no Docker daemon" {
			t.Errorf("notes = %v", res.Notes)
		}
		if f.Ran("docker build") {
			t.Error("build ran without a daemon")
		}
	})

	t.Run("warnings become issues", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("docker").On("docker info", runner.Result{Stdout: "27.0.0"}).
			On("docker build", runner.Result{Stderr: buildCheckOut, ExitCode: 1})
		env, repo := newEnv(t, f)
		file := write(t, repo, "app/Dockerfile", clean)
		res := New().Gate(ctx, env, module.GateInput{Files: []string{file}})
		c, ok := find(res.Checks, "build-check")
		if !ok || len(c.Issues) != 2 {
			t.Fatalf("checks = %+v", res.Checks)
		}
		if is := c.Issues[0]; is.Rule != "FromAsCasing" || is.Line != 1 || is.File != "app/Dockerfile" || strings.Contains(is.Message, "(line") {
			t.Errorf("issue 0 = %+v", is)
		}
		if is := c.Issues[1]; is.Rule != "LegacyKeyValueFormat" || is.Line != 2 || !strings.Contains(is.Message, "ENV key=value") {
			t.Errorf("issue 1 = %+v", is)
		}
		if !f.Ran("docker build --check -f " + file + " " + filepath.Dir(file)) {
			t.Errorf("calls = %v", f.Calls())
		}
		if strings.Join(res.Ran, ",") != "dockerfile-rules,build-check" {
			t.Errorf("Ran = %v", res.Ran)
		}
	})

	t.Run("clean build check", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("docker").On("docker info", runner.Result{Stdout: "27.0.0"}).
			On("docker build", runner.Result{Stderr: "Check complete, no warnings found."})
		env, repo := newEnv(t, f)
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", clean)}})
		if len(res.Checks) != 0 {
			t.Errorf("checks = %+v", res.Checks)
		}
	})

	t.Run("failure without parsable warnings", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("docker").On("docker info", runner.Result{Stdout: "27.0.0"}).
			On("docker build", runner.Result{Stderr: "ERROR: failed to solve: dockerfile parse error", ExitCode: 1})
		env, repo := newEnv(t, f)
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", clean)}})
		c, ok := find(res.Checks, "build-check")
		if !ok || len(c.Lines) != 1 || !strings.Contains(c.Lines[0], "parse error") || !strings.Contains(c.Summary, "1 issue") {
			t.Errorf("checks = %+v", res.Checks)
		}
	})

	t.Run("start error", func(t *testing.T) {
		t.Parallel()
		f := runner.NewFake("docker").On("docker info", runner.Result{Stdout: "27.0.0"}).OnError("docker build", errors.New("killed"))
		env, repo := newEnv(t, f)
		res := New().Gate(ctx, env, module.GateInput{Files: []string{write(t, repo, "Dockerfile", clean)}})
		if c, ok := find(res.Checks, "build-check"); !ok || !strings.Contains(c.Lines[0], "Dockerfile: killed") {
			t.Errorf("checks = %+v", res.Checks)
		}
	})
}

func TestParseBuildCheckEdges(t *testing.T) {
	t.Parallel()
	got := parseBuildCheck("Dockerfile", "WARNING: JSONArgsRecommended\nWARNING: StageNameCasing - url\nStage name 'Build' should be lowercase\n")
	if len(got) != 2 || got[0].Message != "JSONArgsRecommended" || got[1].Message != "Stage name 'Build' should be lowercase" {
		t.Errorf("got %+v", got)
	}
}

func TestRel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ repo, p, want string }{
		{"/r", "/r/a/Dockerfile", "a/Dockerfile"},
		{"/r", "a/Dockerfile", "a/Dockerfile"},
		{"/r", "/elsewhere/Dockerfile", "/elsewhere/Dockerfile"},
	} {
		if got := rel(tc.repo, tc.p); got != tc.want {
			t.Errorf("rel(%q, %q) = %q, want %q", tc.repo, tc.p, got, tc.want)
		}
	}
}

// TestBuildCheckIntegration runs the real `docker build --check` when a
// daemon is reachable.
func TestBuildCheckIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("no Docker daemon")
	}
	env, repo := newEnv(t, runner.NewFake())
	env.Runner = runner.Exec{}
	file := write(t, repo, "Dockerfile", "FROM "+pinned+" as Build\nENV A 1\nUSER app\n")
	res := New().Gate(context.Background(), env, module.GateInput{Files: []string{file}})
	c, ok := find(res.Checks, "build-check")
	if !ok || len(c.Issues) == 0 {
		t.Errorf("checks = %+v notes = %v", res.Checks, res.Notes)
	}
}

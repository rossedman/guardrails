package golang

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/module"
	"github.com/rossedman/guardrails/internal/runner"
)

// goList renders go list output in the format dependents asks for.
func goList(lines ...string) runner.Result {
	return runner.Result{Stdout: strings.Join(lines, "\n") + "\n"}
}

const depsList = modPath + "|fmt||\n" +
	modPath + "/a|fmt||\n" +
	modPath + "/b|" + modPath + "/a,fmt||\n" + // imports a
	modPath + "/c|" + modPath + "/b||\n" + // imports a through b
	modPath + "/d||" + modPath + "/a|\n" + // a test imports a
	modPath + "/e|||" + modPath + "/a\n" + // an external test imports a
	modPath + "/x|strings||\n" + // unrelated
	"broken line without separators\n" +
	"|||\n"

func TestDependents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pkgs []string
		want []string
	}{
		{"direct, transitive and test importers", []string{"./a"}, []string{"./b", "./c", "./d", "./e"}},
		{"touched packages are excluded", []string{"./a", "./b"}, []string{"./c", "./d", "./e"}},
		{"leaf package has none", []string{"./c"}, []string{}},
		{"module root package", []string{"."}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := fakeAll().On("go list", runner.Result{Stdout: depsList})
			got, err := dependents(context.Background(), newEnv(t, t.TempDir(), f), "/m", modPath, tc.pkgs)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Errorf("dependents(%v) = %v, want %v", tc.pkgs, got, tc.want)
			}
		})
	}

	t.Run("importers of the root package", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().On("go list", goList(modPath+"|fmt||", modPath+"/y|"+modPath+"||"))
		got, err := dependents(context.Background(), newEnv(t, t.TempDir(), f), "/m", modPath, []string{"."})
		if err != nil || !slices.Equal(got, []string{"./y"}) {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("go list fails", func(t *testing.T) {
		t.Parallel()
		f := fakeAll().On("go list", runner.Result{ExitCode: 1, Stderr: "go: no go.mod"})
		if _, err := dependents(context.Background(), newEnv(t, t.TempDir(), f), "/m", modPath, []string{"./a"}); err == nil || !strings.Contains(err.Error(), "no go.mod") {
			t.Errorf("err = %v", err)
		}
		f2 := fakeAll().OnError("go list", errors.New("boom"))
		if _, err := dependents(context.Background(), newEnv(t, t.TempDir(), f2), "/m", modPath, []string{"./a"}); err == nil {
			t.Error("want start error")
		}
	})
}

func TestImportPathAndRelPattern(t *testing.T) {
	t.Parallel()
	for pattern, want := range map[string]string{".": modPath, "./": modPath, "./a/b": modPath + "/a/b"} {
		if got := importPath(modPath, pattern); got != want {
			t.Errorf("importPath(%q) = %q, want %q", pattern, got, want)
		}
	}
	if relPattern(modPath, modPath) != "." || relPattern(modPath, modPath+"/a/b") != "./a/b" {
		t.Error("relPattern")
	}
}

func TestGateDependents(t *testing.T) {
	t.Parallel()
	files := map[string]string{"a/a.go": aSource}
	touched := func(repo string) module.GateInput {
		return module.GateInput{Files: []string{filepath.Join(repo, "a/a.go")}}
	}
	brokenB := "--- FAIL: TestQuad (0.00s)\n    b_test.go:7: Quad(3) = 13\nFAIL\nFAIL\t" + modPath + "/b\t0.01s\nok  \t" + modPath + "/c\t(cached)\nFAIL\n"

	t.Run("a broken importer fails the gate", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().
			OnFunc("go test -count=1", profileWriter(aProfileFull)).
			On("go list", runner.Result{Stdout: depsList}).
			On("go test -race ./b ./c ./d ./e", runner.Result{ExitCode: 1, Stdout: brokenB})
		res := New().Gate(context.Background(), newEnv(t, repo, f), touched(repo))
		c, ok := findCheck(res.Checks, "dependents")
		if !ok {
			t.Fatalf("want dependents check, got %v", checkNames(res.Checks))
		}
		if c.Summary != "1 failing test: TestQuad in packages that import your changes" {
			t.Errorf("summary = %q", c.Summary)
		}
		if len(c.Issues) != 1 || c.Issues[0].File != "b/b_test.go" || c.Issues[0].Line != 7 {
			t.Errorf("issues = %+v", c.Issues)
		}
		if !strings.Contains(c.Fix, "broke code that depends on it") {
			t.Errorf("fix = %q", c.Fix)
		}
		if !slices.Contains(res.Ran, "dependents") {
			t.Errorf("ran = %v", res.Ran)
		}
		// Dependents use Go's test cache: no -count=1.
		for _, call := range f.Calls() {
			if strings.HasPrefix(call.String(), "go test -race ./b") && slices.Contains(call.Args, "-count=1") {
				t.Errorf("dependents must use the test cache: %s", call)
			}
		}
	})
	t.Run("passing importers and no race", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().
			OnFunc("go test -count=1", profileWriter(aProfileFull)).
			On("go list", runner.Result{Stdout: depsList})
		env := newEnv(t, repo, f)
		env.Config.Go.Race = false
		res := New().Gate(context.Background(), env, touched(repo))
		if _, ok := findCheck(res.Checks, "dependents"); ok {
			t.Errorf("unexpected dependents check: %+v", res.Checks)
		}
		if !f.Ran("go test ./b ./c ./d ./e") {
			t.Errorf("calls = %v", f.Calls())
		}
	})
	t.Run("summary without test names", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().
			OnFunc("go test -count=1", profileWriter(aProfileFull)).
			On("go list", runner.Result{Stdout: depsList}).
			On("go test -race", runner.Result{ExitCode: 2, Stdout: "# example.com/demo/b\nsetup failed\n"})
		res := New().Gate(context.Background(), newEnv(t, repo, f), touched(repo))
		c, _ := findCheck(res.Checks, "dependents")
		if c.Summary != "tests failed in packages that import your changes" {
			t.Errorf("summary = %q", c.Summary)
		}
	})
	t.Run("dependent run cannot start", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().
			OnFunc("go test -count=1", profileWriter(aProfileFull)).
			On("go list", runner.Result{Stdout: depsList}).
			OnError("go test -race ./b", errors.New("killed"))
		res := New().Gate(context.Background(), newEnv(t, repo, f), touched(repo))
		if c, _ := findCheck(res.Checks, "dependents"); !strings.Contains(c.Summary, "killed") {
			t.Errorf("checks = %+v", res.Checks)
		}
	})
	t.Run("go list failure is a note", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().
			OnFunc("go test -count=1", profileWriter(aProfileFull)).
			On("go list", runner.Result{ExitCode: 1, Stderr: "go list exploded"})
		res := New().Gate(context.Background(), newEnv(t, repo, f), touched(repo))
		if _, ok := findCheck(res.Checks, "dependents"); ok {
			t.Error("go list failure must not block")
		}
		if !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, "dependent packages were not tested") }) {
			t.Errorf("notes = %v", res.Notes)
		}
	})
	t.Run("skipped when touched tests fail or in all mode", func(t *testing.T) {
		t.Parallel()
		repo := writeRepo(t, files)
		f := fakeAll().On("go test", runner.Result{ExitCode: 1, Stdout: "--- FAIL: TestX (0.00s)\nFAIL\n"})
		New().Gate(context.Background(), newEnv(t, repo, f), touched(repo))
		if f.Ran("go list") {
			t.Error("dependents must not run after touched tests fail")
		}
		f2 := fakeAll().OnFunc("go test -count=1", profileWriter(aProfileFull))
		New().Gate(context.Background(), newEnv(t, repo, f2), module.GateInput{All: true})
		if f2.Ran("go list") {
			t.Error("dependents are redundant in all mode")
		}
	})
}

func TestTestLocations(t *testing.T) {
	t.Parallel()
	root := "/repo"
	out := "--- FAIL: TestA (0.00s)\n" +
		"    a_test.go:3: boom\n" +
		"FAIL\n" +
		"FAIL\t" + modPath + "/pkg/a\t0.01s\n" +
		"--- FAIL: TestRoot (0.00s)\n" +
		"    root_test.go:9: bad\n" +
		"FAIL\t" + modPath + "\t0.01s\n" +
		"    nested/x_test.go:4: kept as is\n" +
		"FAIL\tother.com/elsewhere\t0.01s\n" +
		"    trailing_test.go:1: no package line\n"
	got := testLocations(root, modPath, out)
	want := []string{
		filepath.Join(root, "pkg/a", "a_test.go"),
		filepath.Join(root, "root_test.go"),
		"nested/x_test.go",
		"trailing_test.go",
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].File != w {
			t.Errorf("issue %d file = %q, want %q", i, got[i].File, w)
		}
	}
}

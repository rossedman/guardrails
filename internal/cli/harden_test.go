package cli

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/runner"
)

const hardenSrc = `package calc

import "strconv"

// Above reports whether a is greater than b.
func Above(a, b int) bool {
	return a > b
}

// ParseN parses a count.
func ParseN(s string) (int, error) {
	return strconv.Atoi(s)
}
`

// hardenRepo writes a module whose files are all untracked, so every line
// counts as changed. decide judges each mutant (nil for the baseline run).
func hardenRepo(t *testing.T, f *fixture, decide func(mutant string) runner.Result) {
	t.Helper()
	f.write("go.mod", "module example.com/m\n\ngo 1.22\n")
	f.write("calc/calc.go", hardenSrc)
	f.fake.On("git ls-files", runner.Result{Stdout: "calc/calc.go\n"})
	hardenGoTest(f, decide)
}

func hardenGoTest(f *fixture, decide func(mutant string) runner.Result) {
	f.fake.OnFunc("go test", func(c runner.Call) (runner.Result, error) {
		for _, a := range c.Args {
			if a == "-fuzz" {
				return runner.Result{}, nil
			}
			path, ok := strings.CutPrefix(a, "-overlay=")
			if !ok {
				continue
			}
			var ov struct{ Replace map[string]string }
			data, err := os.ReadFile(path)
			if err != nil {
				return runner.Result{}, err
			}
			if err := json.Unmarshal(data, &ov); err != nil {
				return runner.Result{}, err
			}
			for _, m := range ov.Replace {
				src, err := os.ReadFile(m)
				if err != nil {
					return runner.Result{}, err
				}
				return decide(string(src)), nil
			}
		}
		return decide(""), nil
	})
}

// killAllBut kills every mutant except those containing survivor.
func killAllBut(survivor string) func(string) runner.Result {
	return func(m string) runner.Result {
		if m == "" || (survivor != "" && strings.Contains(m, survivor)) {
			return runner.Result{}
		}
		return runner.Result{ExitCode: 1, Stdout: "--- FAIL: TestAbove"}
	}
}

func TestHardenFindsSurvivors(t *testing.T) {
	f := newFixture(t)
	hardenRepo(t, f, killAllBut("a >= b"))
	if code := f.run("", "harden"); code != 1 {
		t.Fatalf("harden = %d\n%s%s", code, f.stdout, f.stderr)
	}
	out := f.stdout.String()
	for _, want := range []string{
		"guardrails harden: found 1 problem (2 changed functions vs HEAD; ran mutation)",
		"[mutation] mutation score 50% < 70% (1 of 2 mutants killed)",
		"calc/calc.go:7  Above  `>` changed to `>=`, tests still pass",
		"suggestions (not failures):",
		"ParseN  could use a Benchmark (parses",
		"ParseN  could use a Fuzz target (parses string input and can fail)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !f.fake.Ran("git diff --unified=0 --no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/ HEAD -- *.go") {
		t.Errorf("calls = %v", f.fake.Calls())
	}
}

func TestHardenPasses(t *testing.T) {
	f := newFixture(t)
	hardenRepo(t, f, killAllBut(""))
	if code := f.run("", "harden", "--base", "main"); code != 0 {
		t.Fatalf("harden = %d\n%s", code, f.stdout)
	}
	if out := f.stdout.String(); !strings.Contains(out, "guardrails harden: passed (2 changed functions vs main; ran mutation)") {
		t.Errorf("out:\n%s", out)
	}
}

func TestHardenFlagsOverrideConfig(t *testing.T) {
	f := newFixture(t)
	hardenRepo(t, f, killAllBut("a >= b"))
	f.write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc FuzzParseN(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, s string) { _, _ = ParseN(s) })\n}\n")
	f.fake.On("git ls-files", runner.Result{Stdout: "calc/calc.go\ncalc/calc_test.go\n"})
	// 1 of 2 killed is 50%: passes with a 40% minimum.
	if code := f.run("", "harden", "--min-score", "40", "--fuzztime", "3s", "--budget", "30s"); code != 0 {
		t.Fatalf("harden = %d\n%s%s", code, f.stdout, f.stderr)
	}
	if !f.fake.Ran("go test -run ^$ -fuzz ^FuzzParseN$ -fuzztime 3s ./calc") {
		t.Errorf("calls = %v", f.fake.Calls())
	}
	if out := f.stdout.String(); !strings.Contains(out, "ran mutation, fuzz") || strings.Contains(out, "could use a Fuzz target") {
		t.Errorf("out:\n%s", out)
	}
}

func TestHardenSkipFlags(t *testing.T) {
	f := newFixture(t)
	hardenRepo(t, f, killAllBut("a >= b"))
	if code := f.run("", "harden", "--no-mutation", "--no-fuzz"); code != 0 {
		t.Fatalf("harden = %d", code)
	}
	if !strings.Contains(f.stdout.String(), "ran nothing to run") {
		t.Errorf("out:\n%s", f.stdout)
	}
	for _, c := range f.fake.Calls() {
		if c.Name == "go" {
			t.Errorf("ran %s", c)
		}
	}
}

func TestHardenNotesGoToStderr(t *testing.T) {
	f := newFixture(t)
	// The baseline itself fails, so mutation testing is skipped with a note.
	hardenRepo(t, f, func(string) runner.Result { return runner.Result{ExitCode: 1, Stdout: "FAIL"} })
	if code := f.run("", "harden"); code != 0 {
		t.Fatalf("harden = %d\n%s", code, f.stdout)
	}
	if !strings.Contains(f.stderr.String(), "note: ") || strings.Contains(f.stdout.String(), "note: ") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", f.stdout, f.stderr)
	}
}

func TestHardenJSON(t *testing.T) {
	f := newFixture(t)
	hardenRepo(t, f, killAllBut("a >= b"))
	if code := f.run("", "harden", "--json"); code != 1 {
		t.Fatalf("harden = %d", code)
	}
	var got struct {
		Passed    bool `json:"passed"`
		Functions int  `json:"functions"`
		Checks    []struct {
			Name string `json:"name"`
		} `json:"checks"`
		Suggestions []string `json:"suggestions"`
		Notes       []string `json:"notes"`
		Ran         []string `json:"ran"`
	}
	if err := json.Unmarshal(f.stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, f.stdout)
	}
	if got.Passed || got.Functions != 2 || len(got.Checks) != 1 || got.Checks[0].Name != "mutation" ||
		len(got.Suggestions) != 2 || len(got.Ran) != 1 {
		t.Errorf("json = %+v", got)
	}
}

func TestHardenMultipleModules(t *testing.T) {
	f := newFixture(t)
	f.write("a/go.mod", "module example.com/a\n\ngo 1.22\n")
	f.write("a/x/x.go", "package x\n\nfunc Above(a, b int) bool {\n\treturn a > b\n}\n")
	f.write("b/go.mod", "module example.com/b\n\ngo 1.22\n")
	f.write("b/y/y.go", "package y\n\nfunc Below(a, b int) bool {\n\treturn a < b\n}\n")
	// Outside any module: ignored.
	f.write("loose.go", "package loose\n\nfunc Loose(a, b int) bool {\n\treturn a == b\n}\n")
	f.fake.On("git ls-files", runner.Result{Stdout: "a/x/x.go\nb/y/y.go\nloose.go\n"})
	hardenGoTest(f, killAllBut(""))
	if code := f.run("", "harden"); code != 0 {
		t.Fatalf("harden = %d\n%s%s", code, f.stdout, f.stderr)
	}
	dirs := map[string]bool{}
	for _, c := range f.fake.Calls() {
		if c.Name == "go" {
			dirs[c.Dir] = true
		}
	}
	if !dirs[f.repo+"/a"] || !dirs[f.repo+"/b"] || len(dirs) != 2 {
		t.Errorf("go test dirs = %v", dirs)
	}
	if !strings.Contains(f.stdout.String(), "passed (3 changed functions") {
		t.Errorf("out:\n%s", f.stdout)
	}
}

func TestHardenErrors(t *testing.T) {
	t.Run("bad flag", func(t *testing.T) {
		f := newFixture(t)
		if code := f.run("", "harden", "--nope"); code != 2 {
			t.Errorf("code = %d", code)
		}
	})
	t.Run("no repo", func(t *testing.T) {
		f := newFixture(t)
		f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
		if code := f.run("", "harden"); code != 1 || !strings.Contains(f.stderr.String(), "gone") {
			t.Errorf("code = %d stderr = %s", code, f.stderr)
		}
	})
	t.Run("bad config", func(t *testing.T) {
		f := newFixture(t)
		f.write(".guardrails.yaml", "bad: key\n")
		if code := f.run("", "harden"); code != 1 || !strings.Contains(f.stderr.String(), "invalid config") {
			t.Errorf("code = %d stderr = %s", code, f.stderr)
		}
	})
	t.Run("git diff fails", func(t *testing.T) {
		f := newFixture(t)
		f.fake.On("git diff", runner.Result{ExitCode: 128, Stderr: "fatal: bad revision 'nope'"})
		if code := f.run("", "harden", "--base", "nope"); code != 1 || !strings.Contains(f.stderr.String(), "bad revision") {
			t.Errorf("code = %d stderr = %s", code, f.stderr)
		}
	})
	t.Run("unparseable change", func(t *testing.T) {
		f := newFixture(t)
		f.write("bad.go", "package bad\n\nfunc (\n")
		f.fake.On("git ls-files", runner.Result{Stdout: "bad.go\n"})
		if code := f.run("", "harden"); code != 1 || !strings.Contains(f.stderr.String(), "parse") {
			t.Errorf("code = %d stderr = %s", code, f.stderr)
		}
	})
	t.Run("module without path", func(t *testing.T) {
		f := newFixture(t)
		f.write("go.mod", "go 1.22\n")
		f.write("calc/calc.go", hardenSrc)
		f.fake.On("git ls-files", runner.Result{Stdout: "calc/calc.go\n"})
		if code := f.run("", "harden"); code != 1 || !strings.Contains(f.stderr.String(), "no module directive") {
			t.Errorf("code = %d stderr = %s", code, f.stderr)
		}
	})
	t.Run("no changes", func(t *testing.T) {
		f := newFixture(t)
		if code := f.run("", "harden"); code != 0 || !strings.Contains(f.stdout.String(), "no Go changes against HEAD; try --base main") {
			t.Errorf("code = %d stdout = %s", code, f.stdout)
		}
	})
}

func TestUniq(t *testing.T) {
	if got := strings.Join(uniq([]string{"a", "b", "a", "c", "b"}), ","); got != "a,b,c" {
		t.Errorf("uniq = %q", got)
	}
}

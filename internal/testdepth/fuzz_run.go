package testdepth

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rossedman/guardrails/internal/feedback"
)

// fuzzDefaultTime is used when go.fuzz.fuzztime does not parse.
const fuzzDefaultTime = 10 * time.Second

var (
	fuzzFailingInput = regexp.MustCompile(`Failing input written to (testdata/fuzz/\S+)`)
	fuzzLocated      = regexp.MustCompile(`^\s*\S+\.go:\d+: (.+)$`)
)

// fuzzRun runs each target sequentially within the fuzz budget.
func fuzzRun(ctx context.Context, e Env, targets []fuzzTarget) Result {
	var res Result
	cfg := e.Config.Go.Fuzz
	fuzztime, err := time.ParseDuration(cfg.Fuzztime)
	if err != nil || fuzztime <= 0 {
		fuzztime = fuzzDefaultTime
	}
	budget, cancel := Slice(ctx, time.Duration(cfg.BudgetSeconds)*time.Second)
	defer cancel()

	var (
		lines   []string
		crashes int
		skipped []string
		ran     bool
	)
	for i, t := range targets {
		per := fuzztime + fuzzRunSlack
		if dl, ok := budget.Deadline(); ok {
			left := time.Until(dl)
			if left <= 0 || budget.Err() != nil {
				for _, s := range targets[i:] {
					skipped = append(skipped, s.Name)
				}
				break
			}
			per = min(per, left)
		}
		ran = true
		runCtx, stop := context.WithTimeout(budget, per)
		pkg := fuzzPkg(e.Root, t.Dir)
		out, err := e.Runner.Run(runCtx, e.Root, "go", "test", "-run", "^$",
			"-fuzz", "^"+t.Name+"$", "-fuzztime", fuzztime.String(), pkg)
		stop()
		switch {
		case err != nil:
			res.Notes = append(res.Notes, fmt.Sprintf("fuzz: %s did not finish: %v", t.Name, err))
		case out.ExitCode != 0:
			text := out.Combined()
			if m := fuzzFailingInput.FindStringSubmatch(text); m != nil {
				crashes++
				saved := fuzzRel(e.Repo, filepath.Join(t.Dir, filepath.FromSlash(m[1])))
				lines = append(lines, fmt.Sprintf("%s  %s  →  %s (now a regression test)", t.Name, fuzzFailure(text), saved))
			} else {
				lines = append(lines, fmt.Sprintf("%s  failed before fuzzing:", t.Name))
				for _, l := range fuzzFirstLines(text, 8) {
					lines = append(lines, "  "+l)
				}
			}
		}
	}
	if ran {
		res.Ran = append(res.Ran, "fuzz")
	}
	if len(skipped) > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("fuzz: skipped %s (%ds budget used)", strings.Join(skipped, ", "), cfg.BudgetSeconds))
	}
	if len(lines) > 0 {
		summary := fmt.Sprintf("%d fuzz target found a failing input", crashes)
		if crashes != 1 {
			summary = fmt.Sprintf("%d fuzz targets found failing inputs", crashes)
		}
		if crashes == 0 {
			summary = "fuzz targets failed to run"
		}
		res.Checks = append(res.Checks, feedback.Check{
			Name: "fuzz", Failed: true, Summary: summary, Lines: lines,
			Fix: "fix the code so the saved input passes; keep the input file, it is now a regression test",
		})
	}
	return res
}

// fuzzFailure returns the first meaningful failure line: a panic, or the
// message of a located t.Error/t.Fatal line.
func fuzzFailure(out string) string {
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if i := strings.Index(t, "panic: "); i >= 0 {
			return t[i:]
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if m := fuzzLocated.FindStringSubmatch(l); m != nil && !strings.HasPrefix(strings.TrimSpace(l), "Failing input") {
			return strings.TrimSpace(m[1])
		}
	}
	return "failing input"
}

// fuzzFirstLines returns up to n non-blank lines of s, with trailing whitespace trimmed.
func fuzzFirstLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, strings.TrimRight(l, " \t\r"))
		if len(out) == n {
			break
		}
	}
	return out
}

// fuzzPkg is the go test package argument for dir, relative to root.
func fuzzPkg(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	if rel == "." {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

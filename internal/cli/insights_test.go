package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/insights"
	"github.com/rossedman/guardrails/internal/runlog"
)

// writeRuns writes records into the fixture's runs.jsonl, plus one
// malformed line.
func writeRuns(t *testing.T, f *fixture, recs ...runlog.Record) {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString("garbage\n")
	if err := os.WriteFile(filepath.Join(f.data, runlog.FileName), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func insightRuns(repo string) []runlog.Record {
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) // two days before the fixture clock
	var recs []runlog.Record
	for i := range 5 {
		s := "s" + string(rune('0'+i))
		recs = append(recs,
			runlog.Record{
				Type: "gate", At: at, Session: s, Repo: repo, Millis: 1500, Modules: []string{"go"},
				Failures: []runlog.Failure{{Check: "lint", Rule: "errcheck", File: "api/h.go"}, {Check: "timeout"}},
			},
			runlog.Record{Type: "gate", At: at.Add(time.Minute), Session: s, Repo: repo, Passed: true, Millis: 800},
		)
	}
	// Another repo, only visible with --all-repos.
	recs = append(recs, runlog.Record{Type: "gate", At: at, Session: "x", Repo: "/elsewhere", Passed: true, Millis: 10})
	return recs
}

func TestInsightsText(t *testing.T) {
	f := newFixture(t)
	writeRuns(t, f, insightRuns(f.repo)...)
	if code := f.run("", "insights"); code != 0 {
		t.Fatalf("exit %d: %s", code, f.stderr)
	}
	out := f.stdout.String()
	for _, want := range []string{
		"last 30 days, this repo",
		"first-pass rate   0% (0 of 5 turns",
		"sessions          5 (10 gate runs)",
		"gate time         p50 800ms, p95 1.5s",
		"timeouts          5 gates exceeded",
		"skipped           1 malformed log lines",
		"lint/errcheck",
		"api/h.go",
		"Rule candidates",
		"5 sessions → .claude/rules/go.md",
		"/guardrails:learn",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInsightsFlags(t *testing.T) {
	f := newFixture(t)
	writeRuns(t, f, insightRuns(f.repo)...)

	// Higher threshold: no candidates.
	if code := f.run("", "insights", "--min-sessions", "6"); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(f.stdout.String(), "Rule candidates") {
		t.Errorf("unexpected candidates:\n%s", f.stdout)
	}

	// All repos includes the other repo's session.
	if code := f.run("", "insights", "--all-repos", "--json"); code != 0 {
		t.Fatal(code)
	}
	var rep insights.Report
	if err := json.Unmarshal(f.stdout.Bytes(), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, f.stdout)
	}
	if rep.Sessions != 6 || rep.Repo != "" || rep.Malformed != 1 || len(rep.RuleCandidates) != 1 {
		t.Errorf("report = %+v", rep)
	}
	if rep.RuleCandidates[0].RulesFile != "go.md" {
		t.Errorf("candidate = %+v", rep.RuleCandidates[0])
	}

	// A one-day window excludes everything.
	if code := f.run("", "insights", "--days", "0"); code != 0 {
		t.Fatal(code)
	}
	if code := f.run("", "insights", "--days", "1"); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(f.stdout.String(), "no gate runs in the last 1 days (this repo)") {
		t.Errorf("window:\n%s", f.stdout)
	}
}

func TestInsightsNoDataAndCandidateWithoutLanguage(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "insights", "--all-repos"); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(f.stdout.String(), "no gate runs in the last 30 days (all repos)") {
		t.Errorf("no data:\n%s", f.stdout)
	}

	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	var recs []runlog.Record
	for i := range 5 {
		recs = append(recs, runlog.Record{
			Type: "gate", At: at, Session: string(rune('a' + i)), Repo: f.repo, Millis: 5, HandedOff: true,
			Modules: []string{"go", "shell"}, Failures: []runlog.Failure{{Check: "format"}},
		})
	}
	writeRuns(t, f, recs...)
	if code := f.run("", "insights"); code != 0 {
		t.Fatal(code)
	}
	out := f.stdout.String()
	for _, want := range []string{"a rules file (language unknown)", "hand-offs         5", "gate time         p50 5ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "timeouts") {
		t.Errorf("no timeouts expected:\n%s", out)
	}
}

func TestInsightsErrors(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "insights", "--nope"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	if err := os.Mkdir(filepath.Join(f.data, runlog.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "insights", "--all-repos"); code != 1 || !strings.Contains(f.stderr.String(), "guardrails:") {
		t.Errorf("load error: %d %s", code, f.stderr)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "insights"); code != 1 || !strings.Contains(f.stderr.String(), "gone") {
		t.Errorf("repo error: %d %s", code, f.stderr)
	}
}

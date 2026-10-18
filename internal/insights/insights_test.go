package insights

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/runlog"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func gate(session string, ago time.Duration, passed, handedOff bool, ms int64, fails ...runlog.Failure) runlog.Record {
	return runlog.Record{
		Type: "gate", At: now.Add(-ago), Session: session, Repo: "/r",
		Passed: passed, HandedOff: handedOff, Failures: fails, Millis: ms, Modules: []string{"go"},
	}
}

func f(check, rule, file string) runlog.Failure {
	return runlog.Failure{Check: check, Rule: rule, File: file}
}

func TestParse(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		`{"type":"gate","session":"a","repo":"/r"}`,
		``,
		`not json`,
		`{"session":"no type"}`,
		`{"type":"session","session":"a"}`,
	}, "\n")
	recs, malformed, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || malformed != 2 {
		t.Fatalf("recs=%d malformed=%d", len(recs), malformed)
	}
}

func TestParseScanError(t *testing.T) {
	t.Parallel()
	// A line longer than the scanner's limit surfaces as an error.
	long := strings.Repeat("x", 17*1024*1024)
	if _, _, err := Parse(strings.NewReader(long)); err == nil {
		t.Fatal("want scan error")
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recs, malformed, err := Load(dir)
	if err != nil || recs != nil || malformed != 0 {
		t.Fatalf("missing log: %v %v %v", recs, malformed, err)
	}
	line, _ := json.Marshal(gate("a", time.Hour, true, false, 10))
	if err := os.WriteFile(filepath.Join(dir, runlog.FileName), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, _, err = Load(dir)
	if err != nil || len(recs) != 1 {
		t.Fatalf("load: %v %v", recs, err)
	}
	// A directory where the log should be is an error, not empty.
	bad := t.TempDir()
	if err := os.Mkdir(filepath.Join(bad, runlog.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(bad); err == nil {
		t.Fatal("want error")
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected not-exist: %v", err)
	}
}

func TestSummarizeTurns(t *testing.T) {
	t.Parallel()
	recs := []runlog.Record{
		// Session a: blocked twice, then passed → one turn, not first pass.
		gate("a", 50*time.Minute, false, false, 100, f("lint", "errcheck", "x.go")),
		gate("a", 40*time.Minute, false, false, 200, f("lint", "errcheck", "x.go")),
		gate("a", 30*time.Minute, true, false, 300),
		// Then a second turn that passes first time.
		gate("a", 20*time.Minute, true, false, 400),
		// Session b: hand-off after a timeout.
		gate("b", 10*time.Minute, false, false, 900, f("timeout", "", "")),
		gate("b", 5*time.Minute, false, true, 1000, f("timeout", "", "")),
		// Subagent turn in session a, interleaved, passes first time.
		{Type: "gate", At: now.Add(-45 * time.Minute), Session: "a", AgentType: "test-writer", Passed: true, Repo: "/r", Millis: 50},
		// Session records count toward sessions only.
		{Type: "session", At: now.Add(-time.Minute), Session: "c", Repo: "/r"},
		// Outside the window and another repo: ignored.
		gate("old", 40*24*time.Hour, true, false, 1),
		{Type: "gate", At: now, Session: "z", Repo: "/other", Passed: true},
	}
	rep := Summarize(recs, Options{Repo: "/r", Now: now})
	want := Report{
		Sessions: 3, GateRuns: 7, Turns: 4, FirstPass: 2, HandOffs: 1, Timeouts: 2,
	}
	got := Report{Sessions: rep.Sessions, GateRuns: rep.GateRuns, Turns: rep.Turns, FirstPass: rep.FirstPass, HandOffs: rep.HandOffs, Timeouts: rep.Timeouts}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if rep.FirstPassRate != 0.5 {
		t.Errorf("first-pass rate = %v", rep.FirstPassRate)
	}
	// Three blocking gates (two in a, one in b before the hand-off) over four turns.
	if rep.BlocksPerTurn != 0.75 {
		t.Errorf("blocks per turn = %v", rep.BlocksPerTurn)
	}
	if rep.GateMillisP50 != 300 || rep.GateMillisP95 != 1000 {
		t.Errorf("p50=%d p95=%d", rep.GateMillisP50, rep.GateMillisP95)
	}
	if !rep.Since.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("since = %v", rep.Since)
	}
	// Timeouts are not rules.
	for _, r := range rep.TopRules {
		if r.Check == "timeout" {
			t.Errorf("timeout listed as a rule: %+v", r)
		}
	}
}

func TestSummarizeEmpty(t *testing.T) {
	t.Parallel()
	rep := Summarize(nil, Options{Now: now, Days: 7, MinSessions: 2})
	if rep.Turns != 0 || rep.FirstPassRate != 0 || rep.GateMillisP50 != 0 || rep.TopRules == nil || rep.RuleCandidates == nil {
		t.Fatalf("empty report = %+v", rep)
	}
	if !rep.Since.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Errorf("since = %v", rep.Since)
	}
}

func TestRulesAndCandidates(t *testing.T) {
	t.Parallel()
	var recs []runlog.Record
	for i := range 6 {
		s := string(rune('a' + i))
		recs = append(recs, gate(s, time.Duration(i+1)*time.Hour, false, false, 10,
			f("lint", "errcheck", "api/h.go"),
			f("lint", "errcheck", "api/h.go"),
			f("lint", "errcheck", "store/db.go"),
		))
		if i < 2 {
			recs = append(recs, gate(s, time.Duration(i+1)*time.Hour-time.Minute, false, false, 10, f("lint", "SC2086", "deploy.sh")))
		}
		if i < 5 {
			// No file: attributed through the only module that ran.
			recs = append(recs, gate(s, time.Duration(i+1)*time.Hour-2*time.Minute, false, false, 10, f("coverage", "", "")))
		}
	}
	// A failure in an unknown language and with mixed modules → no rules file.
	mixed := gate("q", time.Minute, false, false, 10, f("format", "", ""))
	mixed.Modules = []string{"go", "shell"}
	recs = append(recs, mixed)

	rep := Summarize(recs, Options{Now: now, MinSessions: 5})
	names := map[string]Rule{}
	for _, r := range rep.TopRules {
		names[r.Name()] = r
	}
	errcheck := names["lint/errcheck"]
	if errcheck.Count != 18 || errcheck.Sessions != 6 || errcheck.RulesFile != "go.md" {
		t.Errorf("errcheck = %+v", errcheck)
	}
	if !reflect.DeepEqual(errcheck.Examples, []string{"api/h.go", "store/db.go"}) {
		t.Errorf("examples = %v", errcheck.Examples)
	}
	if r := names["lint/SC2086"]; r.RulesFile != "shell.md" || r.Sessions != 2 {
		t.Errorf("SC2086 = %+v", r)
	}
	if r := names["coverage"]; r.RulesFile != "go.md" || r.Name() != "coverage" {
		t.Errorf("coverage = %+v", r)
	}
	if r := names["format"]; r.RulesFile != "" {
		t.Errorf("format = %+v", r)
	}
	// Ordered by sessions, then count.
	if rep.TopRules[0].Name() != "lint/errcheck" || rep.TopRules[1].Name() != "coverage" {
		t.Errorf("order = %v", rep.TopRules)
	}
	var cands []string
	for _, r := range rep.RuleCandidates {
		cands = append(cands, r.Name())
	}
	if !reflect.DeepEqual(cands, []string{"lint/errcheck", "coverage"}) {
		t.Errorf("candidates = %v", cands)
	}
}

func TestTopRulesCappedAtTen(t *testing.T) {
	t.Parallel()
	var recs []runlog.Record
	for i := range 15 {
		recs = append(recs, gate("s", time.Minute, false, false, 1, f("lint", string(rune('a'+i)), "x.go")))
	}
	rep := Summarize(recs, Options{Now: now})
	if len(rep.TopRules) != 10 {
		t.Fatalf("top rules = %d", len(rep.TopRules))
	}
	// Ties break by name.
	if rep.TopRules[0].Name() != "lint/a" {
		t.Errorf("first = %s", rep.TopRules[0].Name())
	}
}

func TestLanguage(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"a/b.go": "go", "x.py": "python", "run.sh": "shell", "x.bash": "shell", "x.ksh": "shell",
		"ci.yml": "yaml", "k.YAML": "yaml", "main.tf": "terraform", "prod.tfvars": "terraform",
		"Dockerfile": "docker", "svc/Dockerfile.dev": "docker", "Containerfile": "docker",
		"api.Dockerfile": "docker", "README.md": "", "Makefile": "",
	} {
		if got := Language(path); got != want {
			t.Errorf("Language(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestPercentilesSingle(t *testing.T) {
	t.Parallel()
	p50, p95 := percentiles([]runlog.Record{{Millis: 42}})
	if p50 != 42 || p95 != 42 {
		t.Errorf("p50=%d p95=%d", p50, p95)
	}
}

package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/guardrails/internal/lessons"
	"github.com/rossedman/guardrails/internal/runlog"
)

func TestLearn(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "learn"); code != 0 || !strings.Contains(f.stdout.String(), "no gate runs") || !strings.Contains(f.stdout.String(), "Your corrections: none recorded") ||
		strings.Contains(f.stdout.String(), "/guardrails:learn") {
		t.Fatalf("empty: %d\n%s", code, f.stdout)
	}

	writeRuns(t, f, insightRuns(f.repo)...)
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, p := range []string{"no, don't mock the database", "from now on, run make lint"} {
		if err := lessons.Record(f.data, lessons.Lesson{Time: at, Session: "s", Repo: f.repo, Prompt: p}); err != nil {
			t.Fatal(err)
		}
	}
	if code := f.run("", "learn", "--min-sessions", "3"); code != 0 {
		t.Fatalf("learn: %d %s", code, f.stderr)
	}
	out := f.stdout.String()
	for _, want := range []string{"first-pass rate", "lint/errcheck", "2 recorded, this repo", "don't mock the database", "Run /guardrails:learn in Claude Code"} {
		if !strings.Contains(out, want) {
			t.Errorf("learn lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "/guardrails:learn") != 1 {
		t.Errorf("the skill hint should appear once:\n%s", out)
	}

	if code := f.run("", "learn", "--json"); code != 0 {
		t.Fatalf("--json: %d", code)
	}
	var got struct {
		Insights struct {
			GateRuns int `json:"gate_runs"`
		} `json:"insights"`
		Lessons []lessons.Lesson `json:"lessons"`
	}
	if err := json.Unmarshal(f.stdout.Bytes(), &got); err != nil || got.Insights.GateRuns != 10 || len(got.Lessons) != 2 {
		t.Errorf("--json: %+v %v\n%s", got, err, f.stdout)
	}

	if code := f.run("", "learn", "--id", got.Lessons[0].ID); code != 2 {
		t.Errorf("--id without --clear: %d", code)
	}
	if code := f.run("", "learn", "--clear", "--id", got.Lessons[0].ID); code != 0 || !strings.Contains(f.stdout.String(), "cleared 1 lesson") {
		t.Errorf("--clear --id: %d %q", code, f.stdout)
	}
	if code := f.run("", "learn", "--clear", "--all-repos"); code != 0 || !strings.Contains(f.stdout.String(), "cleared 1 lesson") {
		t.Errorf("--clear --all-repos: %d %q", code, f.stdout)
	}
}

func TestLearnErrors(t *testing.T) {
	f := newFixture(t)
	// A directory where a log file belongs can't be read.
	if err := os.Mkdir(filepath.Join(f.data, lessons.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "learn"); code != 1 {
		t.Errorf("unreadable lessons: %d %q", code, f.stderr)
	}
	if code := f.run("", "learn", "--clear"); code != 1 {
		t.Errorf("clear unreadable lessons: %d %q", code, f.stderr)
	}
	f = newFixture(t)
	if err := os.Mkdir(filepath.Join(f.data, runlog.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "learn"); code != 1 {
		t.Errorf("unreadable history: %d %q", code, f.stderr)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "learn"); code != 1 {
		t.Errorf("getwd: %d", code)
	}
	if code := f.run("", "learn", "--clear"); code != 1 {
		t.Errorf("clear getwd: %d", code)
	}
}

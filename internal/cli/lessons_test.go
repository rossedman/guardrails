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
)

func TestLessonsCommand(t *testing.T) {
	f := newFixture(t)
	if code := f.run("", "lessons"); code != 0 || !strings.Contains(f.stdout.String(), "no corrections recorded for this repo") {
		t.Fatalf("empty: %d %q", code, f.stdout)
	}
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i, l := range []lessons.Lesson{
		{Repo: f.repo, Session: "s1", Prompt: "Don't mock the database.\nUse the test container.", Time: at},
		{Repo: f.repo, Session: "s2", Prompt: "From now on, run make lint. " + strings.Repeat("x", 100), Time: at.Add(48 * time.Hour), Explicit: true},
		{Repo: "/elsewhere", Session: "s3", Prompt: "Never push to main.", Time: at.Add(time.Hour), Explicit: true},
	} {
		if err := lessons.Record(f.data, l); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	all, _, _ := lessons.Load(f.data)

	if code := f.run("", "lessons"); code != 0 {
		t.Fatalf("list: %d %q", code, f.stderr)
	}
	out := f.stdout.String()
	newest, oldest := strings.Index(out, "From now on"), strings.Index(out, "Don't mock")
	if !strings.Contains(out, "2 recorded, this repo") || newest < 0 || oldest < newest || strings.Contains(out, "Never push") ||
		strings.Contains(out, "test container") || !strings.Contains(out, "…") || !strings.Contains(out, all[1].ID+" * From now on") {
		t.Errorf("list:\n%s", out)
	}

	if code := f.run("", "lessons", "--all-repos"); code != 0 || !strings.Contains(f.stdout.String(), "3 recorded, all repos") || !strings.Contains(f.stdout.String(), "/elsewhere") {
		t.Errorf("all repos: %d\n%s", code, f.stdout)
	}

	if code := f.run("", "lessons", "--json"); code != 0 {
		t.Fatalf("json: %d", code)
	}
	var got []lessons.Lesson
	if err := json.Unmarshal(f.stdout.Bytes(), &got); err != nil || len(got) != 2 || !got[0].Explicit {
		t.Errorf("json = %v, %v", got, err)
	}

	if code := f.run("", "lessons", "--id", "x"); code != 2 || !strings.Contains(f.stderr.String(), "--id only applies with --clear") {
		t.Errorf("--id alone: %d %q", code, f.stderr)
	}
	if code := f.run("", "lessons", "--clear", "--id", all[0].ID); code != 0 || !strings.Contains(f.stdout.String(), "cleared 1 lesson(s)") {
		t.Errorf("clear id: %d %q", code, f.stdout)
	}
	if code := f.run("", "lessons", "--clear"); code != 0 || !strings.Contains(f.stdout.String(), "cleared 1 lesson(s)") {
		t.Errorf("clear repo: %d %q", code, f.stdout)
	}
	if code := f.run("", "lessons", "--clear", "--all-repos"); code != 0 || !strings.Contains(f.stdout.String(), "cleared 1 lesson(s)") {
		t.Errorf("clear all: %d %q", code, f.stdout)
	}
	if code := f.run("", "lessons", "--json", "--all-repos"); code != 0 || strings.TrimSpace(f.stdout.String()) != "[]" {
		t.Errorf("empty json: %q", f.stdout)
	}
}

func TestLessonsErrors(t *testing.T) {
	f := newFixture(t)
	if err := os.Mkdir(filepath.Join(f.data, lessons.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if code := f.run("", "lessons"); code != 1 {
		t.Errorf("unreadable log: %d", code)
	}
	if code := f.run("", "lessons", "--clear"); code != 1 {
		t.Errorf("unclearable log: %d", code)
	}
	f.app.Getwd = func() (string, error) { return "", errors.New("gone") }
	if code := f.run("", "lessons"); code != 1 || !strings.Contains(f.stderr.String(), "gone") {
		t.Errorf("getwd: %d %q", code, f.stderr)
	}
}

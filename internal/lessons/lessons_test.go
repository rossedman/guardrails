package lessons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordLoadPendingClear(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "data")
	if ls, bad, err := Load(dir); err != nil || ls != nil || bad != 0 {
		t.Fatalf("Load(missing) = %v %d %v", ls, bad, err)
	}
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	must := func(l Lesson) {
		t.Helper()
		if err := Record(dir, l); err != nil {
			t.Fatal(err)
		}
	}
	must(Lesson{Session: "s1", Repo: "/a", Prompt: "  Don't mock the DB.  ", Time: at, Signals: []string{"don't"}})
	must(Lesson{Session: "s1", Repo: "/a", Prompt: "don't   mock the db.", Time: at}) // same ID and session: skipped
	must(Lesson{Session: "s2", Repo: "/a", Prompt: "Don't mock the DB.", Time: at})   // another session: kept
	must(Lesson{Session: "s1", Repo: "/b", Prompt: "Never commit to main.", Explicit: true})
	must(Lesson{Session: "s1", Repo: "/a", Prompt: strings.Repeat("é", MaxPromptRunes+10), Time: at})

	ls, bad, err := Load(dir)
	if err != nil || bad != 0 || len(ls) != 4 {
		t.Fatalf("Load = %d lessons, %d malformed, %v", len(ls), bad, err)
	}
	if ls[0].ID != ID("/a", "Don't mock the DB.") || ls[0].Prompt != "Don't mock the DB." || !ls[0].Time.Equal(at) {
		t.Errorf("first lesson = %+v", ls[0])
	}
	if ls[0].ID != ls[1].ID || ls[0].ID == ID("/b", "Don't mock the DB.") || len(ls[0].ID) != 10 {
		t.Errorf("IDs: %q %q", ls[0].ID, ls[1].ID)
	}
	if ls[2].Time.IsZero() {
		t.Error("Record fills a zero time")
	}
	if n := len([]rune(ls[3].Prompt)); n != MaxPromptRunes {
		t.Errorf("prompt runes = %d", n)
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v, %v", info.Mode(), err)
	}

	// A malformed line is counted and survives a clear.
	f, _ := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("not json\n{\"repo\":\"/a\"}\n\n")
	_ = f.Close()
	if _, bad, _ := Load(dir); bad != 2 {
		t.Errorf("malformed = %d", bad)
	}

	if n, _ := Pending(dir, "/a"); n != 3 {
		t.Errorf("Pending(/a) = %d", n)
	}
	if n, err := Clear(dir, "/a", []string{ls[3].ID, "nope"}); n != 1 || err != nil {
		t.Errorf("Clear(id) = %d, %v", n, err)
	}
	if n, err := Clear(dir, "/a", []string{"nope"}); n != 0 || err != nil {
		t.Errorf("Clear(unknown) = %d, %v", n, err)
	}
	if n, _ := Clear(dir, "/a", nil); n != 2 {
		t.Errorf("Clear(repo) = %d", n)
	}
	if n, _ := Pending(dir, "/b"); n != 1 {
		t.Errorf("other repos are kept: %d", n)
	}
	if n, _ := Clear(dir, "", nil); n != 1 {
		t.Errorf("Clear(all) = %d", n)
	}
	if ls, bad, _ := Load(dir); len(ls) != 0 || bad != 2 {
		t.Errorf("after clear: %d lessons, %d malformed", len(ls), bad)
	}
	if n, err := Clear(t.TempDir(), "", nil); n != 0 || err != nil {
		t.Errorf("Clear(empty dir) = %d, %v", n, err)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	// The data dir is a file.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Record(file, Lesson{Prompt: "x"}); err == nil {
		t.Error("Record into a file should fail")
	}
	if _, err := Pending(file, ""); err == nil {
		t.Error("Pending of a file dir should fail")
	}
	// The log is a directory.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, Lesson{Prompt: "x"}); err == nil {
		t.Error("Record onto a directory should fail")
	}
	if _, err := Clear(dir, "", nil); err == nil {
		t.Error("Clear of a directory log should fail")
	}
}

// TestClearWriteFailures swaps package seams, so it doesn't run in parallel.
func TestClearWriteFailures(t *testing.T) {
	t.Cleanup(func() { createTemp, rename = os.CreateTemp, os.Rename })
	record := func() string {
		dir := t.TempDir()
		if err := Record(dir, Lesson{Repo: "/a", Prompt: "Never do X."}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	check := func(name string) {
		t.Helper()
		dir := record()
		if _, err := Clear(dir, "", nil); err == nil {
			t.Errorf("%s: Clear should fail", name)
		}
		if ls, _, _ := Load(dir); len(ls) != 1 {
			t.Errorf("%s: the log must be untouched, has %d lessons", name, len(ls))
		}
		if m, _ := filepath.Glob(filepath.Join(dir, FileName+".*")); len(m) != 0 {
			t.Errorf("%s: temp files left: %v", name, m)
		}
	}

	createTemp = func(string, string) (*os.File, error) { return nil, os.ErrPermission }
	check("create")
	createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
		return os.Open(f.Name()) // read-only: the write fails
	}
	check("write")
	createTemp = os.CreateTemp
	rename = func(string, string) error { return os.ErrPermission }
	check("rename")
}

func TestNudge(t *testing.T) {
	t.Parallel()
	plain := Nudge(Lesson{ID: "abc"})
	if !strings.Contains(plain, "candidate rule (lesson abc)") || !strings.Contains(plain, "/guardrails:learn") {
		t.Errorf("Nudge = %q", plain)
	}
	if got := Nudge(Lesson{ID: "abc", Explicit: true}); !strings.Contains(got, "standing rule") {
		t.Errorf("explicit Nudge = %q", got)
	}
	if got := Nudge(Lesson{Repo: "/a", Prompt: " Don't mock the DB. "}); !strings.Contains(got, "lesson "+ID("/a", "Don't mock the DB.")+")") {
		t.Errorf("Nudge without ID = %q", got)
	}
}

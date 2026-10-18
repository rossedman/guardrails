package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionTouch(t *testing.T) {
	t.Parallel()
	s := &Session{}
	s.Touch("", "/r/b.go")
	s.Touch("agent1", "/r/a.go")
	s.Touch("agent1", "/r/c.go")
	s.Touch("agent2", "/r/c.go")

	tests := []struct {
		agent string
		want  []string
	}{
		{"", []string{"/r/a.go", "/r/b.go", "/r/c.go"}},
		{"agent1", []string{"/r/a.go", "/r/c.go"}},
		{"agent2", []string{"/r/c.go"}},
		{"unknown", []string{}},
	}
	for _, tc := range tests {
		if got := s.TouchedFiles(tc.agent); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("TouchedFiles(%q) = %v, want %v", tc.agent, got, tc.want)
		}
	}
}

func TestSessionBlocks(t *testing.T) {
	t.Parallel()
	s := &Session{}
	if s.Blocks("") != 0 || s.Blocks("a") != 0 {
		t.Fatal("fresh counters should be zero")
	}
	s.SetBlocks("", 2)
	s.SetBlocks("a", 5)
	if s.Blocks("") != 2 || s.StopBlocks != 2 {
		t.Errorf("main blocks = %d", s.Blocks(""))
	}
	if s.Blocks("a") != 5 || s.Blocks("b") != 0 {
		t.Errorf("agent blocks = %d/%d", s.Blocks("a"), s.Blocks("b"))
	}
}

func TestLoadMissing(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	sess, err := st.Load("nope")
	if err != nil || sess.ID != "nope" || len(sess.Touched) != 0 {
		t.Fatalf("Load missing = %+v, %v", sess, err)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err := st.Update("s1", func(s *Session) error {
		s.Repo = "/repo"
		s.Touch("", "/repo/a.go")
		s.Baseline = map[string]float64{"pkg": 91.5}
		s.Started = started
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "/repo" || !got.Touched["/repo/a.go"] || got.Baseline["pkg"] != 91.5 || !got.Started.Equal(started) {
		t.Errorf("round trip lost data: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(st.SessionDir("s1"), "state.lock")); !os.IsNotExist(err) {
		t.Error("lock file not released")
	}
}

func TestUpdateFnErrorWritesNothing(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	boom := errors.New("boom")
	_, err := st.Update("s1", func(s *Session) error {
		s.Repo = "changed"
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got, _ := st.Load("s1")
	if got.Repo != "" {
		t.Error("state written despite error")
	}
}

func TestUpdateConcurrent(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.Update("s", func(s *Session) error {
				s.Gates++
				return nil
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := st.Load("s")
	if got.Gates != n {
		t.Errorf("Gates = %d, want %d (lost updates)", got.Gates, n)
	}
}

func TestCorruptState(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	writeFile(t, filepath.Join(st.SessionDir("s"), "state.json"), "{not json")
	if _, err := st.Load("s"); err == nil || !strings.Contains(err.Error(), "corrupt session state") {
		t.Errorf("Load err = %v", err)
	}
	if _, err := st.Update("s", func(*Session) error { return nil }); err == nil {
		t.Error("Update should surface corrupt state")
	}

	writeFile(t, st.repoFile("/repo"), "{not json")
	if _, err := st.LoadRepo("/repo"); err == nil || !strings.Contains(err.Error(), "corrupt repo cache") {
		t.Errorf("LoadRepo err = %v", err)
	}
}

func TestReadErrors(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	// A directory in place of a file makes reads fail with a non-ENOENT error.
	mkdir(t, filepath.Join(st.SessionDir("s"), "state.json"))
	if _, err := st.Load("s"); err == nil {
		t.Error("Load should fail")
	}
	mkdir(t, st.repoFile("/r"))
	if _, err := st.LoadRepo("/r"); err == nil {
		t.Error("LoadRepo should fail")
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Root is a file: the session dir cannot be created.
	file := filepath.Join(root, "file")
	writeFile(t, file, "x")
	if _, err := New(file).Update("s", func(*Session) error { return nil }); err == nil {
		t.Error("expected mkdir error")
	}

	// state.json is a directory: rename over it fails.
	st := New(t.TempDir())
	mkdir(t, filepath.Join(st.SessionDir("s"), "state.json", "child"))
	if _, err := st.Update("s", func(*Session) error { return nil }); err == nil {
		t.Error("expected error writing over a directory")
	}
}

func TestRepoCache(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	rc, err := st.LoadRepo("/repo")
	if err != nil || rc.LastGate != nil || len(rc.Coverage) != 0 {
		t.Fatalf("empty LoadRepo = %+v, %v", rc, err)
	}
	want := &RepoCache{
		Coverage:   map[string]float64{"a": 90},
		LastGate:   &GateSummary{Passed: true, Coverage: 90, Threshold: 90, Issues: 1, At: time.Unix(0, 0).UTC()},
		LastReport: "/tmp/r.txt",
	}
	if err := st.SaveRepo("/repo", want); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadRepo("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if other, _ := st.LoadRepo("/other"); other.LastGate != nil {
		t.Error("repos must not share caches")
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	var v bool
	if st.CacheGet("k", &v) {
		t.Error("missing entry reported present")
	}
	if err := st.CachePut("k", true); err != nil {
		t.Fatal(err)
	}
	if !st.CacheGet("k", &v) || !v {
		t.Error("entry not read back")
	}
	var wrong int
	if st.CacheGet("k", &wrong) {
		t.Error("type mismatch should report absent")
	}
	if err := st.CachePut("bad", func() {}); err == nil {
		t.Error("unmarshalable value should fail")
	}
}

func TestKey(t *testing.T) {
	t.Parallel()
	if Key("a", "b") == Key("ab") {
		t.Error("parts must be separated")
	}
	if k := Key("x"); k != Key(strings.Clone("x")) || len(k) != 24 {
		t.Errorf("Key unstable or wrong length: %q", k)
	}
}

func TestSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in       string
		readable bool
	}{
		{"abc-DEF_1.2", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../etc", false},
		{"a/b", false},
		{"sp ace", false},
	}
	for _, tc := range tests {
		got := safe(tc.in)
		if tc.readable && got != tc.in {
			t.Errorf("safe(%q) = %q, want unchanged", tc.in, got)
		}
		if !tc.readable && (got == tc.in || strings.ContainsAny(got, `/\ `) || got == "." || got == "..") {
			t.Errorf("safe(%q) = %q, want a hashed single element", tc.in, got)
		}
	}
	if safe("") != "_" {
		t.Errorf("safe(\"\") = %q", safe(""))
	}
}

func TestWriteJSONErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	writeFile(t, file, "x")
	if err := writeJSON(filepath.Join(file, "sub", "x.json"), 1); err == nil {
		t.Error("expected mkdir error under a file")
	}
}

func TestLockContention(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := lock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock(path, 30*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("second lock err = %v, want timeout", err)
	}

	acquired := make(chan error, 1)
	go func() {
		u, err := lock(path, 2*time.Second)
		if err == nil {
			u()
		}
		acquired <- err
	}()
	time.Sleep(50 * time.Millisecond)
	unlock()
	if err := <-acquired; err != nil {
		t.Fatalf("waiter did not acquire after release: %v", err)
	}
}

func TestLockStale(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.lock")
	writeFile(t, path, "")
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(path, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	unlock()
}

func TestLockBadDir(t *testing.T) {
	t.Parallel()
	if _, err := lock(filepath.Join(t.TempDir(), "missing", "x.lock"), time.Second); err == nil {
		t.Error("expected error for missing directory")
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateLockTimeout(t *testing.T) {
	t.Parallel()
	st := New(t.TempDir())
	st.LockTimeout = 20 * time.Millisecond
	writeFile(t, filepath.Join(st.SessionDir("s"), "state.lock"), "")
	if _, err := st.Update("s", func(*Session) error { return nil }); err == nil {
		t.Error("expected lock timeout")
	}
}

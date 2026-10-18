package runlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppend(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested")
	at := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	recs := []Record{
		{Type: "gate", At: at, Session: "s", Repo: "/r", Attempt: 1, Failures: []Failure{{Check: "lint", Rule: "errcheck", File: "a.go"}}},
		{Type: "session", At: at, Session: "s", Reason: "clear", Gates: 2},
	}
	for _, r := range recs {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	var got []Record
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line is not JSON: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 || got[0].Failures[0].Rule != "errcheck" || got[1].Reason != "clear" || !got[0].At.Equal(at) {
		t.Errorf("records = %+v", got)
	}
}

func TestAppendErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(file, Record{}); err == nil {
		t.Error("expected mkdir error when dir is a file")
	}
	if err := os.MkdirAll(filepath.Join(dir, "d", FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Append(filepath.Join(dir, "d"), Record{}); err == nil {
		t.Error("expected open error when log is a directory")
	}
}

func TestAppendMarshalError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// time.Time cannot marshal years beyond 9999.
	if err := Append(dir, Record{At: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Error("expected marshal error")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Error("nothing should be written on marshal error")
	}
}

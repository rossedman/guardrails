package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResultCombined(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  Result
		want string
	}{
		{"empty", Result{}, ""},
		{"stdout only", Result{Stdout: "out"}, "out"},
		{"stderr only", Result{Stderr: "err"}, "err"},
		{"both", Result{Stdout: "out", Stderr: "err"}, "out\nerr"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.res.Combined(); got != tc.want {
				t.Errorf("Combined() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tests := []struct {
		name     string
		cmd      string
		args     []string
		wantOut  string
		wantErr  string
		wantCode int
		startErr bool
	}{
		{name: "success", cmd: "sh", args: []string{"-c", "echo hi; echo oops >&2"}, wantOut: "hi\n", wantErr: "oops\n"},
		{name: "exit code", cmd: "sh", args: []string{"-c", "exit 3"}, wantCode: 3},
		{name: "runs in dir", cmd: "sh", args: []string{"-c", "pwd"}, wantOut: dir + "\n"},
		{name: "missing binary", cmd: "definitely-not-a-real-binary-xyz", startErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := Exec{}.Run(context.Background(), dir, tc.cmd, tc.args...)
			if tc.startErr {
				if err == nil {
					t.Fatal("expected start error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.ExitCode != tc.wantCode {
				t.Errorf("exit = %d, want %d", res.ExitCode, tc.wantCode)
			}
			if tc.wantOut != "" && res.Stdout != tc.wantOut {
				t.Errorf("stdout = %q, want %q", res.Stdout, tc.wantOut)
			}
			if tc.wantErr != "" && res.Stderr != tc.wantErr {
				t.Errorf("stderr = %q, want %q", res.Stderr, tc.wantErr)
			}
		})
	}
}

func TestExecRunCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Exec{}.Run(ctx, "", "sh", "-c", "sleep 5")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestExecLookPath(t *testing.T) {
	t.Parallel()
	if p, err := (Exec{}).LookPath("sh"); err != nil || p == "" {
		t.Errorf("LookPath(sh) = %q, %v", p, err)
	}
	if _, err := (Exec{}).LookPath("definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestFake(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	f := NewFake("go").
		On("go", Result{Stdout: "generic"}).
		On("go test", Result{Stdout: "tests", ExitCode: 1}).
		OnError("go vet", boom).
		OnFunc("go build", func(c Call) (Result, error) {
			return Result{Stdout: "built in " + c.Dir}, nil
		}).
		Install("git")
	ctx := context.Background()

	tests := []struct {
		name    string
		cmd     string
		args    []string
		want    Result
		wantErr error
	}{
		{"longest prefix wins", "go", []string{"test", "./..."}, Result{Stdout: "tests", ExitCode: 1}, nil},
		{"shorter prefix", "go", []string{"version"}, Result{Stdout: "generic"}, nil},
		{"error", "go", []string{"vet", "./..."}, Result{}, boom},
		{"func", "go", []string{"build", "./..."}, Result{Stdout: "built in /repo"}, nil},
		{"unmatched", "ls", nil, Result{}, nil},
	}
	for _, tc := range tests {
		res, err := f.Run(ctx, "/repo", tc.cmd, tc.args...)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
		if res != tc.want {
			t.Errorf("%s: res = %+v, want %+v", tc.name, res, tc.want)
		}
	}

	calls := f.Calls()
	if len(calls) != len(tests) {
		t.Fatalf("got %d calls, want %d", len(calls), len(tests))
	}
	if calls[0].String() != "go test ./..." || calls[0].Dir != "/repo" {
		t.Errorf("first call = %+v", calls[0])
	}
	if !f.Ran("go vet") || f.Ran("terraform") {
		t.Error("Ran reported wrong history")
	}

	for _, name := range []string{"go", "git"} {
		if p, err := f.LookPath(name); err != nil || p != "/usr/bin/"+name {
			t.Errorf("LookPath(%s) = %q, %v", name, p, err)
		}
	}
	if _, err := f.LookPath("golangci-lint"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LookPath missing: err = %v", err)
	}
}

func TestFakeCancelled(t *testing.T) {
	t.Parallel()
	f := NewFake().On("x", Result{Stdout: "y"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Run(ctx, "", "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(f.Calls()) != 1 {
		t.Error("cancelled call not recorded")
	}
}

func TestCallString(t *testing.T) {
	t.Parallel()
	if got := (Call{Name: "go"}).String(); got != "go" {
		t.Errorf("got %q", got)
	}
	if got := (Call{Name: "go", Args: []string{"a", "b"}}).String(); !strings.HasSuffix(got, "a b") {
		t.Errorf("got %q", got)
	}
}

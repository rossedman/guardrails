// Package runner abstracts external process execution so modules can be
// tested without the real toolchain installed.
package runner

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Result is the outcome of running one external command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout followed by stderr.
func (r Result) Combined() string {
	if r.Stdout == "" {
		return r.Stderr
	}
	if r.Stderr == "" {
		return r.Stdout
	}
	return r.Stdout + "\n" + r.Stderr
}

// Runner executes external tools.
type Runner interface {
	// Run executes name with args in dir. A non-zero exit code is reported
	// in Result, not as an error; err is reserved for failures to start the
	// process or context cancellation.
	Run(ctx context.Context, dir, name string, args ...string) (Result, error)
	// LookPath reports whether a tool is installed and where.
	LookPath(name string) (string, error)
}

// Exec is the Runner backed by os/exec.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, dir, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		return res, ctx.Err()
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return res, err
	}
}

// LookPath implements Runner.
func (Exec) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

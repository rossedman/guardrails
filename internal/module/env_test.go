package module

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rossedman/guardrails/internal/runner"
	"github.com/rossedman/guardrails/internal/tools"
)

type fixedResolver map[string]tools.Command

func (f fixedResolver) Resolve(_ context.Context, name string) (tools.Command, error) {
	if c, ok := f[name]; ok {
		return c, nil
	}
	return tools.Command{}, errors.New(name + ": " + tools.ErrInstalling.Error())
}

func TestEnvToolFallsBackToPath(t *testing.T) {
	t.Parallel()
	env := Env{Runner: runner.NewFake("lint").On("lint --x", runner.Result{Stdout: "ok"})}
	cmd, err := env.Tool(context.Background(), "lint")
	if err != nil || cmd.Path != "lint" || cmd.Managed {
		t.Fatalf("cmd = %+v, %v", cmd, err)
	}
	res, err := env.RunTool(context.Background(), "/repo", "lint", "--x")
	if err != nil || res.Stdout != "ok" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if _, err := env.Tool(context.Background(), "absent"); !errors.Is(err, tools.ErrMissing) {
		t.Errorf("absent: %v", err)
	}
	if _, err := env.RunTool(context.Background(), "/repo", "absent"); !IsUnavailable(err) {
		t.Errorf("run absent: %v", err)
	}
}

func TestEnvToolUsesResolver(t *testing.T) {
	t.Parallel()
	fake := runner.NewFake()
	env := Env{Runner: fake, Tools: fixedResolver{"ruff": {Path: "/managed/ruff", Args: []string{"--quiet"}, Managed: true}}}
	if _, err := env.RunTool(context.Background(), "/repo", "ruff", "check", "a.py"); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].String() != "/managed/ruff --quiet check a.py" || calls[0].Dir != "/repo" {
		t.Fatalf("calls = %+v", calls)
	}
	if _, err := env.Tool(context.Background(), "mypy"); err == nil {
		t.Error("unresolvable tool succeeded")
	}
}

func TestUnavailable(t *testing.T) {
	t.Parallel()
	tool := Tool{Name: "shellcheck", Install: "managed"}
	cases := []struct {
		err  error
		want string
	}{
		{tools.ErrInstalling, "being installed in the background"},
		{tools.ErrMissing, "shellcheck is not installed (managed)"},
		{errors.New("checksum mismatch"), "shellcheck: checksum mismatch"},
	}
	for _, c := range cases {
		if got := Unavailable(tool, c.err); !strings.Contains(got, c.want) {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
	if got := Unavailable(Tool{Name: "x"}, tools.ErrMissing); got != "x is not installed" {
		t.Errorf("no install hint: %q", got)
	}
	if !IsUnavailable(tools.ErrInstalling) || !IsUnavailable(tools.ErrMissing) || IsUnavailable(errors.New("exit 1")) {
		t.Error("IsUnavailable")
	}
}

// Command guardrails enforces code quality standards on Claude Code through
// hooks. See https://rossedman.io/guardrails/.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/rossedman/guardrails/internal/cli"
)

// main runs the CLI and exits with its status code; Ctrl-C cancels the context.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.New().Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// Command guardrails enforces code quality standards on Claude Code through
// hooks. See https://rossedman.github.io/guardrails/.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/rossedman/guardrails/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.New().Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

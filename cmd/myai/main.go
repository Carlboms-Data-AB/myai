// Command myai manages a local coding agent stack: a local inference backend,
// downloaded models and the OpenCode frontend, with background services on
// macOS, Windows and Linux.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/carlbomsdata/myai/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:]))
}

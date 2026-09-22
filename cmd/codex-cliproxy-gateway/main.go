package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"codex-cliproxy-gateway/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	streams := app.Streams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
		IsTerminal: func() bool {
			info, err := os.Stdin.Stat()
			return err == nil && info.Mode()&os.ModeCharDevice != 0
		},
	}
	os.Exit(app.Run(ctx, os.Args[1:], streams))
}

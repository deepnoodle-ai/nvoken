package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/deepnoodle-ai/wonton/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app := newApp()
	if err := app.ExecuteContext(ctx, os.Args[1:]); err != nil {
		app.PrintError(err)
		os.Exit(cli.GetExitCode(err))
	}
}

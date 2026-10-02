package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/pyxcloud/pyxcloud-cli/internal/passocli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(passocli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

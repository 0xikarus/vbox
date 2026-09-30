package main

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/cli"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := cli.New().Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vbox:", err)
		os.Exit(cli.ExitCode(err))
	}
}

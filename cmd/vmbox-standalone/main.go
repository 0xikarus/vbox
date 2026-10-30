package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/standalone"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dir := flag.String("data", "/data/standalone", "persistent controller directory")
	listen := flag.String("listen", ":8080", "gateway address")
	prepare := flag.Bool("prepare-tools", false, "prepare selected container packages as root")
	flag.Parse()
	if *prepare {
		if err := standalone.PrepareTools(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := standalone.Serve(ctx, *dir, *listen); err != nil {
		fmt.Fprintln(os.Stderr, "vmbox-standalone:", err)
		os.Exit(1)
	}
}

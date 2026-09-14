package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xikarus/vmbox-service/internal/workeragent"
)

func main() {
	configFile := flag.String("config", "/var/lib/vmbox-worker/config.json", "private worker configuration file")
	supervise := flag.Bool("supervise", false, "restart only the worker agent process")
	verify := flag.Bool("verify-installation", false, "verify private installed identity against stdin")
	flag.Parse()
	if *verify {
		if err := workeragent.VerifyInstallation(*configFile, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "worker installation verification failed")
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *supervise {
		if err := workeragent.Supervise(ctx, *configFile, func(message string) { fmt.Fprintln(os.Stderr, message) }); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "worker supervisor unavailable")
			os.Exit(1)
		}
		return
	}
	lock, err := workeragent.Lock(*configFile + ".agent.lock")
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker agent already running or lock unavailable")
		os.Exit(1)
	}
	defer lock.Close()
	config, err := workeragent.Load(*configFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker configuration unavailable")
		os.Exit(1)
	}
	agent := workeragent.Agent{Config: config, ConfigFile: *configFile, Report: func(message string) { fmt.Fprintln(os.Stderr, message) }}
	if err = agent.Run(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "worker agent stopped")
		os.Exit(1)
	}
}

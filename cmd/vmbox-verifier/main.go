// vmbox-verifier reads its private job from stdin; --child accepts only checks.
package main

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/factory/verifyjob"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--child" {
		err = verifyjob.Child(ctx, os.Stdin, os.Stdout)
	} else if len(os.Args) == 1 {
		err = verifyjob.Execute(ctx, os.Stdin)
	} else {
		err = fmt.Errorf("invalid arguments")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Verification execution or delivery failed; inspect the private attempt record.")
		os.Exit(1)
	}
}

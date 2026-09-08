// vmbox-builder consumes a private, controller-staged job on stdin. It never
// prints the prompt, credentials, CLI diagnostics, or agent-produced document.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xikarus/vmbox-service/internal/factory/buildjob"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := buildjob.Execute(ctx, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "Builder execution or result delivery failed; inspect the durable attempt record.")
		os.Exit(1)
	}
}

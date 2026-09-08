// vmbox-task-runner accepts its private, source-free job on stdin only.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/0xikarus/vmbox-service/internal/taskflowruntime"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--agent-child" {
		err = taskflowruntime.AgentChild(ctx, os.Stdin, os.Stdout)
	} else if len(os.Args) == 1 {
		err = taskflowruntime.Execute(ctx, os.Stdin)
	} else {
		err = fmt.Errorf("unsupported arguments")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

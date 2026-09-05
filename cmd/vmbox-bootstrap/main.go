//go:build vmbox_operator

package main

import (
	"context"
	"fmt"
	"github.com/0xikarus/vmbox-service/internal/cli"
	"os"
)

func main() {
	if err := cli.New().Bootstrap(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

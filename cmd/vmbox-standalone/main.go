package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xikarus/vmbox-service/internal/modelcatalog"
	"github.com/0xikarus/vmbox-service/internal/standalone"
)

func main() {
	dir := flag.String("data", "/data/standalone", "persistent controller directory")
	listen := flag.String("listen", ":8080", "gateway address")
	prepare := flag.Bool("prepare-tools", false, "prepare selected container packages as root")
	catalog := flag.String("profile-models", "", "query a profile catalog from private stdin without starting a box")
	flag.Parse()
	if *catalog != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var files map[string][]byte
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 4<<20))
		if *catalog != "codex" || decoder.Decode(&files) != nil {
			fmt.Fprintln(os.Stderr, "invalid catalog request")
			os.Exit(1)
		}
		defer func() {
			for _, v := range files {
				clear(v)
			}
		}()
		choices, err := modelcatalog.QueryCodex(ctx, files, "codex")
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not query saved Codex account")
			os.Exit(1)
		}
		if json.NewEncoder(os.Stdout).Encode(choices) != nil {
			os.Exit(1)
		}
		return
	}
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

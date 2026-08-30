package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vmbox-runtime:", err)
		os.Exit(1)
	}
}
func run() error {
	runtime := boxruntime.New("")
	args := os.Args[1:]
	switch filepath.Base(os.Args[0]) {
	case "vmbox-report":
		args = append([]string{"report"}, args...)
	case "vmbox-finish":
		args = append([]string{"finish"}, args...)
	case "vmbox-ask":
		args = append([]string{"ask"}, args...)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox-runtime run [--detach] -- COMMAND [ARG...] | exec-json DATA | put-file PATH MODE | welcome | report | ask")
	}
	switch args[0] {
	case "put-file":
		if len(args) != 3 {
			return fmt.Errorf("put-file requires PATH MODE")
		}
		path := filepath.Clean(args[1])
		if path != "/data" && !strings.HasPrefix(path, "/data/") {
			return fmt.Errorf("put-file destination must be below /data")
		}
		mode, err := strconv.ParseUint(args[2], 8, 9)
		if err != nil {
			return fmt.Errorf("invalid put-file mode: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".vmbox-upload-*")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if err := tmp.Chmod(os.FileMode(mode)); err != nil {
			tmp.Close()
			return err
		}
		if _, err := io.Copy(tmp, io.LimitReader(os.Stdin, 16<<20)); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmpPath, path)
	case "welcome":
		welcome := filepath.Join(os.Getenv("HOME"), ".vmbox-welcome")
		if data, err := os.ReadFile(welcome); err == nil {
			_, _ = os.Stdout.Write(data)
			if len(data) == 0 || data[len(data)-1] != '\n' {
				fmt.Println()
			}
		} else {
			fmt.Printf("vmbox %s is ready\nProvider: %s  Region: %s\nSpecs: %s CPU / %s MiB RAM / %s GiB disk\nWorkspace: %s\nCost: %s\nDetach: press Ctrl-b, release both keys, then press d\n\n",
				os.Getenv("VMBOX_NAME"), os.Getenv("VMBOX_PROVIDER"), os.Getenv("VMBOX_REGION"), os.Getenv("VMBOX_CPU"), os.Getenv("VMBOX_MEMORY_MIB"), os.Getenv("VMBOX_DISK_GIB"), os.Getenv("VMBOX_WORKSPACE"), os.Getenv("VMBOX_COST"))
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd := exec.Command(shell, "-l")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	case "exec-json":
		if len(args) != 2 {
			return fmt.Errorf("exec-json requires one encoded argv")
		}
		argv, err := boxruntime.DecodeArgv(args[1])
		if err != nil {
			return err
		}
		run, err := runtime.Run(context.Background(), argv, os.Stdout, os.Stderr)
		if err != nil {
			return err
		}
		if run.ExitCode != nil {
			os.Exit(*run.ExitCode)
		}
		return nil
	case "run":
		detach := false
		internal := false
		i := 1
		for i < len(args) && args[i] != "--" {
			switch args[i] {
			case "--detach":
				detach = true
			case "--internal-background":
				internal = true
			default:
				return fmt.Errorf("unknown run option %q", args[i])
			}
			i++
		}
		if i >= len(args) || i+1 >= len(args) {
			return fmt.Errorf("run requires -- COMMAND [ARG...]")
		}
		argv := args[i+1:]
		if detach && !internal {
			id, err := runtime.RunDetached(argv)
			if err == nil {
				fmt.Println(id)
			}
			return err
		}
		run, err := runtime.Run(context.Background(), argv, os.Stdout, os.Stderr)
		if err != nil {
			return err
		}
		if run.ExitCode != nil {
			os.Exit(*run.ExitCode)
		}
		return nil
	case "report":
		kind, message, err := parseReport(args[1:])
		if err != nil {
			return err
		}
		return runtime.Report(os.Getenv("VMBOX_RUN_ID"), kind, message)
	case "finish":
		kind := "success"
		i := 1
		if i < len(args) && (args[i] == "--failed" || args[i] == "--success") {
			if args[i] == "--failed" {
				kind = "failed"
			}
			i++
		}
		if i < len(args) && args[i] == "--summary" {
			i++
		}
		return runtime.Report(os.Getenv("VMBOX_RUN_ID"), kind, strings.Join(args[i:], " "))
	case "ask":
		timeout := time.Duration(0)
		i := 1
		if i+1 < len(args) && args[i] == "--timeout" {
			value, err := time.ParseDuration(args[i+1])
			if err != nil {
				return err
			}
			timeout = value
			i += 2
		}
		if i >= len(args) {
			return fmt.Errorf("ask requires a prompt")
		}
		answer, err := runtime.Ask(context.Background(), os.Getenv("VMBOX_RUN_ID"), strings.Join(args[i:], " "), timeout)
		if err == nil {
			fmt.Println(answer)
		}
		return err
	case "answer":
		if len(args) != 4 {
			return fmt.Errorf("answer RUN_ID QUESTION_ID TEXT")
		}
		path := runtime.Root + "/runs/" + args[1] + "/questions/" + args[2] + ".answer"
		return os.WriteFile(path, []byte(args[3]+"\n"), 0600)
	case "status":
		id := ""
		if len(args) > 1 {
			id = args[1]
		}
		store, err := runtime.StoreFor(id)
		if err != nil {
			return err
		}
		status, err := store.ReadStatus()
		if err != nil {
			return err
		}
		data, _ := strconv.Unquote(strconv.Quote(fmt.Sprintf("%+v", status)))
		fmt.Println(data)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseReport(args []string) (kind, message string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("report requires a message")
	}
	kind = "progress"
	if strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--progress":
		case "--needs-input":
			kind = "needs_input"
		default:
			return "", "", fmt.Errorf("unknown report option %q", args[0])
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return "", "", fmt.Errorf("report requires a message")
	}
	return kind, strings.Join(args, " "), nil
}

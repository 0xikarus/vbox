package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func runTmuxInteraction(args []string, runtime *boxruntime.Runtime) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "tmux-task":
		if len(args) != 5 {
			return true, fmt.Errorf("tmux-task requires SESSION AGENT MESSAGE_ID BASE64_PROMPT")
		}
		prompt, err := base64.RawURLEncoding.DecodeString(args[4])
		if err != nil {
			return true, fmt.Errorf("decode task prompt: %w", err)
		}
		return true, boxruntime.StartTmuxTask(context.Background(), runtime.Root, args[1], args[2], args[3], string(prompt))
	case "tmux-message":
		if len(args) != 5 {
			return true, fmt.Errorf("tmux-message requires SESSION MESSAGE_ID BASE64_TEXT SUBMIT")
		}
		text, err := base64.RawURLEncoding.DecodeString(args[3])
		if err != nil {
			return true, fmt.Errorf("decode tmux message: %w", err)
		}
		submit, err := strconv.ParseBool(args[4])
		if err != nil {
			return true, fmt.Errorf("decode tmux message submit flag: %w", err)
		}
		return true, boxruntime.DeliverTmuxInput(context.Background(), runtime.Root, args[1], args[2], string(text), submit)
	case "tmux-screen":
		if len(args) < 2 || len(args) > 3 {
			return true, fmt.Errorf("tmux-screen requires SESSION [HISTORY_LINES]")
		}
		history := 200
		if len(args) == 3 {
			var err error
			history, err = strconv.Atoi(args[2])
			if err != nil {
				return true, fmt.Errorf("decode history lines: %w", err)
			}
		}
		snapshot, err := boxruntime.CaptureTmuxScreen(context.Background(), args[1], history)
		if err != nil {
			return true, err
		}
		snapshot.BoxName = os.Getenv("VMBOX_NAME")
		return true, json.NewEncoder(os.Stdout).Encode(snapshot)
	default:
		return false, nil
	}
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/boxruntime"
)

func runTmuxInteraction(args []string, runtime *boxruntime.Runtime) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "process-start":
		if len(args) != 2 {
			return true, fmt.Errorf("process-start requires encoded task")
		}
		b, err := base64.RawURLEncoding.DecodeString(args[1])
		if err != nil {
			return true, err
		}
		var task v1.ProcessTask
		if err = json.Unmarshal(b, &task); err != nil {
			return true, err
		}
		return true, boxruntime.StartProcess(context.Background(), runtime.Root, task)
	case "process-run":
		if len(args) != 3 {
			return true, fmt.Errorf("process-run requires root and ID")
		}
		return true, boxruntime.RunProcess(args[1], args[2])
	case "process-status":
		if len(args) != 2 {
			return true, fmt.Errorf("process-status requires ID")
		}
		result, err := boxruntime.ReadProcess(runtime.Root, args[1])
		if err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(result)
	case "interactive-start":
		if len(args) != 4 && len(args) != 5 {
			return true, fmt.Errorf("interactive-start requires assignment, session, agent")
		}
		if _, err := boxruntime.NativeSessions(context.Background(), args[1]); err != nil {
			return true, err
		}
		startCLI := ""
		if len(args) == 5 {
			startCLI = args[4]
		}
		return true, boxruntime.StartInteractiveCommand(context.Background(), runtime.Root, args[2], args[3], startCLI)
	case "native-bind", "native-sessions", "native-welcome":
		if len(args) != 2 {
			return true, fmt.Errorf("%s requires ASSIGNMENT", args[0])
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		switch args[0] {
		case "native-bind":
			return true, boxruntime.SetNativeAssignment(ctx, args[1])
		case "native-welcome":
			return true, boxruntime.NativeWelcome(ctx, args[1])
		}
		value, err := boxruntime.NativeSessions(ctx, args[1])
		if err != nil {
			return true, err
		}
		return true, json.NewEncoder(os.Stdout).Encode(value)
	case "native-attach":
		if len(args) != 4 {
			return true, fmt.Errorf("native-attach requires ASSIGNMENT SESSION_ID INCARNATION")
		}
		return true, boxruntime.NativeAttach(context.Background(), runtime.Root, args[1], args[2], args[3])
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
		if len(args) != 5 && len(args) != 6 {
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
		steer := false
		if len(args) == 6 {
			steer, err = strconv.ParseBool(args[5])
			if err != nil {
				return true, fmt.Errorf("invalid steer flag")
			}
		}
		return true, boxruntime.DeliverTmuxInput(context.Background(), runtime.Root, args[1], args[2], string(text), submit, steer)
	case "tmux-keys":
		if len(args) != 4 {
			return true, fmt.Errorf("tmux-keys requires SESSION MESSAGE_ID BASE64_KEYS")
		}
		data, err := base64.RawURLEncoding.DecodeString(args[3])
		if err != nil {
			return true, fmt.Errorf("decode tmux keys: %w", err)
		}
		var keys []string
		if err := json.Unmarshal(data, &keys); err != nil {
			return true, fmt.Errorf("decode tmux keys: %w", err)
		}
		return true, boxruntime.DeliverTmuxKeys(context.Background(), runtime.Root, args[1], args[2], keys)
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
	case "chat-pull":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-pull requires SESSION")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		event, found, err := boxruntime.PullChatEvent(home, args[1])
		if err != nil {
			return true, err
		}
		if !found {
			return true, nil
		}
		return true, json.NewEncoder(os.Stdout).Encode(event)
	case "chat-ack":
		if len(args) != 3 {
			return true, fmt.Errorf("chat-ack requires SESSION EVENT_ID")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		return true, boxruntime.AckChatEvent(home, args[1], args[2])
	case "chat-deliver":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-deliver requires SESSION")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 100<<20))
		if err != nil {
			return true, err
		}
		var inbound boxruntime.ChatInbound
		if err := json.Unmarshal(data, &inbound); err != nil {
			return true, fmt.Errorf("invalid inbound chat envelope")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		return true, boxruntime.StoreChatInbound(home, args[1], inbound)
	case "chat-codex":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-codex requires SESSION")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 100<<20))
		if err != nil {
			return true, err
		}
		var inbound boxruntime.ChatInbound
		if err := json.Unmarshal(data, &inbound); err != nil {
			return true, fmt.Errorf("invalid inbound chat envelope")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		return true, boxruntime.DeliverCodexChat(context.Background(), home, args[1], inbound)
	case "chat-codex-start":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-codex-start requires SESSION")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 100<<20))
		if err != nil {
			return true, err
		}
		var inbound boxruntime.ChatInbound
		if err := json.Unmarshal(data, &inbound); err != nil {
			return true, fmt.Errorf("invalid inbound chat envelope")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		return true, boxruntime.StartCodexChat(context.Background(), runtime.Root, home, args[1], inbound)
	case "chat-codex-name":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-codex-name requires SESSION")
		}
		return true, boxruntime.NameCodexChatThread(context.Background(), runtime.Root, args[1])
	case "chat-opencode":
		if len(args) != 2 {
			return true, fmt.Errorf("chat-opencode requires SESSION")
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 100<<20))
		if err != nil {
			return true, err
		}
		var inbound boxruntime.ChatInbound
		if err := json.Unmarshal(data, &inbound); err != nil {
			return true, fmt.Errorf("invalid inbound chat envelope")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return true, err
		}
		return true, boxruntime.DeliverOpenCodeChat(context.Background(), home, args[1], inbound)
	default:
		return false, nil
	}
}

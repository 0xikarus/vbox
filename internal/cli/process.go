package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/url"
)

func (a *App) processOutput(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: task-output BOX TASK_ID")
	}
	var task v1.ProcessTask
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/process-tasks/"+url.PathEscape(args[1]), nil, &task, nil); err != nil {
		return err
	}
	if task.BoxName != args[0] && task.LogicalBoxID != args[0] {
		return fmt.Errorf("task belongs to another logical box")
	}
	var out json.RawMessage
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/process-tasks/"+url.PathEscape(args[1])+"/output", nil, &out, nil); err != nil {
		return err
	}
	return json.NewEncoder(a.Out).Encode(out)
}

func (a *App) openInteractive(ctx context.Context, c config.Context, token string, box v1.LogicalBox, session, agent string) error {
	if session != "" {
		return a.attachNative(ctx, c, token, box, session)
	}
	if agent == "" {
		inv, err := a.sessionInventory(ctx, c, token, box.ID)
		if err != nil {
			return err
		}
		if len(inv.Sessions) > 0 {
			fmt.Fprintln(a.Err, "Existing sessions:")
			for _, s := range inv.Sessions {
				fmt.Fprintf(a.Err, "  %s\n", s.Name)
			}
			fmt.Fprintln(a.Err, "Enter an exact session name to reconnect, or choose codex / claude / shell to start a new session.")
		}
		defaultAgent := box.DefaultAgent
		if defaultAgent != "claude" && defaultAgent != "shell" {
			defaultAgent = "codex"
		}
		choice, err := a.readControllerPrompt(bufio.NewReader(a.In), "Agent (codex / claude / shell) or session", defaultAgent)
		if err != nil {
			return err
		}
		for _, s := range inv.Sessions {
			if s.Name == choice {
				return a.attachNative(ctx, c, token, box, s.Name)
			}
		}
		agent = choice
	}
	if agent != "codex" && agent != "claude" && agent != "shell" {
		return fmt.Errorf("choose codex, claude, shell, or an exact session name")
	}
	var out struct {
		Session string `json:"session"`
	}
	if _, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/interactive", map[string]string{"agent": agent}, &out, nil); err != nil {
		return err
	}
	if out.Session == "" {
		return fmt.Errorf("controller returned no interactive session")
	}
	return a.attachNative(ctx, c, token, box, out.Session)
}

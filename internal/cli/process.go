package cli

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"net/http"
	"net/url"
)

func (a *App) processOutput(ctx context.Context, c config.Context, token string, args []string) error {
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
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
	if asJSON {
		return json.NewEncoder(a.Out).Encode(out)
	}
	var result struct {
		Output    string `json:"output"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return err
	}
	if result.Truncated {
		fmt.Fprintln(a.Err, "Output truncated by controller.")
	}
	_, err := fmt.Fprint(a.Out, result.Output)
	return err
}

func (a *App) openInteractive(ctx context.Context, c config.Context, token string, box v1.LogicalBox, session, agent string, forcePicker bool) error {
	return a.openInteractiveStartup(ctx, c, token, box, session, agent, forcePicker, "")
}

func (a *App) openInteractiveStartup(ctx context.Context, c config.Context, token string, box v1.LogicalBox, session, agent string, forcePicker bool, startCLI string) error {
	if session == "" && !forcePicker && (agent == "" || startCLI != "") {
		var out struct {
			Session string `json:"session"`
		}
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/interactive", map[string]any{"agent": "shell", "reuseShell": startCLI == "", "startCli": startCLI}, &out, nil)
		if err != nil {
			return err
		}
		if out.Session == "" {
			return fmt.Errorf("controller returned no shell session")
		}
		return a.attachRemembered(ctx, c, token, box, out.Session, nil, false)
	}
	if session != "" {
		return a.attachRemembered(ctx, c, token, box, session, nil, true)
	}
	if agent == "" {
		inv, err := a.sessionInventory(ctx, c, token, box.ID)
		if err != nil {
			return err
		}
		if inv.State != "live" {
			return fmt.Errorf("session inventory is %s", inv.State)
		}
		var primary struct {
			Session string `json:"session"`
		}
		if _, err = a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box.ID)+"/sessions/primary", nil, &primary, nil); err != nil {
			return fmt.Errorf("primary session unavailable; controller may need upgrading: %w", err)
		}
		initial := 0
		for i, s := range inv.Sessions {
			if s.Name == primary.Session {
				if !forcePicker {
					return a.attachRemembered(ctx, c, token, box, s.Name, &s, false)
				}
				initial = i
			}
		}
		if len(inv.Sessions) > 0 {
			// Adopt a single existing session once; never create a duplicate just
			// because the box has not recorded a primary yet.
			i := 0
			if forcePicker || len(inv.Sessions) > 1 || primary.Session != "" {
				labels := make([]string, len(inv.Sessions))
				for j, s := range inv.Sessions {
					labels[j] = s.Name
					if s.Name == primary.Session {
						labels[j] += "  (primary)"
					}
				}
				title := "Sessions · " + box.Name
				if inv.Partial {
					title += " (partial inventory)"
				}
				i, err = a.selectTUI(ctx, title, labels, initial)
				if err != nil {
					return err
				}
			}
			selected := inv.Sessions[i]
			return a.attachRemembered(ctx, c, token, box, selected.Name, &selected, true)
		}
		if forcePicker || inv.Partial {
			return fmt.Errorf("no selectable sessions (partial=%t); nothing created", inv.Partial)
		}
		agents := []string{"codex", "claude", "shell"}
		for i, value := range agents {
			if value == box.DefaultAgent {
				initial = i
			}
		}
		title := "Start interactive session · " + box.Name
		if primary.Session != "" {
			title = "Primary session unavailable · choose a new agent"
		}
		i, err := a.selectTUI(ctx, title, agents, initial)
		if err != nil {
			return err
		}
		agent = agents[i]
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
	return a.attachRemembered(ctx, c, token, box, out.Session, nil, true)
}

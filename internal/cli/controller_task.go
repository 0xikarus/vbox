package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

type controllerTaskOptions struct {
	box, agent, session, prompt string
	jsonOutput                  bool
}

func (a *App) controllerTask(ctx context.Context, c config.Context, token string, args []string) error {
	opts, err := parseControllerTaskOptions(args)
	if err != nil {
		return err
	}
	interactive := a.IsTerminal != nil && a.IsTerminal()
	reader := bufio.NewReader(a.In)
	if opts.box == "" {
		if !interactive {
			return fmt.Errorf("task requires a logical box outside an interactive terminal")
		}
		opts.box, err = a.promptTaskBox(ctx, c, token, reader)
		if err != nil {
			return err
		}
	}
	if opts.agent == "" {
		if interactive {
			opts.agent, err = a.promptTaskAgent(reader)
			if err != nil {
				return err
			}
		} else {
			opts.agent = "codex"
		}
	}
	if !validTaskAgent(opts.agent) {
		return fmt.Errorf("agent must be codex, claude, opencode, or shell")
	}
	if opts.prompt == "" {
		if !interactive {
			return fmt.Errorf("task requires --prompt outside an interactive terminal")
		}
		opts.prompt, err = a.readControllerPrompt(reader, "Task prompt", "")
		if err != nil {
			return err
		}
	}

	request := v1.CreateBoxTaskRequest{Agent: opts.agent, Session: opts.session, Prompt: opts.prompt}
	var task v1.BoxTask
	status, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(opts.box)+"/tasks", request, &task, map[string]string{
		"Idempotency-Key": "cli-task:" + opts.box + ":" + strconv.FormatInt(time.Now().UnixNano(), 36),
	})
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return json.NewEncoder(a.Out).Encode(task)
	}
	fmt.Fprintf(a.Out, "Scheduled %s task %s on %s (%s, HTTP %d).\n", task.Agent, task.ID, task.BoxName, task.State, status)
	fmt.Fprintf(a.Out, "The controller will allocate compute automatically. Reconnect with: vmbox %s\n", task.BoxName)
	return nil
}

func (a *App) controllerTaskStatus(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("task-status requires a logical box and optional task ID")
	}
	box := args[0]
	if len(args) == 1 {
		var tasks []v1.BoxTask
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box)+"/tasks", nil, &tasks, nil); err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(tasks)
	}
	var task v1.BoxTask
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/tasks/"+url.PathEscape(args[1]), nil, &task, nil); err != nil {
		return err
	}
	if task.BoxName != box && task.LogicalBoxID != box {
		return fmt.Errorf("task %q belongs to logical box %q, not %q", task.ID, task.BoxName, box)
	}
	return json.NewEncoder(a.Out).Encode(task)
}

func parseControllerTaskOptions(args []string) (controllerTaskOptions, error) {
	var opts controllerTaskOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		next := func() (string, error) {
			index++
			if index >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			return args[index], nil
		}
		var err error
		switch arg {
		case "--agent":
			opts.agent, err = next()
		case "--session":
			opts.session, err = next()
		case "--prompt":
			opts.prompt, err = next()
		case "--json":
			opts.jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown task option %q", arg)
			}
			if opts.box != "" {
				return opts, fmt.Errorf("task accepts only one logical box")
			}
			opts.box = arg
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func (a *App) promptTaskBox(ctx context.Context, c config.Context, token string, reader *bufio.Reader) (string, error) {
	var boxes []v1.LogicalBox
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
		return "", err
	}
	if len(boxes) == 0 {
		return "", fmt.Errorf("no logical boxes are available; create one with 'vmbox new NAME'")
	}
	fmt.Fprintln(a.Out, "Select a logical box:")
	for index, box := range boxes {
		fmt.Fprintf(a.Out, "  %d) %s (%s)\n", index+1, box.Name, box.State)
	}
	choice, err := a.readControllerPrompt(reader, "Box number or name", "1")
	if err != nil {
		return "", err
	}
	if number, numberErr := strconv.Atoi(choice); numberErr == nil {
		if number < 1 || number > len(boxes) {
			return "", fmt.Errorf("box selection must be between 1 and %d", len(boxes))
		}
		return boxes[number-1].Name, nil
	}
	for _, box := range boxes {
		if box.Name == choice {
			return choice, nil
		}
	}
	return "", fmt.Errorf("logical box %q is not in the controller inventory", choice)
}

func (a *App) promptTaskAgent(reader *bufio.Reader) (string, error) {
	fmt.Fprintln(a.Out, "Choose an agent: 1) codex  2) claude  3) opencode  4) shell")
	choice, err := a.readControllerPrompt(reader, "Agent number or name", "1")
	if err != nil {
		return "", err
	}
	agents := []string{"codex", "claude", "opencode", "shell"}
	if number, numberErr := strconv.Atoi(choice); numberErr == nil {
		if number < 1 || number > len(agents) {
			return "", fmt.Errorf("agent selection must be between 1 and %d", len(agents))
		}
		return agents[number-1], nil
	}
	return strings.ToLower(choice), nil
}

func validTaskAgent(value string) bool {
	switch strings.ToLower(value) {
	case "codex", "claude", "opencode", "shell":
		return true
	default:
		return false
	}
}

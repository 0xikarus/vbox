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
	idempotency                 string
	jsonOutput                  bool
	model                       string
	args                        []string
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
		opts.box, err = a.promptTaskBox(ctx, c, token)
		if err != nil {
			return err
		}
	}
	if opts.agent == "" {
		if interactive {
			opts.agent, err = a.promptTaskAgent(ctx)
			if err != nil {
				return err
			}
		} else {
			opts.agent = "codex"
		}
	}
	if opts.agent != "codex" && opts.agent != "claude" && opts.agent != "shell" {
		return fmt.Errorf("one-shot agent must be codex, claude, or shell")
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

	if err := v1.ValidateProcessOptions(opts.agent, opts.model, opts.args); err != nil {
		return err
	}
	request := v1.CreateBoxTaskRequest{Agent: opts.agent, Session: opts.session, Prompt: opts.prompt, Model: opts.model, Args: opts.args}
	var task v1.ProcessTask
	if opts.idempotency == "" {
		opts.idempotency = "cli-task:" + opts.box + ":" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	status, err := a.request(ctx, c, token, http.MethodPost, "/v1/logical-boxes/"+url.PathEscape(opts.box)+"/process-tasks", request, &task, map[string]string{
		"Idempotency-Key": opts.idempotency,
	})
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return json.NewEncoder(a.Out).Encode(task)
	}
	fmt.Fprintf(a.Out, "%s · %s · %s\n", tuiLabel(task.BoxName, 100), tuiLabel(task.ID, 100), tuiLabel(task.State, 40))
	if a.Verbose {
		fmt.Fprintf(a.Err, "Scheduled %s (HTTP %d). Status: vmbox task-status %q %s\n", task.Agent, status, task.BoxName, task.ID)
	}
	return nil
}

func (a *App) controllerTaskStatus(ctx context.Context, c config.Context, token string, args []string) error {
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("task-status requires a logical box and optional task ID")
	}
	box := args[0]
	if len(args) == 1 {
		var tasks []v1.ProcessTask
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes/"+url.PathEscape(box)+"/process-tasks", nil, &tasks, nil); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(a.Out).Encode(tasks)
		}
		for _, task := range tasks {
			if err := a.processTaskSummary(task); err != nil {
				return err
			}
		}
		return nil
	}
	var task v1.ProcessTask
	status, err := a.request(ctx, c, token, http.MethodGet, "/v1/process-tasks/"+url.PathEscape(args[1]), nil, &task, nil)
	if status == http.StatusNotFound {
		_, err = a.request(ctx, c, token, http.MethodGet, "/v1/tasks/"+url.PathEscape(args[1]), nil, &task, nil)
	}
	if err != nil {
		return err
	}
	if task.BoxName != box && task.LogicalBoxID != box {
		return fmt.Errorf("task %q belongs to logical box %q, not %q", task.ID, task.BoxName, box)
	}
	if asJSON {
		return json.NewEncoder(a.Out).Encode(task)
	}
	return a.processTaskSummary(task)
}

func (a *App) processTaskSummary(task v1.ProcessTask) error {
	exit := ""
	if task.ExitCode != nil {
		exit = fmt.Sprintf(" · exit %d", *task.ExitCode)
	}
	_, err := fmt.Fprintf(a.Out, "%s · %s · %s%s\n", tuiLabel(task.ID, 100), tuiLabel(task.Agent, 30), tuiLabel(task.State, 40), exit)
	return err
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
			if opts.agent != "" {
				return opts, fmt.Errorf("agent specified twice; use task BOX AGENT --prompt TEXT")
			}
			opts.agent, err = next()
		case "--idempotency-key":
			opts.idempotency, err = next()
		case "--session":
			opts.session, err = next()
		case "--prompt":
			opts.prompt, err = next()
		case "--model":
			opts.model, err = next()
		case "--arg":
			var value string
			value, err = next()
			opts.args = append(opts.args, value)
		case "--json":
			opts.jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown task option %q", arg)
			}
			if opts.box == "" {
				opts.box = arg
			} else if opts.agent == "" {
				opts.agent = arg
			} else {
				return opts, fmt.Errorf("usage: task BOX codex|claude|shell --prompt TEXT")
			}
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func (a *App) promptTaskBox(ctx context.Context, c config.Context, token string) (string, error) {
	var boxes []v1.LogicalBox
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/logical-boxes"+fleetQuery(c), nil, &boxes, nil); err != nil {
		return "", err
	}
	if len(boxes) == 0 {
		return "", fmt.Errorf("no logical boxes are available; create one with 'vmbox new NAME'")
	}
	labels := make([]string, len(boxes))
	for index, box := range boxes {
		labels[index] = fmt.Sprintf("%s (%s)", box.Name, box.State)
	}
	choice, err := a.selectTUI(ctx, "Select a logical box", labels, 0)
	if err != nil {
		return "", err
	}
	return boxes[choice].Name, nil
}

func (a *App) promptTaskAgent(ctx context.Context) (string, error) {
	agents := []string{"codex", "claude", "shell"}
	choice, err := a.selectTUI(ctx, "Choose an agent", []string{"Codex", "Claude", "Shell command"}, 0)
	if err != nil {
		return "", err
	}
	return agents[choice], nil
}

func validTaskAgent(value string) bool {
	switch strings.ToLower(value) {
	case "codex", "claude", "opencode", "shell":
		return true
	default:
		return false
	}
}

package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"golang.org/x/term"
)

type App struct {
	In                   io.Reader
	Out, Err             io.Writer
	Environ              map[string]string
	HTTP                 *http.Client
	ConfigPath           string
	WorkingDir           string
	ProgressInterval     time.Duration
	ExitPromptTimeout    time.Duration
	Runner               procexec.Runner
	IsTerminal           func() bool
	Verbose              bool
	creationProgress     func(string)
	authReplacements     map[string]string
	authPrompt           func(string) (string, error)
	openCodeAPIProviders []openCodeAPIProvider
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func New() *App {
	a := &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Environ: envMap(), HTTP: &http.Client{Timeout: 75 * time.Second}, ProgressInterval: 10 * time.Second, Runner: procexec.OSRunner{}}
	a.IsTerminal = func() bool { return charDevice(a.In) && charDevice(a.Out) }
	return a
}

func charDevice(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func makeRaw(reader io.Reader) (func(), error) {
	file, ok := reader.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(int(file.Fd()), state) }, nil
}
func envMap() map[string]string {
	result := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func (a *App) Run(ctx context.Context, args []string) error {
	for len(args) > 0 {
		switch args[0] {
		case "--verbose":
			a.Verbose = true
			args = args[1:]
		case "--standalone":
			return fmt.Errorf("standalone mode has been removed; connect to a controller with vbox connect URL")
		case "--context":
			return fmt.Errorf("--context has been removed; this CLI connects to one controller; use vbox connect URL")
		default:
			goto parsed
		}
	}
parsed:
	if len(args) == 0 {
		return a.overview(ctx)
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) == 2 && args[1] == "--all" {
			a.usageFull()
		} else {
			a.usage()
		}
		return nil
	}
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	if args[0] == "context" {
		return fmt.Errorf("context commands have been removed; use vbox connect URL")
	}
	if args[0] == "connect" {
		if len(args) == 1 {
			connected, err := file.Connected()
			if err != nil {
				return err
			}
			if err := validateControllerURL(connected.Controller); err != nil {
				return err
			}
			fmt.Fprintln(a.Out, connected.Controller)
			return nil
		}
		fs := flag.NewFlagSet("connect", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		previous, _ := file.Connected()
		tokenEnv := "VMBOX_CONTROLLER_TOKEN"
		if strings.TrimRight(previous.Controller, "/") == strings.TrimRight(args[1], "/") && previous.TokenEnv != "" {
			tokenEnv = previous.TokenEnv
		}
		fs.StringVar(&tokenEnv, "token-env", tokenEnv, "controller token environment variable")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("usage: vbox connect URL [--token-env ENV]")
		}
		connected, err := a.setControllerConnection(&file, args[1], tokenEnv)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Connected to %s\n", connected.Controller)
		return nil
	}
	active, err := file.Connected()
	if err != nil {
		file, active, err = a.promptControllerConnection(file)
		if err != nil {
			return err
		}
	}
	if args[0] == "controller" {
		return fmt.Errorf("controller bootstrap is operator-only; see docs/CONTROLLER-FIRST.md; no provider operation performed")
	}
	if active.Controller == "" {
		return fmt.Errorf("controller is not configured; run vbox connect URL")
	}
	if err := validateControllerURL(active.Controller); err != nil {
		return err
	}
	if active.TokenEnv == "" {
		active.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	if args[0] == "logout" {
		if len(args) != 1 {
			return fmt.Errorf("usage: vbox logout")
		}
		return a.controllerLogout(active)
	}
	if a.Verbose {
		fmt.Fprintf(a.Err, "vbox: controller %s\n", active.Controller)
	}
	return a.controller(ctx, file, active, args)
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}

func splitRun(args []string) (name string, detach, reuse bool, argv []string, err error) {
	opts, err := parseRunOptions(args)
	return opts.name, opts.detach, opts.reuse, opts.argv, err
}

func (a *App) controller(ctx context.Context, file config.File, c config.Context, args []string) error {
	token, err := a.controllerToken(ctx, c)
	if err != nil {
		return err
	}
	// Worker pool selection comes from controller state, never a saved CLI context.
	needsDefault := !hasWorkerPoolOption(args) && (args[0] == "new" || args[0] == "create" || args[0] == "fleet" || args[0] == "run" || ((args[0] == "boxes" || args[0] == "box") && len(args) > 1 && (args[1] == "new" || args[1] == "create")))
	creationDialog := (args[0] == "new" || args[0] == "create" || ((args[0] == "boxes" || args[0] == "box") && len(args) > 1 && (args[1] == "new" || args[1] == "create"))) && a.IsTerminal != nil && a.IsTerminal()
	for _, arg := range args {
		if arg == "--no-dialog" || arg == "--json" {
			creationDialog = false
		}
	}
	if creationDialog {
		needsDefault = false
	}
	if needsDefault {
		var def v1.FleetConfig
		if status, err := a.request(ctx, c, token, http.MethodGet, "/v1/controller-defaults", nil, &def, nil); err != nil {
			if status != http.StatusConflict || a.IsTerminal == nil || !a.IsTerminal() {
				return err
			}
			def, err = a.chooseDefaultProvider(ctx, c, token)
			if err != nil {
				return err
			}
		}
		c.Provider = def.Provider
		c.ProviderCredential = def.ProviderCredential
	}
	switch args[0] {
	case "whoami":
		return a.controllerWhoami(ctx, c, token, args[1:])
	case "desktop":
		return a.controllerDesktop(ctx, c, token, args[1:])
	case "fleet":
		return a.controllerFleet(ctx, c, token, args[1:])
	case "boxes", "box":
		return a.controllerBoxes(ctx, c, token, args[1:])
	case "auth":
		return a.controllerLogicalBoxAuth(ctx, c, token, args[1:])
	case "task":
		return a.controllerTask(ctx, c, token, args[1:])
	case "task-status":
		return a.controllerTaskStatus(ctx, c, token, args[1:])
	case "task-output":
		return a.processOutput(ctx, c, token, args[1:])
	case "sessions":
		return a.controllerSessions(ctx, c, token, args[1:])
	case "updates":
		return a.controllerUpdates(ctx, c, token, args[1:])
	case "pools", "providers":
		return a.controllerProviders(ctx, c, token, args[1:])
	case "profiles":
		return a.controllerLoginProfiles(ctx, c, token, args[1:])
	case "coworkers":
		return fmt.Errorf("coworker features have been removed; use vbox BOX or vbox task BOX")
	case "allocate":
		return a.controllerBoxes(ctx, c, token, append([]string{"allocate"}, args[1:]...))
	case "hibernate":
		return a.controllerBoxes(ctx, c, token, append([]string{"hibernate"}, args[1:]...))
	case "delete", "delete-volume":
		return a.controllerBoxes(ctx, c, token, append([]string{"delete"}, args[1:]...))
	case "new", "create":
		return a.controllerBoxes(ctx, c, token, append([]string{"new"}, args[1:]...))
	case "run":
		opts, err := parseRunOptions(args[1:])
		if err != nil {
			return err
		}
		if opts.pool != "" {
			c.Provider, c.ProviderCredential, err = parseWorkerPool(opts.pool)
			if err != nil {
				return err
			}
		}
		workingDirectory, err := a.workingDirectory()
		if err != nil {
			return err
		}
		setup := defaultSetup(c)
		if opts.reuse {
			setup, err = loadSetup(file, c.Controller, workingDirectory)
			if err != nil {
				return err
			}
		}
		applyRunOptions(&setup, opts)
		if !opts.reuse && a.IsTerminal != nil && a.IsTerminal() {
			previewArgv := opts.argv
			if len(previewArgv) == 0 {
				previewArgv = defaultSession(true)
			}
			setup, err = a.configureSetup(ctx, c, nil, opts.name, setup, previewArgv)
			if errors.Is(err, errSetupCancelled) {
				fmt.Fprintln(a.Err, "vbox: setup cancelled; nothing was submitted")
				return nil
			}
			if err != nil {
				return err
			}
		}
		prepared, err := a.prepareSetup(ctx, setup)
		if err != nil {
			return err
		}
		if len(prepared.setup.ApplicationProfiles) > 0 || prepared.setup.GitHub != nil || len(prepared.setup.Instructions) > 0 {
			return fmt.Errorf("local application profiles, GitHub credentials, and Markdown instructions require standalone creation; nothing was submitted")
		}
		if len(opts.argv) == 0 {
			opts.argv = defaultSession(true)
		}
		lifecycle := v1.LifecyclePolicy{OnSuccess: v1.LifecycleAction(prepared.setup.OnSuccess), OnFailure: v1.LifecycleAction(prepared.setup.OnFailure), MaxTTLText: prepared.setup.MaxTTL}
		req := v1.CreateRunRequest{Provider: c.Provider, ProviderCredential: c.ProviderCredential, Box: opts.name, Image: c.Image, Region: prepared.setup.Region, Command: opts.argv, Resources: prepared.setup.Resources, Components: prepared.setup.Components, NotificationPolicy: prepared.setup.NotificationPolicy, Lifecycle: lifecycle}
		var run v1.Run
		status, err := a.request(ctx, c, token, http.MethodPost, "/v1/runs", req, &run, map[string]string{"Idempotency-Key": "cli-" + opts.name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "accepted %s (%d)\n", run.ID, status)
		if prepared.setup.Save {
			return saveSetup(a.ConfigPath, file, c.Controller, workingDirectory, prepared.setup)
		}
		return nil
	case "status":
		if len(args) != 2 && !(len(args) == 3 && args[2] == "--json") {
			return fmt.Errorf("status requires a logical box name or ID")
		}
		return a.controllerBoxes(ctx, c, token, append([]string{"status"}, args[1:]...))
	case "ls", "list":
		return a.controllerBoxes(ctx, c, token, append([]string{"list"}, args[1:]...))
	case "stop", "start":
		if len(args) != 2 {
			return fmt.Errorf("%s requires a run ID", args[0])
		}
		var box provider.Box
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/runs/"+args[1]+"/"+args[0], map[string]any{}, &box, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "resize":
		runID := ""
		var err error
		flagArgs := args[1:]
		if len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
			runID = flagArgs[0]
			flagArgs = flagArgs[1:]
		} else {
			runID, err = a.selectControllerRun(ctx, c, token, "Select a run to resize")
			if err != nil {
				return err
			}
		}
		fs := flag.NewFlagSet("resize", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		cpu := fs.Float64("cpu", 0, "CPU limit")
		memory := fs.Int64("memory", 0, "memory MiB")
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		resources := provider.Resources{CPU: *cpu, MemoryMiB: *memory}
		if resources.CPU == 0 && resources.MemoryMiB == 0 {
			resources, err = a.selectResizeResources("Choose new limits for " + runID)
			if err != nil {
				return err
			}
		}
		var box provider.Box
		_, err = a.request(ctx, c, token, http.MethodPost, "/v1/runs/"+runID+"/resize", resources, &box, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "resume":
		if len(args) != 1 {
			return fmt.Errorf("bare 'vbox resume' opens logical-box selection; use 'vbox NAME' for a named box")
		}
		name, err := a.selectControllerLogicalBox(ctx, c, token, "Select a logical box to resume")
		if err != nil {
			return err
		}
		return a.controllerBoxes(ctx, c, token, []string{"open", name})
	case "cost":
		if len(args) != 2 {
			return fmt.Errorf("cost requires a run ID")
		}
		var usage provider.Usage
		_, err := a.request(ctx, c, token, http.MethodGet, "/v1/runs/"+args[1]+"/usage", nil, &usage, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(usage)
	case "logs":
		if len(args) < 2 {
			return fmt.Errorf("logs requires a run ID")
		}
		query := "?tail=100"
		for _, arg := range args[2:] {
			if arg == "--follow" {
				query += "&follow=true"
			} else if strings.HasPrefix(arg, "--tail=") {
				query = "?tail=" + strings.TrimPrefix(arg, "--tail=")
			} else {
				return fmt.Errorf("unknown logs option %q", arg)
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Controller, "/")+"/v1/runs/"+args[1]+"/logs"+query, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := a.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("controller unavailable (no standalone fallback): %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("controller logs: %s", resp.Status)
		}
		_, err = io.Copy(a.Out, resp.Body)
		return err
	case "clean":
		if len(args) != 3 || args[2] != "--yes" {
			return fmt.Errorf("clean requires RUN_ID --yes")
		}
		_, err := a.request(ctx, c, token, http.MethodDelete, "/v1/runs/"+args[1], nil, nil, nil)
		return err
	case "questions":
		var values []v1.Question
		_, err := a.request(ctx, c, token, http.MethodGet, "/v1/questions", nil, &values, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(values)
	case "answer":
		if len(args) < 3 {
			return fmt.Errorf("answer requires QUESTION_ID TEXT")
		}
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/questions/"+args[1]+"/answer", v1.AnswerRequest{Answer: strings.Join(args[2:], " ")}, nil, nil)
		return err
	case "users":
		if len(args) < 2 {
			return fmt.Errorf("users requires list, add, or remove")
		}
		switch args[1] {
		case "list":
			var users []v1.User
			_, err := a.request(ctx, c, token, http.MethodGet, "/v1/users", nil, &users, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(users)
		case "add":
			if len(args) < 3 {
				return fmt.Errorf("users add requires SUBJECT [--role owner|user]")
			}
			fs := flag.NewFlagSet("users add", flag.ContinueOnError)
			fs.SetOutput(a.Err)
			role := fs.String("role", "user", "owner or user")
			if err := fs.Parse(args[3:]); err != nil {
				return err
			}
			var created v1.CreatedUser
			_, err := a.request(ctx, c, token, http.MethodPost, "/v1/users", v1.CreateUserRequest{Subject: args[2], Role: *role}, &created, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(created)
		case "remove":
			if len(args) != 3 {
				return fmt.Errorf("users remove requires USER_ID")
			}
			_, err := a.request(ctx, c, token, http.MethodDelete, "/v1/users/"+url.PathEscape(args[2]), nil, nil, nil)
			return err
		default:
			return fmt.Errorf("unknown users command %q", args[1])
		}
	case "credentials":
		if len(args) < 2 {
			return fmt.Errorf("credentials requires list, set, or remove")
		}
		if args[1] == "list" {
			var values []v1.ProviderCredential
			_, err := a.request(ctx, c, token, http.MethodGet, "/v1/provider-credentials", nil, &values, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(values)
		}
		if len(args) < 4 {
			return fmt.Errorf("credentials %s requires TYPE ALIAS", args[1])
		}
		path := "/v1/provider-credentials/" + url.PathEscape(args[2]) + "/" + url.PathEscape(args[3])
		switch args[1] {
		case "set":
			fs := flag.NewFlagSet("credentials set", flag.ContinueOnError)
			fs.SetOutput(a.Err)
			secretEnv := fs.String("secret-env", "", "environment variable containing a JSON secret object")
			configText := fs.String("config", "{}", "non-secret provider config JSON")
			if err := fs.Parse(args[4:]); err != nil {
				return err
			}
			if *secretEnv == "" || a.Environ[*secretEnv] == "" {
				return fmt.Errorf("--secret-env must name a non-empty environment variable")
			}
			req := v1.PutProviderCredentialRequest{Secret: json.RawMessage(a.Environ[*secretEnv]), Config: json.RawMessage(*configText)}
			var value v1.ProviderCredential
			_, err := a.request(ctx, c, token, http.MethodPut, path, req, &value, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(value)
		case "remove":
			_, err := a.request(ctx, c, token, http.MethodDelete, path, nil, nil, nil)
			return err
		default:
			return fmt.Errorf("unknown credentials command %q", args[1])
		}
	case "notifications":
		if len(args) < 2 {
			return fmt.Errorf("notifications requires list, setup, test, or remove")
		}
		if args[1] == "list" {
			var values []v1.NotificationDestination
			_, err := a.request(ctx, c, token, http.MethodGet, "/v1/notifications", nil, &values, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(values)
		}
		if len(args) < 4 {
			return fmt.Errorf("notifications %s requires KIND NAME", args[1])
		}
		path := "/v1/notifications/" + url.PathEscape(args[2]) + "/" + url.PathEscape(args[3])
		switch args[1] {
		case "setup":
			fs := flag.NewFlagSet("notifications setup", flag.ContinueOnError)
			fs.SetOutput(a.Err)
			secretEnv := fs.String("secret-env", "", "environment variable containing a JSON secret object")
			configText := fs.String("config", "{}", "non-secret destination config JSON")
			var users, chats stringList
			fs.Var(&users, "allow-user", "allowed Discord user ID (repeatable)")
			fs.Var(&chats, "allow-chat", "allowed chat, channel, or guild ID (repeatable)")
			if err := fs.Parse(args[4:]); err != nil {
				return err
			}
			if *secretEnv == "" || a.Environ[*secretEnv] == "" {
				return fmt.Errorf("--secret-env must name a non-empty environment variable")
			}
			req := v1.PutNotificationRequest{Secret: json.RawMessage(a.Environ[*secretEnv]), Config: json.RawMessage(*configText), AllowedUsers: users, AllowedChats: chats}
			var value v1.NotificationDestination
			_, err := a.request(ctx, c, token, http.MethodPut, path, req, &value, nil)
			if err != nil {
				return err
			}
			return json.NewEncoder(a.Out).Encode(value)
		case "test":
			_, err := a.request(ctx, c, token, http.MethodPost, path+"/test", map[string]any{}, nil, nil)
			return err
		case "remove":
			_, err := a.request(ctx, c, token, http.MethodDelete, path, nil, nil, nil)
			return err
		default:
			return fmt.Errorf("unknown notifications command %q", args[1])
		}
	default:
		interactiveOverride := len(args) == 2 && (args[1] == "codex" || args[1] == "claude" || args[1] == "shell" || args[1] == "--session")
		if strings.HasPrefix(args[0], "-") || (len(args) != 1 && !interactiveOverride && !(len(args) == 3 && (args[1] == "--session" || args[1] == "--start-cli"))) {
			return fmt.Errorf("unknown controller command %q", args[0])
		}
		return a.controllerBoxes(ctx, c, token, append([]string{"open"}, args...))
	}
}
func (a *App) request(ctx context.Context, c config.Context, token, method, path string, input, output any, headers map[string]string) (int, error) {
	key := c.Controller + "\x00" + token
	if replacement, ok := a.authReplacements[key]; ok {
		token = replacement
	}
	status, err := a.requestOnce(ctx, c, token, method, path, input, output, headers)
	if status != http.StatusUnauthorized {
		return status, err
	}
	if a.IsTerminal == nil || !a.IsTerminal() {
		return status, fmt.Errorf("controller authentication rejected; set %s to a valid token or run vbox in a terminal to sign in", c.TokenEnv)
	}
	if token == a.Environ[c.TokenEnv] && token != "" {
		saved, readErr := a.savedControllerToken(c)
		if readErr != nil {
			return status, readErr
		}
		if saved != "" && saved != token {
			status, err = a.requestOnce(ctx, c, saved, method, path, input, output, headers)
			if status != http.StatusUnauthorized {
				if status >= 200 && status < 300 {
					if a.authReplacements == nil {
						a.authReplacements = make(map[string]string)
					}
					a.authReplacements[key] = saved
				}
				return status, err
			}
		}
	}
	if _, attempted := a.authReplacements[key]; attempted {
		return status, fmt.Errorf("controller token rejected; retry with a valid controller token")
	}
	fmt.Fprintln(a.Err, "Controller token rejected. Enter your current controller token.")
	prompt := a.readSecret
	if a.authPrompt != nil {
		prompt = a.authPrompt
	}
	replacement, promptErr := prompt("Controller token")
	if promptErr != nil {
		return status, promptErr
	}
	if a.authReplacements == nil {
		a.authReplacements = make(map[string]string)
	}
	a.authReplacements[key] = replacement
	status, err = a.requestOnce(ctx, c, replacement, method, path, input, output, headers)
	if status == http.StatusUnauthorized {
		return status, fmt.Errorf("controller token rejected; retry with a valid controller token")
	}
	if status >= 200 && status < 300 {
		if saveErr := a.saveControllerToken(c, replacement); saveErr != nil {
			return status, saveErr
		}
	}
	return status, err
}

func (a *App) requestOnce(ctx context.Context, c config.Context, token, method, path string, input, output any, headers map[string]string) (int, error) {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Controller, "/")+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, &APIError{Status: 0, Message: fmt.Sprintf("controller unavailable (no standalone fallback): %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr v1.Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return resp.StatusCode, &APIError{Status: resp.StatusCode, Message: apiErr.Error}
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
func (a *App) usage() {
	fmt.Fprint(a.Out, `vbox — persistent remote boxes

  vbox                         Show controller and box states (read-only)
  vbox BOX                     Open its shell; wake it if needed
  vbox desktop BOX             Open its desktop in a local VNC viewer
  vbox new NAME                Configure, create and connect
  vbox ls                      List boxes
  vbox whoami                  Show controller account, user and role
  vbox logout                  Clear this context's saved controller token
  vbox profiles upload         Upload local logins without creating a box
  vbox status BOX              Show its current state
  vbox hibernate BOX           Release compute; keep the workspace
  vbox delete BOX              Permanently delete box and files (confirmation required)

  vbox BOX --session           Choose another existing tmux session
  vbox BOX --start-cli 'claude' Start a command in a new persistent shell
  vbox task BOX                Choose Codex, Claude or a shell one-shot
  vbox task BOX codex --prompt 'YOUR TASK'
  vbox task-status BOX         Read task results and exit codes

Inside a box, run claude, codex, or any shell command yourself.
Detach: Ctrl-a, then d. Choose Leave unchanged to leave programs alive.
Hibernation retains files, not live processes. One-shots hibernate when idle.

Setup: vbox connect URL | vbox pools | vbox profiles list
Full reference: vbox help --all    Diagnostics: vbox --verbose COMMAND
`)
}

func (a *App) usageFull() {
	fmt.Fprint(a.Out, `vbox — controller-managed persistent boxes
  vbox BOX [codex|claude|shell | --session [NAME] | --start-cli COMMAND]
  vbox ls [--json] | status BOX [--json] | sessions BOX [--json]
  vbox new BOX [--pool TYPE/ALIAS] [--disk GiB] [--region ID] [--detach|--hibernate] [--no-dialog] [--start-cli COMMAND]
  vbox task [BOX] [codex|claude|shell] [--prompt TEXT]
            [--session NAME] [--idempotency-key KEY] [--json]
  vbox task-status BOX [TASK_ID]
  vbox task-output BOX TASK_ID
  vbox updates [BOX] [--json]
  vbox updates ack BOX --session NAME --revision REV
  vbox boxes update BOX --default-agent AGENT
  vbox pools list|schema|show|create|update|validate|default|worker
  vbox fleet status|slots|slots set COUNT [--pool TYPE/ALIAS]
  vbox allocate|hibernate|delete BOX
  vbox connect URL [--token-env ENV]
  vbox connect | logout | whoami
  vbox users list|add|remove
  vbox notifications list|setup|test|remove
  vbox auth BOX [authentication options]
  vbox run BOX [--pool TYPE/ALIAS] [job options] -- COMMAND [ARG...]

Controller login does not provision SSH identity. Configure your SSH agent or
VMBOX_SSH_IDENTITY_FILE; optionally VMBOX_SSH_KNOWN_HOSTS_FILE. Changed host keys
fail closed. Native attachment requires the account owner role and a terminal.
Exact session selection never creates or replaces a session. Detach with Ctrl-a d.
BOX reconnects to its remembered primary. --session opens a full-screen picker;
--session NAME selects directly. Your selection is remembered by the controller.
Updates are bounded on-demand snapshots, not agent completion. Reads never ack.
One-shot tasks retain output/exit codes and hibernate only when no sibling work
remains. Exit code 0 is process success, not proof the prompt was completed.
Standalone and client-side controller provisioning have been removed.
`)
}

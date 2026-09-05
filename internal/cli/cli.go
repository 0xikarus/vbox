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
	"sort"
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
	In                io.Reader
	Out, Err          io.Writer
	Environ           map[string]string
	HTTP              *http.Client
	ConfigPath        string
	WorkingDir        string
	ProgressInterval  time.Duration
	ExitPromptTimeout time.Duration
	Runner            procexec.Runner
	IsTerminal        func() bool
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
	contextName := ""
	for len(args) > 0 {
		switch args[0] {
		case "--standalone":
			return fmt.Errorf("standalone mode has been removed; configure a controller context; existing standalone resources are untouched")
		case "--context":
			if len(args) < 2 {
				return fmt.Errorf("--context requires a name")
			}
			contextName = args[1]
			args = args[2:]
		default:
			goto parsed
		}
	}
parsed:
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		a.usage()
		return nil
	}
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	if args[0] == "context" {
		return a.context(file, args[1:])
	}
	active, err := file.Active(contextName)
	if err != nil {
		file, active, err = a.promptControllerContext(file, contextName, config.Context{})
		if err != nil {
			return err
		}
	}
	if args[0] == "controller" {
		return fmt.Errorf("controller bootstrap is operator-only; see docs/CONTROLLER-FIRST.md; no provider operation performed")
	}
	if active.Controller == "" {
		return fmt.Errorf("provider-only context cannot be used; create a controller context; no standalone resources changed")
	}
	if err := validateControllerURL(active.Controller); err != nil {
		return err
	}
	if active.TokenEnv == "" {
		active.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	fmt.Fprintf(a.Err, "vmbox: controller · context %s\n", active.Name)
	return a.controller(ctx, file, active, args)
}

func (a *App) context(file config.File, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vmbox context add|use|list")
	}
	switch args[0] {
	case "list":
		names := make([]string, 0, len(file.Contexts))
		for name := range file.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			ctx := file.Contexts[name]
			marker := " "
			if file.Current == name {
				marker = "*"
			}
			fmt.Fprintf(a.Out, "%s %-20s %-8s %s\n", marker, name, ctx.Provider, ctx.Controller)
		}
		return nil
	case "use":
		if len(args) != 2 {
			return fmt.Errorf("context use requires a name")
		}
		if _, ok := file.Contexts[args[1]]; !ok {
			return fmt.Errorf("context %q does not exist", args[1])
		}
		file.Current = args[1]
		return config.Save(a.ConfigPath, file)
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("context add requires a name")
		}
		name := args[1]
		if _, exists := file.Contexts[name]; exists {
			return fmt.Errorf("context already exists; use a new name after explicitly configuring its controller default; old context preserved")
		}
		fs := flag.NewFlagSet("context add", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		ctx := config.Context{Name: name, TokenEnv: "VMBOX_CONTROLLER_TOKEN"}
		fs.StringVar(&ctx.Controller, "controller", "", "controller HTTPS URL")
		fs.StringVar(&ctx.TokenEnv, "token-env", "VMBOX_CONTROLLER_TOKEN", "controller token environment variable")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected context argument")
		}
		if err := validateControllerURL(ctx.Controller); err != nil {
			return err
		}
		file.Contexts[name] = ctx
		if file.Current == "" {
			file.Current = name
		}
		return config.Save(a.ConfigPath, file)
	default:
		return fmt.Errorf("unknown context command %q", args[0])
	}
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
	token := a.Environ[c.TokenEnv]
	if token == "" {
		return fmt.Errorf("controller token environment %s is empty; refusing standalone fallback", c.TokenEnv)
	}
	// Provider selection comes from controller state, not client SDKs or local
	// provisioning credentials. Legacy selectors must match before mutation.
	needsDefault := args[0] == "new" || args[0] == "create" || args[0] == "fleet" || args[0] == "run" || ((args[0] == "boxes" || args[0] == "box") && len(args) > 1 && (args[1] == "new" || args[1] == "create"))
	if needsDefault {
		var def v1.FleetConfig
		if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/controller-defaults", nil, &def, nil); err != nil {
			return err
		}
		if (c.Provider != "" && c.Provider != def.Provider) || (c.ProviderCredential != "" && c.ProviderCredential != def.ProviderCredential) {
			return fmt.Errorf("legacy context provider selection differs from controller default; migrate explicitly before proceeding")
		}
		c.Provider = def.Provider
		c.ProviderCredential = def.ProviderCredential
	}
	switch args[0] {
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
	case "sessions":
		return a.controllerSessions(ctx, c, token, args[1:])
	case "updates":
		return a.controllerUpdates(ctx, c, token, args[1:])
	case "providers":
		return a.controllerProviders(ctx, c, token, args[1:])
	case "allocate":
		return a.controllerBoxes(ctx, c, token, append([]string{"allocate"}, args[1:]...))
	case "hibernate":
		return a.controllerBoxes(ctx, c, token, append([]string{"hibernate"}, args[1:]...))
	case "delete-volume":
		return a.controllerBoxes(ctx, c, token, append([]string{"delete-volume"}, args[1:]...))
	case "new", "create":
		return a.controllerBoxes(ctx, c, token, append([]string{"new"}, args[1:]...))
	case "run":
		opts, err := parseRunOptions(args[1:])
		if err != nil {
			return err
		}
		workingDirectory, err := a.workingDirectory()
		if err != nil {
			return err
		}
		setup := defaultSetup(c)
		if opts.reuse {
			setup, err = loadSetup(file, c.Name, workingDirectory)
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
				fmt.Fprintln(a.Err, "vmbox: setup cancelled; nothing was submitted")
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
			return saveSetup(a.ConfigPath, file, c.Name, workingDirectory, prepared.setup)
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
			return fmt.Errorf("bare 'vmbox resume' opens logical-box selection; use 'vmbox NAME' for a named box")
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
			return fmt.Errorf("credentials %s requires PROVIDER NAME", args[1])
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
			fs.Var(&users, "allow-user", "allowed Telegram/Discord user ID (repeatable)")
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
		if strings.HasPrefix(args[0], "-") || (len(args) != 1 && !(len(args) == 3 && args[1] == "--session")) {
			return fmt.Errorf("unknown controller command %q", args[0])
		}
		return a.controllerBoxes(ctx, c, token, append([]string{"open"}, args...))
	}
}
func (a *App) request(ctx context.Context, c config.Context, token, method, path string, input, output any, headers map[string]string) (int, error) {
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
	fmt.Fprint(a.Out, `vmbox — controller-managed persistent boxes
  vmbox [--context NAME] BOX [--session NAME]
  vmbox ls [--json] | status BOX [--json] | sessions BOX [--json]
  vmbox new BOX [--disk GiB] [--region ID] [--allocate|--detach]
  vmbox task BOX --agent claude|codex|opencode|shell --prompt TEXT
             [--session NAME] [--idempotency-key KEY] [--json]
  vmbox task-status BOX [TASK_ID]
  vmbox updates [BOX] [--json]
  vmbox updates ack BOX --session NAME --revision REV
  vmbox boxes update BOX --default-agent AGENT
  vmbox providers list|schema|show|create|update|validate|default
  vmbox fleet status|slots|slots set COUNT
  vmbox allocate|hibernate|delete-volume BOX
  vmbox context add NAME --controller URL [--token-env ENV]
  vmbox context use|list
  vmbox users list|add|remove
  vmbox notifications list|setup|test|remove
  vmbox auth BOX [authentication options]
  vmbox run BOX [job options] -- COMMAND [ARG...]

Controller login does not provision SSH identity. Configure your SSH agent or
VMBOX_SSH_IDENTITY_FILE; optionally VMBOX_SSH_KNOWN_HOSTS_FILE. Changed host keys
fail closed. Native attachment requires the account owner role and a terminal.
Exact session selection never creates or replaces a session. Detach with Ctrl-a d.
Updates are bounded on-demand snapshots, not agent completion. Reads never ack.
Standalone and client-side controller provisioning have been removed.
`)
}

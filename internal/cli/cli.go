package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	incusprovider "github.com/0xikarus/vmbox-service/internal/provider/incus"
	railwayprovider "github.com/0xikarus/vmbox-service/internal/provider/railway"
	"golang.org/x/term"
)

type App struct {
	In         io.Reader
	Out, Err   io.Writer
	Environ    map[string]string
	HTTP       *http.Client
	ConfigPath string
	Runner     procexec.Runner
	IsTerminal func() bool
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func New() *App {
	a := &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Environ: envMap(), HTTP: &http.Client{Timeout: 75 * time.Second}, Runner: procexec.OSRunner{}}
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
	standalone := false
	contextName := ""
	for len(args) > 0 {
		switch args[0] {
		case "--standalone":
			standalone = true
			args = args[1:]
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
	if len(args) == 0 {
		a.usage()
		return nil
	}
	file, err := config.Load(a.ConfigPath)
	if err != nil {
		return err
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		a.usage()
		return nil
	}
	if args[0] == "context" {
		return a.context(file, args[1:])
	}
	active, err := file.Active(contextName)
	if err != nil {
		return err
	}
	if args[0] == "controller" {
		return a.provisionController(ctx, file, active, args[1:])
	}
	mode := "standalone"
	if active.Controller != "" && !standalone {
		mode = "controller"
	}
	fmt.Fprintf(a.Err, "vmbox: mode=%s account=%s context=%s provider=%s\n", mode, active.Account, active.Name, active.Provider)
	if args[0] == "provider" {
		if len(args) != 2 || args[1] != "validate" {
			return fmt.Errorf("usage: vmbox provider validate")
		}
		p, err := a.provider(active)
		if err != nil {
			return err
		}
		cap, err := p.Validate(ctx)
		if encodeErr := json.NewEncoder(a.Out).Encode(cap); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	if mode == "controller" {
		return a.controller(ctx, file, active, args)
	}
	p, err := a.provider(active)
	if err != nil {
		return err
	}
	return a.standalone(ctx, file, p, active, args)
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
		fs := flag.NewFlagSet("context add", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		ctx := config.Context{Name: name, TokenEnv: "VMBOX_CONTROLLER_TOKEN"}
		fs.StringVar(&ctx.Provider, "provider", "", "provider name")
		fs.StringVar(&ctx.Controller, "controller", "", "controller HTTPS URL")
		fs.StringVar(&ctx.Account, "account", "", "controller account label")
		fs.StringVar(&ctx.Project, "project", "", "project ID")
		fs.StringVar(&ctx.Environment, "environment", "", "environment ID")
		fs.BoolVar(&ctx.RailwayCLIAuth, "railway-cli-auth", false, "use the local Railway CLI login when no token environment is set")
		fs.StringVar(&ctx.Cluster, "cluster", "", "cluster ID")
		fs.StringVar(&ctx.Image, "image", "", "default OCI image")
		fs.StringVar(&ctx.DockerContext, "docker-context", "", "Docker context")
		fs.StringVar(&ctx.DockerHost, "docker-host", "", "Docker host")
		fs.BoolVar(&ctx.DockerTLSVerify, "docker-tls-verify", false, "require Docker mTLS")
		fs.StringVar(&ctx.DockerCertPath, "docker-cert-path", "", "Docker certificate directory")
		fs.StringVar(&ctx.IncusRemote, "incus-remote", "", "Incus remote")
		fs.StringVar(&ctx.IncusProject, "incus-project", "", "Incus project")
		fs.BoolVar(&ctx.IncusVM, "incus-vm", false, "use QEMU VMs")
		fs.StringVar(&ctx.ProviderCredential, "provider-credential", "", "controller provider credential name")
		fs.StringVar(&ctx.TokenEnv, "token-env", "VMBOX_CONTROLLER_TOKEN", "environment variable containing controller token")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if ctx.Provider == "" {
			return fmt.Errorf("--provider is required")
		}
		switch ctx.Provider {
		case "docker", "incus", "railway":
		default:
			return fmt.Errorf("unsupported provider %q (available: docker, incus, railway)", ctx.Provider)
		}
		if ctx.DockerHost != "" && strings.HasPrefix(ctx.DockerHost, "tcp://") && !ctx.DockerTLSVerify {
			return fmt.Errorf("unauthenticated Docker TCP is forbidden")
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

func (a *App) provisionController(ctx context.Context, file config.File, c config.Context, args []string) error {
	if len(args) == 0 || (args[0] != "init" && args[0] != "ensure") {
		return fmt.Errorf("usage: vmbox controller init|ensure --endpoint HTTPS_URL [--yes]")
	}
	operation := args[0]
	fs := flag.NewFlagSet("controller "+operation, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	endpoint := fs.String("endpoint", a.Environ["VMBOX_CONTROLLER_URL"], "public controller HTTPS endpoint")
	account := fs.String("account", "default", "initial account name")
	owner := fs.String("owner", "owner", "initial owner subject")
	yes := fs.Bool("yes", false, "confirm billable provisioning")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if operation == "ensure" && c.Controller != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Controller, "/")+"/healthz", nil)
		if err == nil {
			if resp, requestErr := a.HTTP.Do(req); requestErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					fmt.Fprintln(a.Out, "controller is healthy; no provisioning changes required")
					return nil
				}
			}
		}
	}
	if operation == "init" && c.Controller != "" {
		return fmt.Errorf("context %q already has controller %s; use controller ensure", c.Name, c.Controller)
	}
	if c.Provider != "railway" {
		return fmt.Errorf("controller provisioning currently requires a Railway context")
	}
	if c.Project == "" || c.Environment == "" || c.Image == "" || !strings.Contains(c.Image, "@sha256:") {
		return fmt.Errorf("Railway project, environment, and a digest-pinned context image are required")
	}
	if *endpoint == "" || !strings.HasPrefix(*endpoint, "https://") {
		return fmt.Errorf("--endpoint must be the controller's public HTTPS URL")
	}
	fmt.Fprintf(a.Out, "Controller plan\n  provider: Railway\n  project: %s\n  environment: %s\n  service: vmbox-controller\n  database: vmbox-postgres\n  endpoint: %s\n", c.Project, c.Environment, *endpoint)
	if !*yes {
		return fmt.Errorf("provisioning may create billable infrastructure; review the plan and rerun with --yes")
	}
	runner := a.Runner
	if _, ok := runner.(procexec.OSRunner); ok {
		token, tokenEnvironment, err := railwayToken(a.Environ)
		if err != nil {
			if !c.RailwayCLIAuth || a.Environ["RAILWAY_TOKEN"] != "" || a.Environ["RAILWAY_API_TOKEN"] != "" {
				return err
			}
			localRunner, _, runnerErr := railwayRunner("", "", a.Environ)
			if runnerErr != nil {
				return runnerErr
			}
			runner = localRunner
		} else {
			tokenRunner, _, runnerErr := railwayRunner(token, tokenEnvironment, a.Environ)
			if runnerErr != nil {
				return runnerErr
			}
			runner = tokenRunner
		}
	}
	target := []string{"--project", c.Project, "--environment", c.Environment}
	runInput := func(stdin io.Reader, argv ...string) (procexec.Result, error) {
		full := append([]string{"railway"}, argv...)
		full = append(full, target...)
		result, err := runner.Run(ctx, full, stdin, nil, nil)
		if err != nil {
			return result, err
		}
		if result.ExitCode != 0 {
			action := "Railway command"
			if len(argv) > 0 {
				action = "Railway " + argv[0]
			}
			return result, fmt.Errorf("%s failed with exit %d", action, result.ExitCode)
		}
		return result, nil
	}
	run := func(argv ...string) (procexec.Result, error) { return runInput(nil, argv...) }
	listed, err := run("service", "list", "--json")
	if err != nil {
		return err
	}
	var services []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(listed.Stdout, &services); err != nil {
		return fmt.Errorf("decode Railway services: %w", err)
	}
	hasController, hasDatabase := false, false
	for _, service := range services {
		hasController = hasController || service.Name == "vmbox-controller"
		hasDatabase = hasDatabase || service.Name == "vmbox-postgres"
	}
	if !hasDatabase {
		if _, err := run("add", "--database", "postgres", "--service", "vmbox-postgres"); err != nil {
			return err
		}
	}
	created := false
	if !hasController {
		if _, err := run("add", "--service", "vmbox-controller", "--json"); err != nil {
			return err
		}
		created = true
	}
	variables := map[string]string{
		"DATABASE_URL":         "${{vmbox-postgres.DATABASE_URL}}",
		"VMBOX_CONTROLLER_URL": strings.TrimRight(*endpoint, "/"),
		"VMBOX_IMAGE":          c.Image,
	}
	tokenText := ""
	if created {
		encryption, err := randomBytes(32)
		if err != nil {
			return err
		}
		ownerToken, err := randomBytes(32)
		if err != nil {
			return err
		}
		tokenText = base64.RawURLEncoding.EncodeToString(ownerToken)
		tokenHash := sha256.Sum256([]byte(tokenText))
		variables["VMBOX_ENCRYPTION_KEY"] = base64.RawURLEncoding.EncodeToString(encryption)
		variables["VMBOX_BOOTSTRAP_TOKEN_HASH"] = base64.RawURLEncoding.EncodeToString(tokenHash[:])
		variables["VMBOX_ACCOUNT_NAME"] = *account
		variables["VMBOX_OWNER_SUBJECT"] = *owner
	}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := runInput(strings.NewReader(variables[key]), "variable", "set", key, "--stdin", "--service", "vmbox-controller", "--skip-deploys"); err != nil {
			return err
		}
	}
	if _, err := run("service", "source", "connect", "--service", "vmbox-controller", "--image", c.Image, "--json"); err != nil {
		return err
	}
	if _, err := run("redeploy", "--service", "vmbox-controller", "--yes", "--json"); err != nil {
		return err
	}
	c.Controller = strings.TrimRight(*endpoint, "/")
	c.Account = *account
	if c.TokenEnv == "" {
		c.TokenEnv = "VMBOX_CONTROLLER_TOKEN"
	}
	file.Contexts[c.Name] = c
	if err := config.Save(a.ConfigPath, file); err != nil {
		return err
	}
	if created {
		fmt.Fprintf(a.Out, "controller provisioned; save this owner token now (shown once):\n%s=%s\n", c.TokenEnv, tokenText)
	} else {
		fmt.Fprintln(a.Out, "controller configuration ensured; existing accounts and tokens were preserved")
	}
	return nil
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}

func (a *App) provider(ctx config.Context) (provider.Provider, error) {
	switch ctx.Provider {
	case "docker":
		return dockerprovider.New(dockerprovider.Config{Context: ctx.DockerContext, Host: ctx.DockerHost, TLSVerify: ctx.DockerTLSVerify, CertPath: ctx.DockerCertPath, DefaultImage: ctx.Image}, procexec.OSRunner{}), nil
	case "railway":
		token, tokenEnvironment, err := railwayToken(a.Environ)
		if err != nil {
			if !ctx.RailwayCLIAuth || a.Environ["RAILWAY_TOKEN"] != "" || a.Environ["RAILWAY_API_TOKEN"] != "" {
				return nil, err
			}
			runner, knownHosts, runnerErr := railwayRunner("", "", a.Environ)
			if runnerErr != nil {
				return nil, runnerErr
			}
			return railwayprovider.New(railwayprovider.Config{ProjectID: ctx.Project, EnvironmentID: ctx.Environment, DefaultImage: ctx.Image, SSHKnownHostsFile: knownHosts}, runner), nil
		}
		runner, knownHosts, runnerErr := railwayRunner(token, tokenEnvironment, a.Environ)
		if runnerErr != nil {
			return nil, runnerErr
		}
		return railwayprovider.New(railwayprovider.Config{ProjectID: ctx.Project, EnvironmentID: ctx.Environment, Token: token, TokenEnvironment: tokenEnvironment, DefaultImage: ctx.Image, SSHKnownHostsFile: knownHosts}, runner), nil
	case "incus":
		return incusprovider.New(incusprovider.Config{Remote: ctx.IncusRemote, Project: ctx.IncusProject, DefaultImage: ctx.Image, VM: ctx.IncusVM}, procexec.OSRunner{}), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", ctx.Provider)
	}
}

func splitRun(args []string) (name string, detach, reuse bool, argv []string, err error) {
	opts, parseErr := parseRunOptions(args)
	return opts.name, opts.detach, opts.reuse, opts.argv, parseErr
}
func (a *App) standalone(ctx context.Context, file config.File, p provider.Provider, c config.Context, args []string) error {
	command := args[0]
	switch command {
	case "new", "create", "run":
		opts, err := parseRunOptions(args[1:])
		if err != nil {
			return err
		}
		box, inspectErr := p.Inspect(ctx, opts.name)
		created := false
		// An empty selection means "keep the components recorded inside an
		// existing box". New boxes replace it with their configured selection.
		var bootstrapComponents []string
		if opts.componentsSet {
			bootstrapComponents = append([]string(nil), opts.components...)
		}
		if inspectErr == nil {
			if opts.reuse {
				fmt.Fprintf(a.Err, "vmbox: --reuse ignored because %q already exists\n", opts.name)
			}
			if box.State == provider.StateStopped || box.State == provider.StateFailed {
				box, err = p.Start(ctx, box.ID)
				if err != nil {
					return err
				}
			}
		} else if !errors.Is(inspectErr, provider.ErrNotFound) {
			return inspectErr
		} else if command == "run" {
			return fmt.Errorf("box %q does not exist", opts.name)
		} else {
			setup := defaultSetup(c)
			if opts.reuse {
				setup, err = loadSetup(file, c.Name)
				if err != nil {
					return err
				}
			}
			applyRunOptions(&setup, opts)
			bootstrapComponents = append([]string(nil), setup.Components...)
			if !opts.reuse && a.IsTerminal != nil && a.IsTerminal() {
				previewArgv := opts.argv
				if len(previewArgv) == 0 {
					previewArgv = defaultSession(opts.detach)
				}
				setup, err = a.configureSetup(ctx, c, p, opts.name, setup, previewArgv)
				if errors.Is(err, errSetupCancelled) {
					fmt.Fprintln(a.Err, "vmbox: setup cancelled; nothing was provisioned")
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
			setupEnv := map[string]string{
				"VMBOX_NAME": opts.name, "VMBOX_PROVIDER": p.Name(), "VMBOX_REGION": prepared.setup.Region,
				"VMBOX_CPU":        strconv.FormatFloat(prepared.setup.Resources.CPU, 'f', -1, 64),
				"VMBOX_MEMORY_MIB": strconv.FormatInt(prepared.setup.Resources.MemoryMiB, 10),
				"VMBOX_DISK_GIB":   strconv.FormatInt(prepared.setup.Resources.DiskGiB, 10), "VMBOX_WORKSPACE": prepared.setup.Workspace,
				"VMBOX_COST":  "use vmbox cost " + opts.name,
				"HOME":        "/data/home",
				"SHELL":       "/bin/bash",
				"BUN_INSTALL": "/opt/bun",
				"FOUNDRY_DIR": "/opt/foundry",
				"PATH":        "/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			}
			box, err = p.Create(ctx, provider.CreateRequest{Name: opts.name, Image: c.Image, Region: prepared.setup.Region, Owner: provider.Owner{AccountID: "standalone", BoxID: opts.name}, Resources: prepared.setup.Resources, Components: prepared.setup.Components, Env: setupEnv})
			if err != nil {
				return err
			}
			created = true
			if err := a.ensureBootstrap(ctx, p, opts.name, bootstrapComponents, false); err != nil {
				return err
			}
			if err := a.uploadPrepared(ctx, p, opts.name, prepared); err != nil {
				return err
			}
			if prepared.setup.Save {
				if err := saveSetup(a.ConfigPath, file, c.Name, prepared.setup); err != nil {
					return fmt.Errorf("save complete reusable setup: %w", err)
				}
				fmt.Fprintf(a.Err, "vmbox: saved complete reusable setup for context %s\n", c.Name)
			}
		}
		if !created {
			if err := a.ensureBootstrap(ctx, p, opts.name, bootstrapComponents, !opts.componentsSet); err != nil {
				return err
			}
		}
		if err := a.uploadWelcome(ctx, p, box, c.Name); err != nil {
			return err
		}
		if created {
			fmt.Fprintf(a.Err, "vmbox: box %q is ready\n", opts.name)
		}
		if len(opts.argv) == 0 {
			opts.argv = defaultSession(opts.detach)
		} else if !opts.detach {
			opts.argv = interactiveSession(opts.argv)
		}
		restore := func() {}
		if !opts.detach {
			restore, err = makeRaw(a.In)
			if err != nil {
				return fmt.Errorf("configure interactive terminal: %w", err)
			}
		}
		result, execErr := p.Exec(ctx, opts.name, opts.argv, provider.ExecOptions{Interactive: !opts.detach, Detach: opts.detach, Stdin: a.In, Stdout: a.Out, Stderr: a.Err})
		restore()
		err = execErr
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("command exited with status %d", result.ExitCode)
		}
		return nil
	case "ls", "list":
		boxes, err := p.List(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(boxes)
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("status requires a box")
		}
		box, err := p.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "task-status":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("task-status requires a box and optional run ID")
		}
		argv := []string{"vmbox-runtime", "status"}
		if len(args) == 3 {
			argv = append(argv, args[2])
		}
		result, err := p.Exec(ctx, args[1], argv, provider.ExecOptions{Stdout: a.Out, Stderr: a.Err})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("task-status exited with status %d", result.ExitCode)
		}
		return nil
	case "logs":
		if len(args) < 2 {
			return fmt.Errorf("logs requires a box")
		}
		opts := provider.LogOptions{Tail: 100}
		for _, arg := range args[2:] {
			if arg == "--follow" {
				opts.Follow = true
			} else if strings.HasPrefix(arg, "--tail=") {
				opts.Tail, _ = strconv.Atoi(strings.TrimPrefix(arg, "--tail="))
			} else {
				return fmt.Errorf("unknown logs option %q", arg)
			}
		}
		return p.Logs(ctx, args[1], opts, a.Out)
	case "stop":
		if len(args) != 2 {
			return fmt.Errorf("stop requires a box")
		}
		box, err := p.Stop(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "start":
		if len(args) != 2 {
			return fmt.Errorf("start requires a box")
		}
		box, err := p.Start(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "resize":
		name, flagArgs, err := a.selectStandaloneResize(ctx, p, args[1:])
		if err != nil {
			return err
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
			resources, err = a.selectResizeResources("Choose new limits for " + name)
			if err != nil {
				return err
			}
		}
		box, err := p.Resize(ctx, name, resources)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "clean":
		if len(args) < 2 {
			return fmt.Errorf("clean requires a box and --yes")
		}
		yes := false
		for _, arg := range args[2:] {
			if arg == "--yes" {
				yes = true
			} else {
				return fmt.Errorf("unknown clean option %q", arg)
			}
		}
		if !yes {
			return fmt.Errorf("cleanup is destructive; review the exact box and rerun with --yes")
		}
		box, err := p.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		return p.Delete(ctx, args[1], box.Owner)
	case "cost":
		if len(args) != 2 {
			return fmt.Errorf("cost requires a box")
		}
		usage, err := p.Usage(ctx, args[1])
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(usage)
	case "resume":
		if len(args) != 1 {
			return fmt.Errorf("bare 'vmbox resume' opens selection; named resume is intentionally unsupported")
		}
		name, err := a.selectStandaloneBox(ctx, p, "Select a box to resume")
		if err != nil {
			return err
		}
		return a.standalone(ctx, file, p, c, []string{"new", name})
	default:
		if strings.HasPrefix(command, "-") {
			return fmt.Errorf("unknown command %q", command)
		}
		next := append([]string{"new", command}, args[1:]...)
		return a.standalone(ctx, file, p, c, next)
	}
}

func (a *App) controller(ctx context.Context, file config.File, c config.Context, args []string) error {
	token := a.Environ[c.TokenEnv]
	if token == "" {
		return fmt.Errorf("controller token environment %s is empty; refusing standalone fallback", c.TokenEnv)
	}
	switch args[0] {
	case "new", "create", "run":
		opts, err := parseRunOptions(args[1:])
		if err != nil {
			return err
		}
		if args[0] != "run" {
			var existing []v1.Run
			if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/runs", nil, &existing, nil); err != nil {
				return err
			}
			for _, candidate := range existing {
				if candidate.Request.Box != opts.name || candidate.State == v1.JobDeleted {
					continue
				}
				var box provider.Box
				_, err := a.request(ctx, c, token, http.MethodPost, "/v1/runs/"+candidate.ID+"/start", map[string]any{}, &box, nil)
				if err != nil {
					return err
				}
				return json.NewEncoder(a.Out).Encode(box)
			}
		}
		setup := defaultSetup(c)
		if opts.reuse {
			setup, err = loadSetup(file, c.Name)
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
			return saveSetup(a.ConfigPath, file, c.Name, prepared.setup)
		}
		return nil
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("status requires a run ID")
		}
		var run v1.Run
		_, err := a.request(ctx, c, token, http.MethodGet, "/v1/runs/"+args[1], nil, &run, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(run)
	case "ls", "list":
		var runs []v1.Run
		_, err := a.request(ctx, c, token, http.MethodGet, "/v1/runs", nil, &runs, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(runs)
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
			return fmt.Errorf("bare 'vmbox resume' opens selection; named resume is intentionally unsupported")
		}
		runID, err := a.selectControllerRun(ctx, c, token, "Select a run to resume")
		if err != nil {
			return err
		}
		var box provider.Box
		_, err = a.request(ctx, c, token, http.MethodPost, "/v1/runs/"+runID+"/start", map[string]any{}, &box, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
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
		return fmt.Errorf("unknown controller command %q", args[0])
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
		return 0, fmt.Errorf("controller unavailable (no standalone fallback): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr v1.Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return resp.StatusCode, errors.New(apiErr.Error)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
func (a *App) usage() {
	fmt.Fprint(a.Out, `vmbox - provider-neutral development box orchestrator

Usage:
  vmbox [--context NAME] [--standalone] <box> [--detach] [-- COMMAND [ARG...]]
  vmbox new|create <box> [--reuse] [--detach] [creation options] [-- COMMAND [ARG...]]
  vmbox run <box> [--detach] -- COMMAND [ARG...]
  vmbox ls | status <box> | task-status <box> [run-id] | logs <box> [--follow] | stop <box>
  vmbox resume | resize [box] --cpu N --memory MiB | clean <box> --yes | cost <box>
  vmbox context add|use|list | provider validate
  vmbox questions | answer <question-id> <text>
  vmbox controller init|ensure --endpoint HTTPS_URL [--yes]
  vmbox users list|add|remove | credentials list|set|remove
  vmbox notifications list|setup|test|remove

Everything following -- is forwarded as an exact argv vector. Use bash -lc
explicitly when shell parsing is desired. A configured controller is mandatory
unless --standalone is supplied; controller failures never silently fall back.

Creation options include --component ID, --application-profile APP=PATH,
--github-credential HOST:USER[:ssh|https], --instructions PATH, --region,
--cpu, --memory, --disk, --workspace, notification, and lifecycle choices.
`)
}

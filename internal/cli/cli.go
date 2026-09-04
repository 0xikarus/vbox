package cli

import (
	"bufio"
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
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	incusprovider "github.com/0xikarus/vmbox-service/internal/provider/incus"
	railwayprovider "github.com/0xikarus/vmbox-service/internal/provider/railway"
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
	if err != nil && !standalone && args[0] != "controller" {
		file, active, err = a.promptControllerContext(file, contextName, config.Context{})
	}
	if err != nil {
		return err
	}
	if args[0] == "controller" {
		return a.provisionController(ctx, file, active, args[1:])
	}
	if active.Controller == "" && !standalone {
		file, active, err = a.promptControllerContext(file, active.Name, active)
		if err != nil {
			return err
		}
	}
	mode := "standalone"
	if active.Controller != "" && !standalone {
		mode = "controller"
	}
	fmt.Fprintf(a.Err, "vmbox: %s · context %s · provider %s", mode, active.Name, active.Provider)
	if active.Account != "" {
		fmt.Fprintf(a.Err, " · account %s", active.Account)
	}
	fmt.Fprintln(a.Err)
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
			return railwayprovider.New(railwayprovider.Config{ProjectID: ctx.Project, EnvironmentID: ctx.Environment, DefaultImage: ctx.Image, SSHKnownHostsFile: knownHosts, SSHBinary: runner.Env["VMBOX_REAL_SSH"], SSHControlDir: runner.Env["VMBOX_RAILWAY_CONTROL_DIR"]}, runner), nil
		}
		runner, knownHosts, runnerErr := railwayRunner(token, tokenEnvironment, a.Environ)
		if runnerErr != nil {
			return nil, runnerErr
		}
		return railwayprovider.New(railwayprovider.Config{ProjectID: ctx.Project, EnvironmentID: ctx.Environment, Token: token, TokenEnvironment: tokenEnvironment, DefaultImage: ctx.Image, SSHKnownHostsFile: knownHosts, SSHBinary: runner.Env["VMBOX_REAL_SSH"], SSHControlDir: runner.Env["VMBOX_RAILWAY_CONTROL_DIR"]}, runner), nil
	case "incus":
		return incusprovider.New(incusprovider.Config{Remote: ctx.IncusRemote, Project: ctx.IncusProject, DefaultImage: ctx.Image, VM: ctx.IncusVM}, procexec.OSRunner{}), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", ctx.Provider)
	}
}

// connectionProvider constructs only the provider's data-plane transport.
// Controller-managed Railway sessions already receive a fenced deployment
// endpoint from the controller and must not require a Railway API token or
// perform another control-plane lookup on the client.
func (a *App) connectionProvider(ctx config.Context) (provider.Provider, error) {
	if ctx.Provider != "railway" {
		return a.provider(ctx)
	}
	runner, knownHosts, err := railwayRunner("", "", a.Environ)
	if err != nil {
		return nil, err
	}
	return railwayprovider.New(railwayprovider.Config{
		SSHKnownHostsFile: knownHosts,
		SSHBinary:         runner.Env["VMBOX_REAL_SSH"],
		SSHControlDir:     runner.Env["VMBOX_RAILWAY_CONTROL_DIR"],
	}, runner), nil
}

func splitRun(args []string) (name string, detach, reuse bool, argv []string, err error) {
	opts, parseErr := parseRunOptions(args)
	return opts.name, opts.detach, opts.reuse, opts.argv, parseErr
}
func (a *App) standalone(ctx context.Context, file config.File, p provider.Provider, c config.Context, args []string) error {
	command := args[0]
	switch command {
	case "fleet":
		return a.localFleet(file, c, args[1:])
	case "task":
		return fmt.Errorf("vmbox task requires a controller; remove --standalone and configure a controller context")
	case "new", "create", "run":
		opts, err := parseRunOptions(args[1:])
		if err != nil {
			return err
		}
		checked := a.progress(ctx, fmt.Sprintf("checking box %q", opts.name))
		box, inspectErr := p.Inspect(ctx, opts.name)
		checked()
		created := false
		// An empty selection means "keep the components recorded inside an
		// existing box". New boxes replace it with their configured selection.
		var bootstrapComponents []string
		if opts.componentsSet {
			bootstrapComponents = append([]string(nil), opts.components...)
		}
		exists := inspectErr == nil
		repairingOwner := exists && box.Owner.BoxID == ""
		if repairingOwner && box.Owner.AccountID != "standalone" {
			return fmt.Errorf("Railway service for %q exists without complete vmbox ownership; refusing to adopt it", opts.name)
		}
		if inspectErr != nil && !errors.Is(inspectErr, provider.ErrNotFound) {
			return inspectErr
		}
		if exists && !repairingOwner {
			if box.State == provider.StateStopped || box.State == provider.StateFailed {
				started := a.progress(ctx, fmt.Sprintf("starting box %q", opts.name))
				box, err = p.Start(ctx, box.ID)
				started()
				if err != nil {
					return err
				}
			}
		}
		pendingSetup := false
		if exists && !repairingOwner {
			pendingSetup, err = a.standaloneSetupPending(ctx, p, box.ID)
			if err != nil {
				return err
			}
		}
		repairing := repairingOwner || pendingSetup
		if exists && !repairing && opts.reuse {
			fmt.Fprintf(a.Err, "vmbox: --reuse ignored because %q already exists\n", opts.name)
		}
		if command == "run" && (!exists || repairing) {
			if repairing {
				return fmt.Errorf("box %q has an incomplete prior creation; rerun vmbox new %s to repair it", opts.name, opts.name)
			}
			return fmt.Errorf("box %q does not exist", opts.name)
		}
		if !exists || repairing {
			if repairing {
				fmt.Fprintf(a.Err, "vmbox: resuming saved setup for incomplete service %q\n", opts.name)
			}
			workingDirectory, err := a.workingDirectory()
			if err != nil {
				return err
			}
			setup := defaultSetup(c)
			if opts.reuse || repairing {
				saved, loadErr := loadSetup(file, c.Name, workingDirectory)
				if loadErr == nil {
					setup = saved
				} else if opts.reuse || pendingSetup {
					err = loadErr
					return fmt.Errorf("resume saved setup for %q: %w", opts.name, err)
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
			if prepared.setup.Save {
				if err := saveSetup(a.ConfigPath, file, c.Name, workingDirectory, prepared.setup); err != nil {
					return fmt.Errorf("save reusable setup before provisioning: %w", err)
				}
				file, err = config.Load(a.ConfigPath)
				if err != nil {
					return fmt.Errorf("reload saved setup: %w", err)
				}
			}
			setupEnv := map[string]string{
				"VMBOX_NAME": opts.name, "VMBOX_PROVIDER": p.Name(), "VMBOX_REGION": prepared.setup.Region,
				"VMBOX_CPU":        strconv.FormatFloat(prepared.setup.Resources.CPU, 'f', -1, 64),
				"VMBOX_MEMORY_MIB": strconv.FormatInt(prepared.setup.Resources.MemoryMiB, 10),
				"VMBOX_DISK_GIB":   strconv.FormatInt(prepared.setup.Resources.DiskGiB, 10), "VMBOX_WORKSPACE": prepared.setup.Workspace,
				"VMBOX_COST":            "use vmbox cost " + opts.name,
				"HOME":                  "/data/home",
				"SHELL":                 "/bin/bash",
				"BUN_INSTALL":           "/opt/bun",
				"FOUNDRY_DIR":           "/opt/foundry",
				standaloneSetupStateEnv: "pending",
				"PATH":                  "/data/home/bin:/data/home/.local/bin:/opt/bun/bin:/opt/foundry/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			}
			provisioned := a.progress(ctx, fmt.Sprintf("provisioning box %q", opts.name))
			box, err = p.Create(ctx, provider.CreateRequest{Name: opts.name, Image: c.Image, Region: prepared.setup.Region, Owner: provider.Owner{AccountID: "standalone", BoxID: opts.name}, Resources: prepared.setup.Resources, Components: prepared.setup.Components, Env: setupEnv})
			provisioned()
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
			if err := a.markStandaloneSetupComplete(ctx, p, opts.name); err != nil {
				return err
			}
			if prepared.setup.Save {
				fmt.Fprintf(a.Err, "vmbox: saved reusable setup for context %s in %s\n", c.Name, workingDirectory)
			}
		}
		if !created {
			if err := a.ensureBootstrap(ctx, p, opts.name, bootstrapComponents, !opts.componentsSet); err != nil {
				return err
			}
		}
		fmt.Fprintf(a.Err, "vmbox: preparing tmux session for %q\n", opts.name)
		if err := a.uploadWelcome(ctx, p, box, c.Name); err != nil {
			return err
		}
		fmt.Fprintf(a.Err, "vmbox: tmux session metadata is ready\n")
		if created {
			fmt.Fprintf(a.Err, "vmbox: box %q is ready\n", opts.name)
		}
		sessionAttacher, nativeSession := p.(provider.SessionAttacher)
		interactive := !opts.detach && a.IsTerminal != nil && a.IsTerminal()
		if !nativeSession || opts.detach {
			if len(opts.argv) == 0 {
				if !opts.detach && !interactive {
					return fmt.Errorf("an interactive box session requires a terminal; use vmbox run BOX -- COMMAND for noninteractive execution")
				}
				opts.argv = defaultSession(opts.detach)
			} else if interactive {
				opts.argv = interactiveSession(opts.argv)
			}
		}
		if len(opts.argv) == 0 && !interactive {
			return fmt.Errorf("an interactive box session requires a terminal; use vmbox run BOX -- COMMAND for noninteractive execution")
		}
		restore := func() {}
		if interactive {
			rawRestore, rawErr := makeRaw(a.In)
			if rawErr != nil {
				return fmt.Errorf("configure interactive terminal: %w", rawErr)
			}
			restored := false
			restore = func() {
				if !restored {
					rawRestore()
					restored = true
				}
			}
			defer restore()
		}
		execOptions := provider.ExecOptions{Interactive: interactive, Detach: opts.detach, Stdin: a.In, Stdout: a.Out, Stderr: a.Err}
		var result provider.ExecResult
		var execErr error
		if interactive && nativeSession {
			sessionCommand := append([]string(nil), opts.argv...)
			if len(sessionCommand) == 0 {
				sessionCommand = []string{"vmbox-runtime", "welcome"}
			}
			result, execErr = sessionAttacher.AttachSession(ctx, opts.name, "vmbox", sessionCommand, execOptions)
		} else {
			result, execErr = p.Exec(ctx, opts.name, opts.argv, execOptions)
		}
		restore()
		if interactive && ctx.Err() != nil {
			fmt.Fprintln(a.Err, "\nvmbox: connection closed; box and tmux session are still running")
			return nil
		}
		err = execErr
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("command exited with status %d", result.ExitCode)
		}
		if opts.hibernateOnExit {
			return a.hibernateBox(ctx, p, box)
		}
		if interactive {
			return a.postInteractiveExit(ctx, p, box)
		}
		a.nonInteractiveFollowUp(box.Name)
		return nil
	case "auth":
		return a.syncApplicationProfiles(ctx, p, args[1:])
	case "ls", "list":
		jsonOutput, err := parseListOutput(args)
		if err != nil {
			return err
		}
		listed := a.progress(ctx, "loading boxes")
		boxes, err := p.List(ctx)
		listed()
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(a.Out).Encode(boxes)
		}
		return writeBoxList(a.Out, boxes)
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("status requires a box")
		}
		loadedStatus := a.progress(ctx, fmt.Sprintf("loading status for %q", args[1]))
		box, err := p.Inspect(ctx, args[1])
		loadedStatus()
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
		stopped := a.progress(ctx, fmt.Sprintf("stopping box %q", args[1]))
		box, err := p.Stop(ctx, args[1])
		stopped()
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "start":
		if len(args) != 2 {
			return fmt.Errorf("start requires a box")
		}
		started := a.progress(ctx, fmt.Sprintf("starting box %q", args[1]))
		box, err := p.Start(ctx, args[1])
		started()
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(box)
	case "hibernate":
		if len(args) != 2 {
			return fmt.Errorf("hibernate requires a box")
		}
		box, err := p.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		return a.hibernateBox(ctx, p, box)
	case "delete-volume":
		if len(args) != 2 {
			return fmt.Errorf("delete-volume requires a box")
		}
		box, err := p.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		timeout := a.ExitPromptTimeout
		if timeout <= 0 {
			timeout = defaultExitPromptTimeout
		}
		return a.confirmAndDeleteVolume(ctx, bufio.NewReader(a.In), timeout, p, box)
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
		resized := a.progress(ctx, fmt.Sprintf("resizing box %q", name))
		box, err := p.Resize(ctx, name, resources)
		resized()
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
		cleaned := a.progress(ctx, fmt.Sprintf("deleting box %q and its storage", args[1]))
		box, err := p.Inspect(ctx, args[1])
		if err != nil {
			cleaned()
			return err
		}
		err = p.Delete(ctx, args[1], box.Owner)
		cleaned()
		return err
	case "cost":
		if len(args) != 2 {
			return fmt.Errorf("cost requires a box")
		}
		loadedCost := a.progress(ctx, fmt.Sprintf("loading cost for %q", args[1]))
		usage, err := p.Usage(ctx, args[1])
		loadedCost()
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
	case "fleet":
		return a.controllerFleet(ctx, c, token, args[1:])
	case "boxes", "box":
		return a.controllerBoxes(ctx, c, token, args[1:])
	case "auth":
		return a.controllerLogicalBoxAuth(ctx, c, token, args[1:])
	case "task":
		return a.controllerTask(ctx, c, token, args[1:])
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
		if len(args) != 2 {
			return fmt.Errorf("status requires a logical box name or ID")
		}
		return a.controllerBoxes(ctx, c, token, []string{"status", args[1]})
	case "ls", "list":
		jsonOutput, err := parseListOutput(args)
		if err != nil {
			return err
		}
		var inventory v1.BoxInventory
		_, err = a.request(ctx, c, token, http.MethodGet, "/v1/inventory"+fleetQuery(c), nil, &inventory, nil)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(a.Out).Encode(inventory)
		}
		return writeInventoryList(a.Out, inventory)
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
		if strings.HasPrefix(args[0], "-") || len(args) != 1 {
			return fmt.Errorf("unknown controller command %q", args[0])
		}
		return a.controllerBoxes(ctx, c, token, []string{"open", args[0]})
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
  vmbox [--context NAME] [--standalone] <box>
  vmbox new|create <box> [--disk GiB] [--region ID] [--allocate|--detach]
  vmbox run <box> [run options] -- COMMAND [ARG...]
  vmbox ls [--json] | status <box> | task-status <box> [run-id]
  vmbox stop <box> | start <box>
  vmbox auth <box> [--application-profile APP=PATH]
                   [--github-credential HOST:USER[:ssh|https] | --no-github]
  vmbox task [box] [--agent codex|claude|opencode|shell] [--prompt TEXT] [--session NAME]
  vmbox resume | resize [box] --cpu N --memory MiB | clean <box> --yes | cost <box>
  vmbox fleet status [--json] | fleet slots | fleet slots set COUNT
  vmbox context add|use|list | provider validate
  vmbox questions | answer <question-id> <text>
  vmbox controller init|ensure [--endpoint HTTPS_URL]
      [--source PATH|--controller-image IMAGE@sha256:DIGEST] --box-image IMAGE@sha256:DIGEST [--yes]
  vmbox users list|add|remove | credentials list|set|remove
  vmbox notifications list|setup|test|remove

Everything following -- is forwarded as an exact argv vector. Use bash -lc
explicitly when shell parsing is desired. Controller management is the default;
interactive first use asks for its URL. --standalone is the only explicit
bypass, and controller failures never silently fall back.

In controller mode, new/create always provisions a logical box in the managed
fleet; run is the explicit ephemeral-job command. vmbox <box> restores the
logical box when necessary and opens its tmux session. In standalone Railway mode, stop removes only the active
deployment; vmbox <box> redeploys it while preserving the service and /data.
Only an explicit delete/clean operation removes persistent storage.

Creation options:
  Tools/auth:     --component ID, --application-profile APP=PATH,
                  --github-credential HOST:USER[:ssh|https], --instructions PATH
  Location/size:  --region ID, --cpu N, --memory MiB, --disk GiB, --workspace PATH
  Automation:     --notification-policy NAME, --on-success ACTION,
                  --on-failure ACTION, --max-ttl DURATION
`)
}

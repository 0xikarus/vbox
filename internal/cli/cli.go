package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
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
	sevallaprovider "github.com/0xikarus/vmbox-service/internal/provider/sevalla"
)

type App struct {
	In         io.Reader
	Out, Err   io.Writer
	Environ    map[string]string
	HTTP       *http.Client
	ConfigPath string
}

func New() *App {
	return &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Environ: envMap(), HTTP: &http.Client{Timeout: 75 * time.Second}}
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
		return a.controller(ctx, active, args)
	}
	p, err := a.provider(active)
	if err != nil {
		return err
	}
	return a.standalone(ctx, p, active, args)
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
		fs.StringVar(&ctx.Company, "company", "", "company ID")
		fs.StringVar(&ctx.Cluster, "cluster", "", "cluster ID")
		fs.StringVar(&ctx.ResourceType, "resource-type", "", "resource type ID")
		fs.StringVar(&ctx.Image, "image", "", "default OCI image")
		fs.StringVar(&ctx.DockerContext, "docker-context", "", "Docker context")
		fs.StringVar(&ctx.DockerHost, "docker-host", "", "Docker host")
		fs.BoolVar(&ctx.DockerTLSVerify, "docker-tls-verify", false, "require Docker mTLS")
		fs.StringVar(&ctx.DockerCertPath, "docker-cert-path", "", "Docker certificate directory")
		fs.StringVar(&ctx.IncusRemote, "incus-remote", "", "Incus remote")
		fs.StringVar(&ctx.IncusProject, "incus-project", "", "Incus project")
		fs.BoolVar(&ctx.IncusVM, "incus-vm", false, "use QEMU VMs")
		fs.StringVar(&ctx.PreAttachedDisk, "pre-attached-disk", "", "manual Sevalla disk ID")
		fs.StringVar(&ctx.TokenEnv, "token-env", "VMBOX_CONTROLLER_TOKEN", "environment variable containing controller token")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if ctx.Provider == "" {
			return fmt.Errorf("--provider is required")
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

func (a *App) provider(ctx config.Context) (provider.Provider, error) {
	switch ctx.Provider {
	case "docker":
		return dockerprovider.New(dockerprovider.Config{Context: ctx.DockerContext, Host: ctx.DockerHost, TLSVerify: ctx.DockerTLSVerify, CertPath: ctx.DockerCertPath, DefaultImage: ctx.Image}, procexec.OSRunner{}), nil
	case "railway":
		return railwayprovider.New(railwayprovider.Config{ProjectID: ctx.Project, EnvironmentID: ctx.Environment, Token: a.Environ["RAILWAY_API_TOKEN"], DefaultImage: ctx.Image}, procexec.OSRunner{}), nil
	case "sevalla":
		return sevallaprovider.New(sevallaprovider.Config{Token: a.Environ["SEVALLA_API_TOKEN"], APIURL: a.Environ["SEVALLA_API_URL"], CompanyID: ctx.Company, ProjectID: ctx.Project, ClusterID: ctx.Cluster, ResourceTypeID: ctx.ResourceType, DefaultImage: ctx.Image, PreAttachedDisk: ctx.PreAttachedDisk, HTTPClient: a.HTTP}), nil
	case "incus":
		return incusprovider.New(incusprovider.Config{Remote: ctx.IncusRemote, Project: ctx.IncusProject, DefaultImage: ctx.Image, VM: ctx.IncusVM}, procexec.OSRunner{}), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", ctx.Provider)
	}
}

func splitRun(args []string) (name string, detach, reuse bool, argv []string, err error) {
	if len(args) == 0 {
		return "", false, false, nil, fmt.Errorf("box name is required")
	}
	name = args[0]
	i := 1
	for i < len(args) && args[i] != "--" {
		switch args[i] {
		case "--detach", "-d":
			detach = true
		case "--reuse":
			reuse = true
		default:
			return "", false, false, nil, fmt.Errorf("unknown box option %q", args[i])
		}
		i++
	}
	if i < len(args) && args[i] == "--" {
		argv = append([]string(nil), args[i+1:]...)
	}
	return
}
func (a *App) standalone(ctx context.Context, p provider.Provider, c config.Context, args []string) error {
	command := args[0]
	switch command {
	case "new", "create", "run":
		name, detach, _, argv, err := splitRun(args[1:])
		if err != nil {
			return err
		}
		if command == "run" {
			if _, err := p.Inspect(ctx, name); err != nil {
				return err
			}
		} else {
			_, err = p.Create(ctx, provider.CreateRequest{Name: name, Image: c.Image, Region: c.Cluster, Owner: provider.Owner{AccountID: "standalone", BoxID: name}, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 10}})
			if err != nil {
				return err
			}
		}
		if len(argv) == 0 {
			argv = []string{"tmux", "new-session", "-A", "-s", "vmbox"}
		}
		result, err := p.Exec(ctx, name, argv, provider.ExecOptions{Interactive: !detach, Detach: detach, Stdin: a.In, Stdout: a.Out, Stderr: a.Err})
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
		if len(args) < 2 {
			return fmt.Errorf("resize requires a box")
		}
		fs := flag.NewFlagSet("resize", flag.ContinueOnError)
		cpu := fs.Float64("cpu", 0, "CPU limit")
		memory := fs.Int64("memory", 0, "memory MiB")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		box, err := p.Resize(ctx, args[1], provider.Resources{CPU: *cpu, MemoryMiB: *memory})
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
		boxes, err := p.List(ctx)
		if err != nil {
			return err
		}
		for _, box := range boxes {
			fmt.Fprintln(a.Out, box.Name)
		}
		return nil
	default:
		if strings.HasPrefix(command, "-") {
			return fmt.Errorf("unknown command %q", command)
		}
		next := append([]string{"new", command}, args[1:]...)
		return a.standalone(ctx, p, c, next)
	}
}

func (a *App) controller(ctx context.Context, c config.Context, args []string) error {
	token := a.Environ[c.TokenEnv]
	if token == "" {
		return fmt.Errorf("controller token environment %s is empty; refusing standalone fallback", c.TokenEnv)
	}
	switch args[0] {
	case "new", "create", "run":
		name, _, _, argv, err := splitRun(args[1:])
		if err != nil {
			return err
		}
		if len(argv) == 0 {
			argv = []string{"tmux", "new-session", "-A", "-s", "vmbox"}
		}
		req := v1.CreateRunRequest{Provider: c.Provider, Box: name, Image: c.Image, Region: c.Cluster, Command: argv, Resources: provider.Resources{CPU: 2, MemoryMiB: 4096, DiskGiB: 10}}
		var run v1.Run
		status, err := a.request(ctx, c, token, http.MethodPost, "/v1/runs", req, &run, map[string]string{"Idempotency-Key": "cli-" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "accepted %s (%d)\n", run.ID, status)
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
		if len(args) < 2 {
			return fmt.Errorf("resize requires a run ID")
		}
		fs := flag.NewFlagSet("resize", flag.ContinueOnError)
		cpu := fs.Float64("cpu", 0, "CPU limit")
		memory := fs.Int64("memory", 0, "memory MiB")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		var box provider.Box
		_, err := a.request(ctx, c, token, http.MethodPost, "/v1/runs/"+args[1]+"/resize", provider.Resources{CPU: *cpu, MemoryMiB: *memory}, &box, nil)
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
  vmbox new|create <box> [--detach] [-- COMMAND [ARG...]]
  vmbox run <box> [--detach] -- COMMAND [ARG...]
  vmbox ls | status <box> | logs <box> [--follow] | stop <box>
  vmbox resize <box> --cpu N --memory MiB | clean <box> --yes | cost <box>
  vmbox context add|use|list | provider validate
  vmbox questions | answer <question-id> <text>

Everything following -- is forwarded as an exact argv vector. Use bash -lc
explicitly when shell parsing is desired. A configured controller is mandatory
unless --standalone is supplied; controller failures never silently fall back.
`)
}

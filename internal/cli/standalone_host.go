package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/boxruntime"
	"github.com/0xikarus/vmbox-service/internal/components"
	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/instance"
	"github.com/0xikarus/vmbox-service/internal/provider"
	dockerprovider "github.com/0xikarus/vmbox-service/internal/provider/docker"
	"golang.org/x/sys/unix"
)

type standaloneInstance struct {
	Version        int                `json:"version"`
	Name           string             `json:"name"`
	ID             string             `json:"id"`
	Host           string             `json:"host,omitempty"`
	Mode           string             `json:"mode"`
	Agent          string             `json:"agent,omitempty"`
	Tools          []string           `json:"tools,omitempty"`
	Proxy          string             `json:"proxy,omitempty"`
	Directory      string             `json:"directory,omitempty"`
	Image          string             `json:"image"`
	GatewayAddress string             `json:"gatewayAddress"`
	Port           int                `json:"port"`
	Resources      provider.Resources `json:"resources"`
	State          string             `json:"state"`
}

func (a *App) standaloneRegistry() string {
	p := a.ConfigPath
	if p == "" {
		p = config.DefaultPath()
	}
	return filepath.Join(filepath.Dir(p), "standalone")
}
func standaloneSave(path string, i standaloneInstance) error {
	b, e := json.MarshalIndent(i, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".instance-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func (a *App) standaloneProvider(i standaloneInstance) *instance.Manager {
	host := ""
	if i.Host != "" {
		host = "ssh://" + i.Host
	}
	return instance.New(dockerprovider.Config{Host: host, PublishGateway: true, GatewayAddress: i.GatewayAddress, GatewayPort: i.Port, CreateStopped: true, StopOnSuccess: true, MemorySwapMiB: &i.Resources.MemoryMiB}, a.Runner)
}
func (a *App) standaloneHost(ctx context.Context, args []string) error {
	command := args[0]
	registry := a.standaloneRegistry()
	if command == "list" {
		entries, err := os.ReadDir(registry)
		if os.IsNotExist(err) {
			return json.NewEncoder(a.Out).Encode([]standaloneInstance{})
		}
		if err != nil {
			return err
		}
		out := []standaloneInstance{}
		for _, f := range entries {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			b, e := os.ReadFile(filepath.Join(registry, f.Name()))
			if e != nil {
				return e
			}
			var i standaloneInstance
			if e = json.Unmarshal(b, &i); e != nil {
				return e
			}
			out = append(out, i)
		}
		return json.NewEncoder(a.Out).Encode(out)
	}
	if len(args) < 2 {
		return fmt.Errorf("standalone %s requires an instance name", command)
	}
	name := args[1]
	if err := provider.ValidateName(name); err != nil {
		return err
	}
	path := filepath.Join(registry, name+".json")
	if err := os.MkdirAll(registry, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(registry, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("another standalone operation is in progress")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if command == "create" {
		fs := flag.NewFlagSet("standalone create", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		host := fs.String("host", "", "SSH config alias or user@host")
		mode := fs.String("mode", "vm", "vm or container")
		agent := fs.String("agent", "opencode", "managed agent: opencode, claude or codex")
		var profileSources stringList
		fs.Var(&profileSources, "profile", "import APP=PATH or github=HOST:USER; repeat for additional applications")
		image := fs.String("image", "vbox-standalone:dev", "prebuilt standalone application image")
		directory := fs.String("storage", "", "dedicated manifest directory on the target host")
		cpu := fs.Float64("cpus", 2, "CPU limit")
		fs.Float64Var(cpu, "cpu", 2, "CPU limit")
		memory := new(standaloneMemory)
		*memory = 4096
		fs.Var(memory, "memory", "memory limit in MiB or a size such as 6g")
		address := fs.String("listen", "127.0.0.1", "host binding IP")
		port := fs.Int("port", 0, "host gateway port; 0 selects a free port")
		endpoint := fs.String("model-url", "", "model API endpoint reachable from the box")
		model := fs.String("model", "", "model ID")
		vision := fs.Bool("model-vision", false, "model supports image input")
		toolsFlag := fs.String("tools", "", "comma-separated tool presets, such as blender,foundry")
		proxyFlag := fs.String("proxy", "none", "none, tor or warp (local SOCKS5)")
		passwordFile := fs.String("password-file", "", "private file containing the initial web password")
		password := ""
		passwordSupplied := false
		noDialog := fs.Bool("no-dialog", false, "use explicit arguments")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected arguments")
		}
		if strings.HasPrefix(*host, "-") || strings.ContainsAny(*host, "/\n\r\x00 ") {
			return fmt.Errorf("invalid SSH target")
		}
		if *mode != "container" {
			return fmt.Errorf("VM installation is not implemented yet; use --mode container")
		}
		supplied := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { supplied[f.Name] = true })
		complete := supplied["mode"] && (supplied["cpus"] || supplied["cpu"]) && supplied["memory"] && (((*agent == "claude" || *agent == "codex") && len(profileSources) > 0) || (*endpoint != "" && *model != ""))
		terminal := a.IsTerminal != nil && a.IsTerminal()
		if !*noDialog && !complete && !terminal {
			return fmt.Errorf("incomplete standalone creation requires a terminal wizard; supply all settings or --no-dialog")
		}
		if *passwordFile != "" {
			passwordSupplied = true
			b, err := os.ReadFile(*passwordFile)
			if err != nil {
				return fmt.Errorf("cannot read password file")
			}
			password = strings.TrimRight(string(b), "\r\n")
			clear(b)
		}
		if !*noDialog && !complete && terminal {
			c := &formField{Label: "CPU limit", Value: strconv.FormatFloat(*cpu, 'f', -1, 64)}
			m := &formField{Label: "Memory", Value: memory.String()}
			mo := &formField{Label: "Isolation", Value: *mode, ReadOnly: true, Detail: "Container mode is supported; VM provisioning is still under development."}
			ag := &formField{Label: "Managed agent", Value: *agent, Choices: []string{"opencode", "claude", "codex"}}
			ep := &formField{Label: "Model endpoint", Value: *endpoint}
			md := &formField{Label: "Model ID", Value: *model}
			ep.When = func() bool { return ag.Value == "opencode" }
			md.When = ep.When
			vis := &formField{Label: "Model image input", Value: strconv.FormatBool(*vision), Choices: []string{"false", "true"}, When: ep.When}
			target := *host
			if target == "" {
				target = "local Docker host"
			}
			storage := &formField{Label: "Host directory", Value: *directory, Detail: "Blank creates ~/.local/share/vbox-standalone/instances/ID on the target. Workspace data uses the owned Docker volume."}
			pw := &formField{Label: "Web password", Value: password, Secret: true, Detail: "Used for desktop, tmux and chat. Any password is accepted."}
			proxy := &formField{Label: "Proxy / IP", Value: *proxyFlag, Choices: []string{"none", "tor", "warp"}, Detail: "Local SOCKS5; applications must opt in."}
			selected, err := standaloneTools(*toolsFlag, "none")
			if err != nil {
				return err
			}
			toolRows := toolFields(selected)
			toolRows = slices.DeleteFunc(toolRows, func(f *formField) bool {
				return f.Label == "desktop" || f.Label == "tor-socks5" || f.Label == "cloudflare-warp"
			})
			for _, row := range toolRows {
				if row.Label == "blender" {
					row.RenderRow = func(width int) string {
						mark := "[ ]"
						if row.Value == "Install" {
							mark = "[x]"
						}
						return mark + " Blender · distribution package + Blender MCP (amd64/arm64)"
					}
				}
			}

			fields := []*formField{{Label: "Target", Value: target, ReadOnly: true}, {Label: "Host capacity", Value: a.standaloneCapacity(ctx, *host), ReadOnly: true}, {Label: "Host resources", Value: a.standaloneLinuxResources(ctx, *host), ReadOnly: true}, mo, ag, c, m, storage, ep, md, vis, pw, proxy}
			fields = append(fields, toolRows...)
			type selectedProfile struct {
				app, path string
				choice    *formField
			}
			var imports []selectedProfile
			home, _ := os.UserHomeDir()
			profiles, discoverErr := components.DiscoverConfigured(home, a.Environ)
			if discoverErr != nil {
				return discoverErr
			}
			profiles = append(profiles, a.standaloneGitHubProfiles(ctx)...)
			for _, p := range profiles {
				if p.Component != "claude" && p.Component != "codex" && p.Component != "opencode" && p.Component != "github" {
					continue
				}
				field := &formField{Label: p.Component + " / " + p.Name, Value: "Skip", Choices: []string{"Skip", "Import"}, Checkbox: true, Detail: p.Directory}
				for _, source := range profileSources {
					if source == p.Component+"="+p.Directory {
						field.Value = "Import"
					}
				}
				fields = append(fields, field)
				imports = append(imports, selectedProfile{p.Component, p.Directory, field})
			}
			if err := a.runForm(ctx, "Standalone instance · experimental", fields, func(progress func(string)) error {
				var err error
				*cpu, err = strconv.ParseFloat(c.Value, 64)
				if err != nil {
					return err
				}
				err = memory.Set(m.Value)
				if err != nil {
					return err
				}
				*mode = mo.Value
				*agent = ag.Value
				*directory = storage.Value
				*endpoint = ep.Value
				*model = md.Value
				*vision = vis.Value == "true"
				password = pw.Value
				passwordSupplied = true
				*proxyFlag = proxy.Value
				*toolsFlag = strings.Join(selectedTools(toolRows), ",")

				profileSources = nil
				for _, p := range imports {
					if p.choice.Value == "Import" {
						profileSources = append(profileSources, p.app+"="+p.path)
					}
				}
				archive, err := a.standaloneProfileArchive(ctx, profileSources)
				clear(archive)
				if err != nil {
					return err
				}
				return nil
			}); err != nil {
				return err
			}
		}
		chosenTools, err := standaloneTools(*toolsFlag, *proxyFlag)
		if err != nil {
			return err
		}
		var passwordArchive []byte
		if passwordSupplied {
			passwordArchive, err = standalonePasswordArchive(password)
			password = ""
			if err != nil {
				return err
			}
			defer clear(passwordArchive)
		}
		if *mode != "container" {
			return fmt.Errorf("VM installation is not implemented yet; container mode requires explicit --mode container")
		}
		if *cpu <= 0 || math.IsNaN(*cpu) || math.IsInf(*cpu, 0) || *memory < 512 || *port < 0 || *port > 65535 {
			return fmt.Errorf("invalid CPU, memory, or port limit")
		}
		if *agent != "opencode" && *agent != "claude" && *agent != "codex" {
			return fmt.Errorf("agent must be opencode, claude or codex")
		}
		if *agent == "opencode" && (*endpoint == "" || *model == "") {
			return fmt.Errorf("model-url and model are required; no model service is installed")
		}
		u, err := url.Parse(*endpoint)
		if *endpoint != "" && (err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https")) {
			return fmt.Errorf("model-url must be an HTTP(S) endpoint without embedded credentials")
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("instance %s already exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(registry, 0700); err != nil {
			return err
		}
		profileArchive, err := a.standaloneProfileArchive(ctx, profileSources)
		if err != nil {
			return err
		}
		defer clear(profileArchive)
		if *port == 0 {
			chosen, err := a.standaloneFreePort(ctx, *host, *address)
			if err != nil {
				return err
			}
			*port = chosen
		}
		if err := a.prepareStandaloneImage(ctx, *host, *image); err != nil {
			return err
		}
		i := standaloneInstance{Version: 1, Name: name, ID: boxruntime.ID("sa-"), Host: *host, Mode: *mode, Agent: *agent, Tools: chosenTools, Proxy: *proxyFlag, Image: *image, GatewayAddress: *address, Port: *port, Resources: provider.Resources{CPU: *cpu, MemoryMiB: int64(*memory), PIDs: 2048}, State: "creating"}
		i.Directory = *directory
		if err = a.writeStandaloneHostManifest(ctx, &i); err != nil {
			return err
		}
		if err := standaloneSave(path, i); err != nil {
			return err
		}
		p := a.standaloneProvider(i)
		owner := provider.Owner{AccountID: "standalone", BoxID: i.ID, Lease: i.ID}
		_, err = p.Provision(ctx, provider.CreateRequest{Name: i.ID, Image: i.Image, Owner: owner, Resources: i.Resources, Env: map[string]string{"VMBOX_TOOLS": strings.Join(chosenTools, ","), "VMBOX_MODEL_URL": *endpoint, "VMBOX_MODEL": *model, "VMBOX_MODEL_VISION": strconv.FormatBool(*vision), "VMBOX_AGENT": *agent, "VMBOX_BOX_NAME": i.Name}, Command: []string{"vmbox-standalone"}}, profileArchive, passwordArchive)
		if err != nil {
			i.State = "create-failed"
			_ = standaloneSave(path, i)
			return err
		}
		i.State = "running"
		if err = a.writeStandaloneHostManifest(ctx, &i); err != nil {
			return err
		}
		if err = standaloneSave(path, i); err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(i)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var i standaloneInstance
	if err = json.Unmarshal(b, &i); err != nil {
		return err
	}
	p := a.standaloneProvider(i)
	owner := provider.Owner{AccountID: "standalone", BoxID: i.ID, Lease: i.ID}
	if command == "stop" {
		fs := flag.NewFlagSet("standalone stop", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		cleanup := fs.Bool("cleanup", false, "permanently remove the instance and all owned data")
		yes := fs.Bool("yes", false, "confirm permanent cleanup")
		if err = fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("unexpected shutdown arguments")
		}
		if *cleanup {
			if !*yes {
				return fmt.Errorf("shutdown cleanup permanently deletes files and profiles; add --yes")
			}
			command = "delete"
			args = []string{"delete", name, "--yes"}
		}
	}
	if command == "delete" {
		if len(args) != 3 || args[2] != "--yes" {
			return fmt.Errorf("deletion permanently removes this instance and files; repeat with --yes")
		}
		if err = a.cleanupStandaloneDirectory(ctx, i, true); err != nil {
			return err
		}
		current, inspectErr := p.Inspect(ctx, i.ID)
		if inspectErr != nil && !errors.Is(inspectErr, provider.ErrNotFound) {
			return inspectErr
		}
		if inspectErr == nil {
			if err = provider.VerifyOwner(current.Owner, owner); err != nil {
				return err
			}
			if current.State == provider.StateRunning {
				if _, err = p.Stop(ctx, i.ID); err != nil {
					return err
				}
			}
		}
		if err = p.Delete(ctx, i.ID, owner); err != nil {
			return err
		}
		if err = a.cleanupStandaloneDirectory(ctx, i, false); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return json.NewEncoder(a.Out).Encode(map[string]string{"name": i.Name, "state": "deleted"})
	}
	actual, err := p.Inspect(ctx, i.ID)
	if err != nil {
		return err
	}
	if err = provider.VerifyOwner(actual.Owner, owner); err != nil {
		return err
	}
	switch command {
	case "links":
		fs := flag.NewFlagSet("standalone links", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		base := fs.String("base-url", "", "external gateway origin")
		if err = fs.Parse(args[2:]); err != nil {
			return err
		}
		if *base == "" {
			argv := []string{"docker"}
			if i.Host != "" {
				argv = append(argv, "--host", "ssh://"+i.Host)
			}
			argv = append(argv, "port", "vmbox-"+i.ID, "8080")
			result, e := a.Runner.Run(ctx, argv, nil, nil, nil)
			if e != nil || result.ExitCode != 0 {
				return fmt.Errorf("cannot resolve published port; supply --base-url")
			}
			_, port, e := net.SplitHostPort(strings.TrimSpace(string(result.Stdout)))
			if e != nil {
				return fmt.Errorf("cannot resolve published port; supply --base-url")
			}
			host := "127.0.0.1"
			if i.Host != "" {
				host = i.Host
				if n := strings.LastIndex(host, "@"); n >= 0 {
					host = host[n+1:]
				}
			}
			*base = "http://" + net.JoinHostPort(host, port)
		}
		u, e := url.Parse(*base)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid base-url")
		}
		result, e := p.Exec(ctx, i.ID, []string{"cat", "/data/standalone/config.json"}, provider.ExecOptions{})
		if e != nil {
			return fmt.Errorf("cannot read instance links")
		}
		var c struct {
			Capabilities map[string]string `json:"capabilities"`
		}
		if e = json.Unmarshal([]byte(result.Stdout), &c); e != nil {
			return fmt.Errorf("invalid instance identity")
		}
		links := map[string]string{}
		for _, scope := range []string{"view", "desktop", "chat"} {
			token := c.Capabilities[scope]
			if len(token) != 64 {
				return fmt.Errorf("invalid capability")
			}
			u.Path = "/" + scope
			u.Fragment = token
			links[scope] = u.String()
		}
		return json.NewEncoder(a.Out).Encode(links)
	case "status":
		return json.NewEncoder(a.Out).Encode(actual)
	case "start":
		fs := flag.NewFlagSet("standalone start", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		port := fs.Int("port", i.Port, "gateway host port; 0 chooses a free port")
		image := fs.String("image", i.Image, "replacement image, preserving instance files")
		if err = fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *port < 0 || *port > 65535 {
			return fmt.Errorf("invalid start port or arguments")
		}
		recreate := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "port" || f.Name == "image" {
				recreate = true
			}
		})
		if recreate {
			if *port == 0 {
				*port, err = a.standaloneFreePort(ctx, i.Host, i.GatewayAddress)
				if err != nil {
					return err
				}
			}
			if *port != i.Port {
				if _, err = a.standaloneTargetPort(ctx, i.Host, i.GatewayAddress, *port); err != nil {
					return fmt.Errorf("requested startup port is unavailable; instance was not stopped")
				}
			}
			i.Port = *port
			i.Image = *image
			if _, err = p.Stop(ctx, i.ID); err != nil {
				return err
			}
			p = a.standaloneProvider(i)
			if _, err = p.Deploy(ctx, i.ID, i.Image); err != nil {
				return err
			}
		}
		actual, err = p.Start(ctx, i.ID)
	case "stop":
		actual, err = p.Stop(ctx, i.ID)
	case "set":
		fs := flag.NewFlagSet("standalone set", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		cpu := fs.Float64("cpus", actual.Resources.CPU, "CPU limit")
		mem := new(standaloneMemory)
		*mem = standaloneMemory(actual.Resources.MemoryMiB)
		fs.Var(mem, "memory", "memory in MiB or a size such as 6g")
		if err = fs.Parse(args[2:]); err != nil {
			return err
		}
		if len(args) == 2 {
			c := &formField{Label: "CPU limit", Value: strconv.FormatFloat(*cpu, 'f', -1, 64)}
			m := &formField{Label: "Memory", Value: mem.String()}
			if err = a.runFormButton(ctx, "Edit standalone resources", "Apply", []*formField{{Label: "Host capacity", Value: a.standaloneCapacity(ctx, i.Host), ReadOnly: true}, {Label: "Host resources", Value: a.standaloneLinuxResources(ctx, i.Host), ReadOnly: true}, c, m}, func(func(string)) error {
				var e error
				*cpu, e = strconv.ParseFloat(c.Value, 64)
				if e != nil {
					return e
				}
				e = mem.Set(m.Value)
				return e
			}); err != nil {
				return err
			}
		}
		if *cpu <= 0 || math.IsNaN(*cpu) || math.IsInf(*cpu, 0) || int64(*mem) < actual.Resources.MemoryMiB {
			return fmt.Errorf("CPU must be positive; live memory shrinking is not supported")
		}
		actual, err = p.Resize(ctx, i.ID, provider.Resources{CPU: *cpu, MemoryMiB: int64(*mem)})
		if err == nil && (actual.Resources.CPU != *cpu || actual.Resources.MemoryMiB != int64(*mem)) {
			return fmt.Errorf("resource update was not verified")
		}
	default:
		return fmt.Errorf("unknown standalone management command")
	}
	if err != nil {
		return err
	}
	i.State = string(actual.State)
	i.Resources = actual.Resources
	if err = a.writeStandaloneHostManifest(ctx, &i); err != nil {
		return err
	}
	if err = standaloneSave(path, i); err != nil {
		return err
	}
	return json.NewEncoder(a.Out).Encode(i)
}

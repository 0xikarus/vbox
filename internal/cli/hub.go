package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/components"
	hubservice "github.com/0xikarus/vmbox-service/internal/hub"
	"github.com/0xikarus/vmbox-service/internal/standalone"
)

func (a *App) hub(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(a.Out, `Single-host vbox hub:
  vbox hub install [--host SSH_ALIAS] [--data DIR] [--listen IP:PORT]
    [--cpus N --memory SIZE --max-running N --reserve-cpus N --reserve-memory SIZE --model-memory SIZE]
    [--image TAG --model-url URL --model ID --model-vision --password-file PATH --autostart --no-dialog]
  vbox hub init [same configuration flags]
  vbox hub serve [--data DIR]
  vbox hub up [--host SSH_ALIAS --data DIR]
  vbox hub status | list [--host SSH_ALIAS --data DIR --url URL]
  vbox hub create NAME [--cpu N --memory SIZE --agent opencode|claude|codex --profile APP=PATH --profile-id ID --goal TEXT]
  vbox hub start | stop | delete BOX_ID [--yes for delete]
  vbox hub set BOX_ID [--cpu N --memory SIZE]
  vbox hub message BOX_ID --text TEXT [--key IDEMPOTENCY_KEY]
  vbox hub messages BOX_ID
  vbox hub profiles | profile upload APP=PATH [--name NAME]
  vbox hub token [--host SSH_ALIAS --data DIR]
  vbox hub shutdown [--host SSH_ALIAS --data DIR]
All management commands accept --host, --data and --url. Hub shutdown retains workers and files.`)
		return nil
	}
	command := args[0]
	rest := args[1:]
	name := ""
	if command == "profile" {
		if len(rest) < 2 || (rest[0] != "upload" && rest[0] != "delete") {
			return fmt.Errorf("use hub profile upload APP=PATH")
		}
		command = "profile-" + rest[0]
		name = rest[1]
		rest = rest[2:]
	} else if command == "set" || command == "create" || command == "start" || command == "stop" || command == "delete" || command == "message" || command == "messages" {
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return fmt.Errorf("%s requires a box name or ID", command)
		}
		name = rest[0]
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("hub "+command, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	data := fs.String("data", "~/.local/share/vbox-hub", "private hub directory on target host")
	host := fs.String("host", "", "SSH target for installation/config discovery")
	baseURL := fs.String("url", "", "hub URL override")
	listen := fs.String("listen", "127.0.0.1:8089", "hub IP:port")
	image := fs.String("image", "vbox-standalone:dev", "standalone worker image")
	cpus := fs.Float64("cpus", 2, "total worker CPU budget")
	cpu := fs.Float64("cpu", 0, "new box CPU limit")
	memory := new(standaloneMemory)
	*memory = 4096
	fs.Var(memory, "memory", "worker budget or new box RAM")
	reserveCPU := fs.Float64("reserve-cpus", 1, "CPU capacity reserved for host/model")
	reserveRAM := new(standaloneMemory)
	*reserveRAM = 2048
	fs.Var(reserveRAM, "reserve-memory", "RAM reserved for hub and host")
	modelRAM := fs.Int64("model-memory", 0, "additional reserved local-model RAM in MiB")
	disk := fs.Int64("min-disk-free", 2048, "minimum free Docker disk in MiB")
	max := fs.Int("max-running", 4, "maximum active boxes")
	passwordFile := fs.String("password-file", "", "private initial hub password file")
	noDialog := fs.Bool("no-dialog", false, "use explicit settings")
	autostart := fs.Bool("autostart", false, "automatically restart the hub with Docker (install only; default off)")
	agent := fs.String("agent", "opencode", "opencode, claude or codex")
	modelSource := fs.String("model-source", "", "OpenCode model configuration: profile or custom")
	endpoint := fs.String("model-url", "", "local model API URL")
	model := fs.String("model", "", "custom endpoint model ID")
	agentModel := fs.String("agent-model", "", "per-box agent model override")
	agentEffort := fs.String("reasoning-effort", "", "per-box reasoning effort")
	vision := fs.Bool("model-vision", false, "enable image input")
	goal := fs.String("goal", "", "initial goal delivered once ready")
	text := fs.String("text", "", "chat message")
	key := fs.String("key", "", "message idempotency key")
	profileName := fs.String("name", "Imported profile", "saved profile name")
	tools := fs.String("tools", "", "blender,foundry")
	proxy := fs.String("proxy", "none", "none,tor,warp")
	yes := fs.Bool("yes", false, "confirm permanent box deletion")
	var sources, profileIDs stringList
	fs.Var(&sources, "profile", "APP=PATH or github=HOST:USER profile import")
	fs.Var(&profileIDs, "profile-id", "saved agent or GitHub profile ID")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected hub arguments")
	}
	if strings.HasPrefix(*host, "-") || strings.ContainsAny(*host, "/\r\n\x00 ") {
		return fmt.Errorf("invalid SSH target")
	}
	if command == "init" || command == "install" {
		pw := ""
		if *passwordFile != "" {
			b, e := os.ReadFile(*passwordFile)
			if e != nil {
				return fmt.Errorf("cannot read password file")
			}
			pw = strings.TrimRight(string(b), "\r\n")
			clear(b)
		}
		if !*noDialog && a.IsTerminal != nil && a.IsTerminal() {
			fields := []*formField{{Label: "Host", Value: func() string {
				if *host != "" {
					return *host
				}
				return "local"
			}(), ReadOnly: true}, {Label: "Capacity", Value: a.standaloneCapacity(ctx, *host), ReadOnly: true}, {Label: "Resources", Value: a.standaloneLinuxResources(ctx, *host), ReadOnly: true}}
			listenField := &formField{Label: "Hub listen address", Value: *listen}
			c := &formField{Label: "Worker CPU budget", Value: strconv.FormatFloat(*cpus, 'f', -1, 64)}
			ram := &formField{Label: "Worker RAM budget", Value: memory.String()}
			reserved := &formField{Label: "Reserved host RAM", Value: reserveRAM.String()}
			rcpu := &formField{Label: "Reserved host CPU", Value: strconv.FormatFloat(*reserveCPU, 'f', -1, 64)}
			mr := &formField{Label: "Reserved local-model RAM (MiB)", Value: strconv.FormatInt(*modelRAM, 10)}
			count := &formField{Label: "Maximum running boxes", Value: strconv.Itoa(*max)}
			ag := &formField{Label: "Default agent", Value: *agent, Choices: []string{"opencode", "claude", "codex"}}
			source := &formField{Label: "OpenCode configuration", Value: "profile", Choices: []string{"profile", "custom"}, When: func() bool { return ag.Value == "opencode" }}
			if *modelSource != "" {
				source.Value = *modelSource
			} else if *endpoint != "" {
				source.Value = "custom"
			}
			ep := &formField{Label: "Default model endpoint", Value: *endpoint, When: func() bool { return ag.Value == "opencode" && source.Value == "custom" }}
			md := &formField{Label: "Default model ID", Value: *model, When: ep.When}
			pwField := &formField{Label: "Hub password", Value: pw, Secret: true}
			boot := &formField{When: func() bool { return command == "install" }, Label: "Autostart hub with Docker", Value: strconv.FormatBool(*autostart), Choices: []string{"false", "true"}, Detail: "Off by default. Installation starts the hub now; this setting controls future automatic restarts."}
			fields = append(fields, listenField, c, ram, reserved, rcpu, mr, count, ag, source, ep, md, pwField, boot)
			if err := a.runForm(ctx, "Single-host vbox hub", fields, func(func(string)) error {
				var e error
				*cpus, e = strconv.ParseFloat(c.Value, 64)
				if e != nil {
					return e
				}
				if e = memory.Set(ram.Value); e != nil {
					return e
				}
				if e = reserveRAM.Set(reserved.Value); e != nil {
					return e
				}
				*reserveCPU, e = strconv.ParseFloat(rcpu.Value, 64)
				if e != nil {
					return e
				}
				*modelRAM, e = strconv.ParseInt(mr.Value, 10, 64)
				if e != nil {
					return e
				}
				*max, e = strconv.Atoi(count.Value)
				if e != nil {
					return e
				}
				*agent = ag.Value
				*modelSource = source.Value
				*listen = listenField.Value
				*endpoint = ep.Value
				*model = md.Value
				pw = pwField.Value
				*autostart = boot.Value == "true"
				return nil
			}); err != nil {
				return err
			}
		} else if *passwordFile == "" {
			return fmt.Errorf("initial hub setup needs a terminal wizard or --password-file")
		}
		addr, listenPort, e := net.SplitHostPort(*listen)
		portNumber, portErr := strconv.Atoi(listenPort)
		if e != nil || portErr != nil || net.ParseIP(addr) == nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("listen must be an IP address and valid nonzero port")
		}
		if *agent != "opencode" && *agent != "claude" && *agent != "codex" {
			return fmt.Errorf("agent must be opencode, claude or codex")
		}

		if *modelSource != "" && *modelSource != "profile" && *modelSource != "custom" {
			return fmt.Errorf("model-source must be profile or custom")
		}
		if *agent != "opencode" || *modelSource == "profile" {
			*endpoint = ""
			*model = ""
			*vision = false
		}
		hash, err := standalone.HashPassword(pw)
		pw = ""
		if err != nil {
			return err
		}
		c := hubservice.Config{ID: "hub-" + hubservice.Token()[:20], Listen: *listen, Image: *image, Password: hash, APIToken: hubservice.Token(), Policy: hubservice.Policy{CPUs: *cpus, MemoryMiB: int64(*memory), ReserveCPUs: *reserveCPU, ReserveMemoryMiB: int64(*reserveRAM), ModelMemoryMiB: *modelRAM, MinDiskFreeMiB: *disk, MaxRunning: *max}, Defaults: hubservice.Spec{Agent: *agent, ModelSource: *modelSource, CPU: math.Min(1, *cpus), MemoryMiB: min(2048, int64(*memory)), ModelURL: *endpoint, Model: *model, Vision: vision}}
		if err = c.Policy.Validate(hubservice.Host{CPUs: 1e6, MemoryMiB: 1 << 50}); err != nil {
			return err
		}
		if command == "install" {
			return a.installHub(ctx, *host, *data, c, *autostart)
		}
		dir, err := a.writeHubConfig(ctx, *host, *data, c)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Out, "Hub initialized at "+dir+"; API token is stored privately in hub.json")
		return nil
	}
	if command == "serve" {
		if *host != "" {
			return fmt.Errorf("hub serve runs on the target host; use hub install --host")
		}
		dir, err := hubLocalPath(*data)
		if err != nil {
			return err
		}
		return hubservice.Serve(ctx, dir, "")
	}
	c, dir, err := a.readHubConfig(ctx, *host, *data)
	if err != nil {
		return err
	}
	if command == "token" {
		fmt.Fprintln(a.Out, c.APIToken)
		return nil
	}
	if command == "shutdown" {
		return a.hubContainerAction(ctx, *host, c.ID, "stop")
	}
	if command == "up" {
		return a.hubContainerAction(ctx, *host, c.ID, "start")
	}
	listenHost, listenPort, splitErr := net.SplitHostPort(c.Listen)
	if splitErr != nil {
		return splitErr
	}
	remoteSSH := *host != "" && *baseURL == ""
	apiHost := listenHost
	if ip := net.ParseIP(apiHost); ip != nil && ip.IsUnspecified() {
		if strings.Contains(apiHost, ":") {
			apiHost = "::1"
		} else {
			apiHost = "127.0.0.1"
		}
	}
	base := *baseURL
	if base == "" {
		_, port, e := net.SplitHostPort(c.Listen)
		if e != nil {
			return e
		}
		target := "127.0.0.1"
		base = "http://" + net.JoinHostPort(target, port)
	}
	request := func(method, path string, in any, out any) error {
		if remoteSSH {
			return a.hubLoopbackRequest(ctx, *host, dir, "http://"+net.JoinHostPort(apiHost, listenPort), method, path, *key, in, out)
		}

		var reader io.Reader
		if in != nil {
			b, e := json.Marshal(in)
			if e != nil {
				return e
			}
			reader = bytes.NewReader(b)
		}
		r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+"/api/"+path, reader)
		if e != nil {
			return e
		}
		r.Header.Set("Authorization", "Bearer "+c.APIToken)
		r.Header.Set("Content-Type", "application/json")
		if *key != "" {
			r.Header.Set("Idempotency-Key", *key)
		}
		resp, e := a.HTTP.Do(r)
		if e != nil {
			return fmt.Errorf("hub unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
			return fmt.Errorf("hub: %s", strings.TrimSpace(string(data)))
		}
		if out != nil {
			return json.NewDecoder(io.LimitReader(resp.Body, 6<<20)).Decode(out)
		}
		return nil
	}
	_ = dir
	var result any
	switch command {
	case "status":
		err = request("GET", "status", nil, &result)
	case "list":
		err = request("GET", "boxes", nil, &result)
	case "profiles":
		err = request("GET", "profiles", nil, &result)
	case "profile-delete":
		if !*yes {
			return fmt.Errorf("profile deletion requires --yes")
		}
		err = request("DELETE", "profiles/"+url.PathEscape(name), nil, &result)
	case "profile-upload":
		archive, e := a.standaloneProfileArchive(ctx, []string{name})
		if e != nil {
			return e
		}
		defer clear(archive)
		app, _, _ := strings.Cut(name, "=")
		err = request("POST", "profiles", map[string]any{"name": *profileName, "agent": app, "archive": archive}, &result)
	case "create":
		spec := hubservice.Spec{Name: name, CPU: *cpu, Agent: *agent, ModelSource: *modelSource, ModelURL: *endpoint, Model: *model, Goal: *goal, Profiles: profileIDs, AgentModel: *agentModel, AgentReasoningEffort: *agentEffort}
		suppliedMemory := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "model-vision" {
				spec.Vision = vision
			}
		})
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "memory" {
				suppliedMemory = true
			}
		})
		if suppliedMemory {
			spec.MemoryMiB = int64(*memory)
		}
		if !*noDialog && a.IsTerminal != nil && a.IsTerminal() {
			cp := &formField{Label: "CPU", Value: strconv.FormatFloat(func() float64 {
				if spec.CPU > 0 {
					return spec.CPU
				}
				return c.Defaults.CPU
			}(), 'f', -1, 64)}
			ram := &formField{Label: "RAM (MiB)", Value: strconv.FormatInt(func() int64 {
				if spec.MemoryMiB > 0 {
					return spec.MemoryMiB
				}
				return c.Defaults.MemoryMiB
			}(), 10)}
			ag := &formField{Label: "Agent", Value: spec.Agent, Choices: []string{"opencode", "claude", "codex"}}
			source := &formField{Label: "OpenCode configuration", Value: "profile", Choices: []string{"profile", "custom"}, When: func() bool { return ag.Value == "opencode" }}
			if spec.ModelSource != "" {
				source.Value = spec.ModelSource
			} else if spec.ModelURL != "" {
				source.Value = "custom"
			}
			ep := &formField{Label: "Model endpoint", Value: spec.ModelURL, When: func() bool { return ag.Value == "opencode" && source.Value == "custom" }}
			md := &formField{Label: "Model ID", Value: spec.Model, When: ep.When}
			if ep.Value == "" {
				ep.Value = c.Defaults.ModelURL
			}
			if md.Value == "" {
				md.Value = c.Defaults.Model
			}
			vis := &formField{Label: "Model supports images", Value: strconv.FormatBool(c.Defaults.Vision != nil && *c.Defaults.Vision), Choices: []string{"false", "true"}, When: ep.When}
			g := &formField{Label: "Initial goal", Value: spec.Goal}
			fields := []*formField{{Label: "Hub", Value: base, ReadOnly: true}, cp, ram, ag, source, ep, md, vis, g}
			type profileRow struct {
				app, path string
				field     *formField
			}
			var rows []profileRow
			home, _ := os.UserHomeDir()
			profiles, e := components.DiscoverConfigured(home, a.Environ)
			if e != nil {
				return e
			}
			profiles = append(profiles, a.standaloneGitHubProfiles(ctx)...)
			for _, p := range profiles {
				if p.Component != "opencode" && p.Component != "claude" && p.Component != "codex" && p.Component != "github" {
					continue
				}
				f := &formField{Label: p.Component + " / " + p.Name, Value: "Skip", Choices: []string{"Skip", "Import"}, Checkbox: true, Detail: p.Directory}
				fields = append(fields, f)
				rows = append(rows, profileRow{p.Component, p.Directory, f})
			}
			if err = a.runForm(ctx, "Create hub box", fields, func(func(string)) error {
				var e error
				spec.CPU, e = strconv.ParseFloat(cp.Value, 64)
				if e != nil {
					return e
				}
				spec.MemoryMiB, e = strconv.ParseInt(ram.Value, 10, 64)
				if e != nil {
					return e
				}
				spec.Agent = ag.Value
				spec.ModelSource = source.Value
				spec.ModelURL = ""
				spec.Model = ""
				spec.Vision = nil
				if ag.Value == "opencode" && source.Value == "custom" {
					spec.ModelURL = ep.Value
					spec.Model = md.Value
					v := vis.Value == "true"
					spec.Vision = &v
				}
				spec.Goal = g.Value
				for _, p := range rows {
					if p.field.Value == "Import" {
						sources = append(sources, p.app+"="+p.path)
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		for _, source := range sources {
			archive, e := a.standaloneProfileArchive(ctx, []string{source})
			if e != nil {
				return e
			}
			app, _, _ := strings.Cut(source, "=")
			var p hubservice.Profile
			e = request("POST", "profiles", map[string]any{"name": name + " " + app, "agent": app, "archive": archive}, &p)
			clear(archive)
			if e != nil {
				return e
			}
			spec.Profiles = append(spec.Profiles, p.ID)
		}
		if *key == "" {
			*key = hubservice.Token()
		}
		spec.Tools, err = standaloneTools(*tools, *proxy)
		if err != nil {
			return err
		}
		err = request("POST", "boxes", spec, &result)
	case "set":
		var b hubservice.Box
		if err = request("GET", "boxes/"+url.PathEscape(name), nil, &b); err != nil {
			return err
		}
		if *cpu == 0 {
			*cpu = b.Spec.CPU
		}
		ram := b.Spec.MemoryMiB
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "memory" {
				ram = int64(*memory)
			}
			if f.Name == "cpus" {
				*cpu = *cpus
			}
		})
		err = request("POST", "boxes/"+url.PathEscape(name)+"/resources", map[string]any{"cpu": *cpu, "memoryMiB": ram}, &result)
	case "start", "stop", "delete":
		if command == "delete" && !*yes {
			return fmt.Errorf("deleting a box and its files requires --yes")
		}
		err = request("POST", "boxes/"+url.PathEscape(name)+"/"+command, map[string]any{}, &result)
	case "message":
		if *text == "" {
			return fmt.Errorf("--text is required")
		}
		if *key == "" {
			*key = hubservice.Token()
		}
		err = request("POST", "boxes/"+url.PathEscape(name)+"/messages", map[string]string{"text": *text}, &result)
	case "messages":
		err = request("GET", "boxes/"+url.PathEscape(name)+"/messages", nil, &result)
	default:
		return fmt.Errorf("unknown hub command %q", command)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(a.Out).Encode(result)
}

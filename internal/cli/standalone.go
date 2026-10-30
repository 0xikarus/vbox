package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xikarus/vmbox-service/internal/standalone"
)

// This command deliberately bypasses fleet login. Installation subcommands are
// added only when their lifecycle and isolation requirements are implemented.
func (a *App) standalone(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "shutdown" {
		args = append([]string{"stop"}, args[1:]...)
	}
	if len(args) > 1 && args[0] == "links" && !strings.HasPrefix(args[1], "-") {
		return a.standaloneHost(ctx, args)
	}
	if len(args) > 0 {
		switch args[0] {
		case "create", "set", "list", "status", "start", "stop", "delete":
			return a.standaloneHost(ctx, args)
		}
	}
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprintln(a.Out, "Experimental standalone instances:\n  vbox standalone create NAME [--host SSH_ALIAS] --mode container [--agent opencode|claude|codex]\n    [--cpus N --memory SIZE --port PORT --profile APP=PATH --model-url URL --model ID --model-vision --image IMAGE --password-file PATH --tools blender,foundry --proxy none|tor|warp]\n  vbox standalone list | status NAME | set NAME [--cpus N --memory SIZE]\n  vbox standalone links NAME [--base-url URL]\n  vbox standalone start NAME [--port PORT] | stop NAME | delete NAME --yes\n  vbox standalone shutdown NAME [--cleanup --yes]\n  vbox standalone serve --data DIRECTORY [--listen 127.0.0.1:8080]\nVM installation and backup/upgrade workflows are under development.")
		return nil
	}
	fs := flag.NewFlagSet("standalone "+args[0], flag.ContinueOnError)
	fs.SetOutput(a.Err)
	dir := fs.String("data", "", "instance controller directory")
	listen := fs.String("listen", "127.0.0.1:8080", "gateway bind address")
	base := fs.String("base-url", "http://127.0.0.1:8080", "externally reachable gateway URL")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *dir == "" {
		return fmt.Errorf("standalone %s requires --data DIRECTORY", args[0])
	}
	switch args[0] {
	case "serve":
		return standalone.Serve(ctx, *dir, *listen)
	case "links":
		u, err := url.Parse(*base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return fmt.Errorf("base-url must be an HTTP(S) origin")
		}
		b, err := os.ReadFile(filepath.Join(*dir, "config.json"))
		if err != nil {
			return err
		}
		var c standalone.Config
		if err = json.Unmarshal(b, &c); err != nil {
			return err
		}
		result := map[string]string{}
		for _, scope := range []string{"view", "desktop", "chat"} {
			token := c.Capabilities[scope]
			if len(token) != 64 {
				return fmt.Errorf("invalid instance capability")
			}
			u.Path = "/" + scope
			u.Fragment = token
			result[scope] = u.String()
		}
		return json.NewEncoder(a.Out).Encode(result)
	default:
		return fmt.Errorf("standalone subcommand %q is not implemented; run vbox standalone help", args[0])
	}
}

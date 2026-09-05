package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerCoworkers(ctx context.Context, c config.Context, token string, args []string) error {
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	path, method := "/v1/coworkers", http.MethodGet
	var input any
	switch args[0] {
	case "spawn":
		if len(args) < 3 {
			return fmt.Errorf("usage: coworkers spawn BOX claude|codex --prompt TEXT --confirm [--allow-development-channel]")
		}
		fs := flag.NewFlagSet("coworkers spawn", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		prompt := fs.String("prompt", "", "coworker task instructions")
		confirm := fs.Bool("confirm", false, "explicitly launch opted-in coworker")
		development := fs.Bool("allow-development-channel", false, "allow Claude custom channel; startup consent is still required")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if !*confirm || *prompt == "" || fs.NArg() != 0 || (args[2] != "claude" && args[2] != "codex") {
			return fmt.Errorf("spawn requires claude/codex, --prompt TEXT and --confirm")
		}
		path += "/" + url.PathEscape(args[1]) + "/spawn"
		method = http.MethodPost
		input = map[string]any{"agent": args[2], "prompt": *prompt, "confirm": true, "allowDevelopmentChannel": *development}
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: coworkers list [--json]")
		}
	case "status":
		if len(args) != 1 {
			return fmt.Errorf("usage: coworkers status [--json]")
		}
		path += "/settings"
	case "enable", "disable":
		if len(args) != 2 || args[1] != "--confirm" {
			return fmt.Errorf("coworkers %s requires --confirm; enabling permits explicitly spawned coworkers to exchange messages and shared tasks", args[0])
		}
		path += "/settings"
		method = http.MethodPut
		input = map[string]bool{"enabled": args[0] == "enable"}
	default:
		return fmt.Errorf("usage: coworkers list|status|enable --confirm|disable --confirm [--json]")
	}
	var result any
	if _, err := a.request(ctx, c, token, method, path, input, &result, nil); err != nil {
		return err
	}
	return a.providerOutput(result, asJSON)
}

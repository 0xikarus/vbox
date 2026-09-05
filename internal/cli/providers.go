package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerProviders(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: providers list|schema|show|create|update|validate")
	}
	if args[0] == "default" {
		if len(args) != 3 {
			return fmt.Errorf("usage: providers default PROVIDER NAME")
		}
		var value v1.FleetConfig
		_, err := a.request(ctx, c, token, http.MethodPut, "/v1/controller-defaults", v1.FleetConfig{Provider: args[1], ProviderCredential: args[2]}, &value, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(value)
	}
	var out any
	if args[0] == "list" || args[0] == "schema" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return fmt.Errorf("unexpected provider argument")
		}
		path := "/v1/provider-credentials"
		if args[0] == "schema" {
			path = "/v1/provider-schemas"
		}
		if _, err := a.request(ctx, c, token, http.MethodGet, path, nil, &out, nil); err != nil {
			return err
		}
		return json.NewEncoder(a.Out).Encode(out)
	}
	if len(args) < 3 {
		return fmt.Errorf("providers %s requires PROVIDER NAME", args[0])
	}
	path := "/v1/provider-credentials/" + url.PathEscape(args[1]) + "/" + url.PathEscape(args[2])
	fs := flag.NewFlagSet("providers", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	file := fs.String("config-file", "", "config JSON file or - for stdin")
	secretEnv := fs.String("secret-env", "", "environment variable containing JSON secret object")
	revision := fs.String("revision", "", "expected updatedAt; defaults to a fresh read")
	fs.Bool("json", false, "JSON output (always enabled)")
	if err := fs.Parse(args[3:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected provider argument")
	}
	method := http.MethodGet
	var input any
	headers := map[string]string{}
	switch args[0] {
	case "show":
	case "validate":
		method = http.MethodPost
		path += "/validate"
		input = map[string]any{}
	case "create", "update":
		request := map[string]any{}
		if *file != "" {
			var data []byte
			var err error
			if *file == "-" {
				data, err = io.ReadAll(io.LimitReader(a.In, 1<<20))
			} else {
				data, err = os.ReadFile(*file)
			}
			if err != nil {
				return err
			}
			var value map[string]any
			if len(data) > 1<<20 || json.Unmarshal(data, &value) != nil || value == nil {
				return fmt.Errorf("config file must contain a JSON object under 1 MiB")
			}
			request["config"] = value
		}
		if *secretEnv != "" {
			var secret map[string]any
			if json.Unmarshal([]byte(a.Environ[*secretEnv]), &secret) != nil || len(secret) == 0 {
				return fmt.Errorf("secret environment must contain a non-empty JSON object")
			}
			request["secret"] = secret
		}
		if args[0] == "create" {
			method = http.MethodPut
			if request["secret"] == nil {
				return fmt.Errorf("create requires --secret-env")
			}
		} else {
			method = http.MethodPatch
			if request["secret"] != nil {
				request["replaceSecret"] = true
			}
			if *revision == "" {
				var existing v1.ProviderCredential
				if _, err := a.request(ctx, c, token, http.MethodGet, path, nil, &existing, nil); err != nil {
					return err
				}
				*revision = existing.UpdatedAt.Format(time.RFC3339Nano)
			}
			headers["If-Match"] = *revision
		}
		input = request
	default:
		return fmt.Errorf("unsupported provider command %q", args[0])
	}
	if _, err := a.request(ctx, c, token, method, path, input, &out, headers); err != nil {
		return err
	}
	return json.NewEncoder(a.Out).Encode(out)
}

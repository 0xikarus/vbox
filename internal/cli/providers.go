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
	"sort"
	"strings"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) controllerProviders(ctx context.Context, c config.Context, token string, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	jsonOutput := false
	if args[len(args)-1] == "--json" {
		jsonOutput = true
		args = args[:len(args)-1]
		if len(args) == 0 {
			args = []string{"list"}
		}
	}
	if len(args) == 1 && (args[0] == "show" || args[0] == "validate") {
		value, err := a.pickProvider(ctx, c, token)
		if err != nil {
			return err
		}
		args = append(args, value.Provider, value.Name)
	}
	if args[0] == "create" && len(args) < 3 {
		requested := ""
		if len(args) == 2 {
			requested = args[1]
		}
		value, err := a.setupProvider(ctx, c, token, requested)
		if err != nil {
			return err
		}
		return a.providerOutput(value, jsonOutput)
	}
	if args[0] == "default" {
		if len(args) == 1 {
			value, err := a.chooseDefaultProvider(ctx, c, token)
			if err != nil {
				return err
			}
			return a.providerOutput(value, jsonOutput)
		}
		if len(args) != 3 {
			return fmt.Errorf("usage: providers default PROVIDER NAME")
		}
		var value v1.FleetConfig
		_, err := a.request(ctx, c, token, http.MethodPut, "/v1/controller-defaults", v1.FleetConfig{Provider: args[1], ProviderCredential: args[2]}, &value, nil)
		if err != nil {
			return err
		}
		return a.providerOutput(value, jsonOutput)
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
		return a.providerOutput(out, jsonOutput)
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
	fs.BoolVar(&jsonOutput, "json", jsonOutput, "JSON output")
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
	return a.providerOutput(out, jsonOutput)
}

func (a *App) providerOutput(value any, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(a.Out).Encode(value)
	}
	// Normalize typed responses and decoded API objects through the same renderer.
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	return a.writeSummary(decoded, "")
}

func (a *App) writeSummary(value any, indent string) error {
	switch v := value.(type) {
	case []any:
		if len(v) == 0 {
			_, err := fmt.Fprintln(a.Out, indent+"None configured.")
			return err
		}
		for _, item := range v {
			// Lists stay compact; a single provider's show response includes
			// its public configuration so users can actually inspect the setup.
			if row, ok := item.(map[string]any); ok && row["state"] == nil {
				provider, hasProvider := row["provider"].(string)
				name, hasName := row["name"].(string)
				if hasProvider && hasName {
					if _, err := fmt.Fprintf(a.Out, "%s%s · %s\n", indent, tuiLabel(provider, 100), tuiLabel(name, 100)); err != nil {
						return err
					}
					continue
				}
			}
			if err := a.writeSummary(item, indent); err != nil {
				return err
			}
		}
	case map[string]any:
		if provider, ok := v["provider"].(string); ok && v["state"] == nil {
			if name, ok := v["name"].(string); ok {
				if _, err := fmt.Fprintf(a.Out, "%s%s · %s\n", indent, tuiLabel(provider, 100), tuiLabel(name, 100)); err != nil {
					return err
				}
				if config, ok := v["config"].(map[string]any); ok {
					if err := a.writeSummary(config, indent+"  "); err != nil {
						return err
					}
				}
				if a.Verbose {
					for _, key := range []string{"id", "accountId", "createdAt", "updatedAt"} {
						if v[key] != nil {
							if err := a.writeSummary(v[key], indent+"  "+key+": "); err != nil {
								return err
							}
						}
					}
				}
				return nil
			}
		}
		for _, key := range sortedSummaryKeys(v) {
			if err := a.writeSummary(v[key], indent+tuiLabel(key, 100)+": "); err != nil {
				return err
			}
		}
	default:
		_, err := fmt.Fprintln(a.Out, indent+strings.TrimSpace(tuiLabel(fmt.Sprint(v), 500)))
		return err
	}
	return nil
}

func sortedSummaryKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/config"
)

func (a *App) pickProvider(ctx context.Context, c config.Context, token string) (v1.ProviderCredential, error) {
	if a.IsTerminal == nil || !a.IsTerminal() {
		return v1.ProviderCredential{}, fmt.Errorf("specify TYPE ALIAS; use vmbox pools list to see configured worker pools")
	}
	var values []v1.ProviderCredential
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/provider-credentials", nil, &values, nil); err != nil {
		return v1.ProviderCredential{}, err
	}
	if len(values) == 0 {
		return a.setupProvider(ctx, c, token, "")
	}
	labels := make([]string, len(values))
	for i, value := range values {
		labels[i] = value.Provider + " · " + value.Name
	}
	i, err := a.selectTUI(ctx, "Choose a worker pool", labels, 0)
	if err != nil {
		return v1.ProviderCredential{}, err
	}
	return values[i], nil
}

// The controller owns provider schemas and credentials. The CLI has no
// provider SDK, token format assumptions, or provisioning operations.
func (a *App) setupProvider(ctx context.Context, c config.Context, token, requested string) (v1.ProviderCredential, error) {
	var zero v1.ProviderCredential
	if a.IsTerminal == nil || !a.IsTerminal() {
		return zero, fmt.Errorf("use vmbox pools create TYPE ALIAS --config-file FILE --secret-env ENV; see vmbox pools schema")
	}
	var schema struct {
		Providers   map[string]map[string]string `json:"providers"`
		Required    map[string][]string          `json:"required"`
		SecretInput string                       `json:"secretInput"`
	}
	if _, err := a.request(ctx, c, token, http.MethodGet, "/v1/provider-schemas", nil, &schema, nil); err != nil {
		return zero, err
	}
	names := make([]string, 0, len(schema.Providers))
	for name := range schema.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if requested == "" {
		i, err := a.selectTUI(ctx, "Set up a worker pool type", names, 0)
		if err != nil {
			return zero, err
		}
		requested = names[i]
	}
	fields, ok := schema.Providers[requested]
	if !ok {
		return zero, fmt.Errorf("worker pool type is not supported by this controller; use vmbox pools schema")
	}
	reader := bufio.NewReader(singleByteReader{a.In})
	alias, err := a.readControllerPrompt(reader, "Worker pool alias", "primary")
	if err != nil {
		return zero, err
	}
	configValue, err := a.promptProviderFields(reader, fields, schema.Required[requested])
	if err != nil {
		return zero, err
	}
	fmt.Fprintln(a.Err, "Secret format: "+tuiLabel(schema.SecretInput, 500))
	env, err := a.readControllerPrompt(reader, "Environment variable containing secret JSON", "VMBOX_PROVIDER_SECRET")
	if err != nil {
		return zero, err
	}
	secretJSON := a.Environ[env]
	if secretJSON == "" {
		secretJSON, err = a.readSecret("Provider secret JSON")
		if err != nil {
			return zero, err
		}
	}
	var secret map[string]any
	if json.Unmarshal([]byte(secretJSON), &secret) != nil || len(secret) == 0 {
		return zero, fmt.Errorf("provider secret must be a non-empty JSON object")
	}
	choice, err := a.selectTUI(ctx, "Create "+requested+" · "+alias+" on the controller?", []string{"Confirm", "Cancel"}, 1)
	if err != nil {
		return zero, err
	}
	if choice != 0 {
		return zero, fmt.Errorf("provider setup cancelled; nothing created")
	}
	var result v1.ProviderCredential
	_, err = a.request(ctx, c, token, http.MethodPut, "/v1/provider-credentials/"+url.PathEscape(requested)+"/"+url.PathEscape(alias), map[string]any{"config": configValue, "secret": secret}, &result, nil)
	return result, err
}

// Fields and requiredness come from the controller, including new providers.
func (a *App) promptProviderFields(reader *bufio.Reader, fields map[string]string, required []string) (map[string]any, error) {
	keys := make([]string, 0, len(fields))
	needed := map[string]bool{}
	for _, key := range required {
		needed[key] = true
	}
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if needed[keys[i]] != needed[keys[j]] {
			return needed[keys[i]]
		}
		return keys[i] < keys[j]
	})
	values := map[string]any{}
	for _, key := range keys {
		hint := "optional, Enter to skip"
		if needed[key] {
			hint = "required"
		}
		fmt.Fprintf(a.Err, "%s (%s; %s): ", tuiLabel(key, 100), tuiLabel(fields[key], 40), hint)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("could not read provider field")
		}
		value := strings.TrimSpace(line)
		if value == "" {
			if needed[key] {
				return nil, fmt.Errorf("%s is required", tuiLabel(key, 100))
			}
			if err == io.EOF {
				return nil, fmt.Errorf("provider setup input ended; nothing created")
			}
			continue
		}
		switch fields[key] {
		case "string":
			values[key] = value
		case "boolean":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("%s must be true or false", tuiLabel(key, 100))
			}
			values[key] = parsed
		default:
			return nil, fmt.Errorf("unsupported provider field type; use explicit --config-file")
		}
	}
	return values, nil
}

func (a *App) chooseDefaultProvider(ctx context.Context, c config.Context, token string) (v1.FleetConfig, error) {
	value, err := a.pickProvider(ctx, c, token)
	if err != nil {
		return v1.FleetConfig{}, err
	}
	choice, err := a.selectTUI(ctx, "Use worker pool "+value.Provider+" · "+value.Name+" as the controller default?", []string{"Confirm", "Cancel"}, 1)
	if err != nil {
		return v1.FleetConfig{}, err
	}
	if choice != 0 {
		return v1.FleetConfig{}, fmt.Errorf("provider selection cancelled; no default changed")
	}
	var result v1.FleetConfig
	_, err = a.request(ctx, c, token, http.MethodPut, "/v1/controller-defaults", v1.FleetConfig{Provider: value.Provider, ProviderCredential: value.Name}, &result, nil)
	return result, err
}

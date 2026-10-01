package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

// providerField describes one provider setting so clients can render a form
// instead of asking owners for JSON. Secret fields go into the encrypted secret
// and are never returned; only Mutable fields may change after creation.
type providerField struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	Mutable     bool     `json:"mutable,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
}

type providerType struct {
	Name   string          `json:"name"`
	Label  string          `json:"label"`
	Help   string          `json:"help,omitempty"`
	Fields []providerField `json:"fields"`
}

var imageField = providerField{Name: "image", Label: "Worker image", Type: "string", Mutable: true, Placeholder: "registry/image@sha256:…", Help: "Optional. Applies to future boxes only."}

var providerTypes = []providerType{
	{Name: "shared-worker", Label: "Shared worker (your Linux server)", Help: "A vmbox-shared-worker installed on a machine. Slots and box sizes are set from its card after saving.", Fields: []providerField{
		{Name: "endpoint", Label: "Worker URL", Type: "string", Required: true, Placeholder: "https://203.0.113.10", Help: "HTTPS origin of the worker."},
		{Name: "token", Label: "Worker token", Type: "string", Required: true, Secret: true, Help: "VMBOX_SHARED_TOKEN from the worker's configuration."},
	}},
	{Name: "railway", Label: "Railway", Help: "One Railway service and volume per compute slot.", Fields: []providerField{
		{Name: "projectId", Label: "Project ID", Type: "string", Required: true},
		{Name: "environmentId", Label: "Environment ID", Type: "string", Required: true},
		{Name: "tokenEnvironment", Label: "Token type", Type: "select", Options: []string{"RAILWAY_API_TOKEN", "RAILWAY_TOKEN"}, Help: "Account/team token or project token."},
		imageField,
		{Name: "token", Label: "Railway token", Type: "string", Required: true, Secret: true},
	}},
	{Name: "docker", Label: "Docker engine", Fields: []providerField{
		{Name: "context", Label: "Docker context", Type: "string"},
		{Name: "host", Label: "Docker host", Type: "string", Placeholder: "ssh://user@host"},
		{Name: "tlsVerify", Label: "Verify TLS", Type: "boolean"},
		{Name: "certPath", Label: "TLS certificate directory", Type: "string"},
		imageField,
	}},
	{Name: "incus", Label: "Incus", Fields: []providerField{
		{Name: "remote", Label: "Remote", Type: "string"},
		{Name: "project", Label: "Project", Type: "string"},
		{Name: "vm", Label: "Use virtual machines", Type: "boolean"},
		imageField,
	}},
}

// providerSchemas is the non-secret config allowlist, derived from providerTypes.
var providerSchemas, providerRequired = func() (map[string]map[string]string, map[string][]string) {
	schemas, required := map[string]map[string]string{}, map[string][]string{}
	for _, typ := range providerTypes {
		schemas[typ.Name] = map[string]string{}
		for _, field := range typ.Fields {
			if field.Secret {
				continue
			}
			kind := field.Type
			if kind == "select" {
				kind = "string"
			}
			schemas[typ.Name][field.Name] = kind
			if field.Required {
				required[typ.Name] = append(required[typ.Name], field.Name)
			}
		}
	}
	return schemas, required
}()

func publicProviderConfig(name string, raw json.RawMessage) json.RawMessage {
	var input map[string]any
	_ = json.Unmarshal(raw, &input)
	out := map[string]any{}
	for key, typ := range providerSchemas[name] {
		v := input[key]
		switch typ {
		case "string":
			if _, ok := v.(string); ok {
				out[key] = v
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				out[key] = v
			}
		}
	}
	data, _ := json.Marshal(out)
	return data
}

// An allowlist prevents credential fields (including nested objects) leaking
// through publicly readable configuration. Provider-specific rules stay here.
func validateProviderConfig(name string, raw json.RawMessage) error {
	schema, ok := providerSchemas[name]
	if !ok {
		return fmt.Errorf("unsupported provider")
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("config must be a JSON object")
	}
	for k, v := range values {
		typ, ok := schema[k]
		if !ok {
			return fmt.Errorf("config contains an unsupported field; use provider schema (secrets belong in secret)")
		}
		switch typ {
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("config field %s must be string", k)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("config field %s must be boolean", k)
			}
		}
	}
	for _, k := range providerRequired[name] {
		if values[k] == nil || values[k] == "" {
			return fmt.Errorf("%s config requires %s", name, k)
		}
	}
	if name == "railway" {
		if v, ok := values["tokenEnvironment"]; ok && v != "RAILWAY_TOKEN" && v != "RAILWAY_API_TOKEN" {
			return fmt.Errorf("invalid tokenEnvironment")
		}
	}
	return nil
}

func (s *Server) providerSchemasHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, 200, map[string]any{"types": providerTypes, "providers": providerSchemas, "required": providerRequired, "secretInput": "JSON object; railway and shared-worker require token", "update": "PATCH with If-Match=updatedAt; null deletes config fields; omitted secret preserved; replaceSecret required", "retarget": "Target fields are immutable; create a new alias. Deletion requires reviewed migration."})
}

func (s *Server) providerShowHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	values, err := s.Store.ListProviderCredentials(r.Context(), p.AccountID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for _, v := range values {
		if v.Provider == r.PathValue("provider") && v.Name == r.PathValue("name") {
			w.Header().Set("ETag", v.UpdatedAt.Format(time.RFC3339Nano))
			writeJSON(w, 200, v)
			return
		}
	}
	writeError(w, 404, fmt.Errorf("provider alias not found"))
}

type patchProviderRequest struct {
	Config        map[string]json.RawMessage `json:"config,omitempty"`
	Secret        json.RawMessage            `json:"secret,omitempty"`
	ReplaceSecret bool                       `json:"replaceSecret,omitempty"`
}

func (s *Store) PatchProvider(ctx context.Context, p Principal, name, alias, revision string, req patchProviderRequest) (v1.ProviderCredential, error) {
	if revision == "" {
		return v1.ProviderCredential{}, fmt.Errorf("If-Match updatedAt revision required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return v1.ProviderCredential{}, err
	}
	defer tx.Rollback()
	var v v1.ProviderCredential
	var sealed string
	err = tx.QueryRowContext(ctx, `SELECT id::text,account_id::text,provider,name,config,encrypted_value,created_at,updated_at FROM provider_credentials WHERE account_id=$1 AND provider=$2 AND name=$3 FOR UPDATE`, p.AccountID, name, alias).Scan(&v.ID, &v.AccountID, &v.Provider, &v.Name, &v.Config, &sealed, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return v, fmt.Errorf("provider alias not found")
	}
	if v.UpdatedAt.Format(time.RFC3339Nano) != revision {
		return v, fmt.Errorf("provider revision conflict")
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(v.Config, &config); err != nil {
		return v, err
	}
	for k, value := range req.Config {
		// Target changes use new aliases, eliminating races with allocation and
		// credential resolution. Image changes affect future operations only.
		if k != "image" && string(config[k]) != string(value) {
			return v, fmt.Errorf("target/config field %s is immutable; create a new alias and explicitly migrate resources", k)
		}
		if string(value) == "null" {
			delete(config, k)
		} else {
			config[k] = value
		}
	}
	v.Config, err = json.Marshal(config)
	if err != nil {
		return v, err
	}
	if err = validateProviderConfig(name, v.Config); err != nil {
		return v, err
	}
	if len(req.Secret) > 0 {
		if !req.ReplaceSecret || !validJSONObject(req.Secret, true) {
			return v, fmt.Errorf("explicit replaceSecret and non-empty secret object required")
		}
		if s.Envelope == nil {
			return v, fmt.Errorf("encryption unavailable")
		}
		sealed, err = s.Envelope.Seal(p.AccountID, req.Secret)
		if err != nil {
			return v, fmt.Errorf("secret encryption failed")
		}
	} else if req.ReplaceSecret {
		return v, fmt.Errorf("replacement secret required")
	}
	err = tx.QueryRowContext(ctx, `UPDATE provider_credentials SET config=$4,encrypted_value=$5,updated_at=clock_timestamp() WHERE account_id=$1 AND provider=$2 AND name=$3 RETURNING updated_at`, p.AccountID, name, alias, v.Config, sealed).Scan(&v.UpdatedAt)
	if err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	return v, nil
}

func (s *Server) providerPatchHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var req patchProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, fmt.Errorf("invalid provider edit JSON"))
		return
	}
	v, err := s.Store.PatchProvider(r.Context(), p, r.PathValue("provider"), r.PathValue("name"), r.Header.Get("If-Match"), req)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) providerValidateHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	prov, err := s.provider(ctx, p.AccountID, r.PathValue("provider"), r.PathValue("name"))
	if err != nil {
		writeError(w, 422, fmt.Errorf("provider configuration or credentials invalid"))
		return
	}
	// List is a read-only authentication/connectivity probe, not a claim of
	// create/delete permission. Do not call provider validation that may mutate.
	_, err = prov.List(ctx)
	if err != nil {
		writeError(w, 422, fmt.Errorf("provider read-only inventory probe failed"))
		return
	}
	writeJSON(w, 200, map[string]any{"valid": true, "checked": []string{"read inventory"}, "unchecked": []string{"create", "delete", "attach volume", "SSH authentication"}})
}

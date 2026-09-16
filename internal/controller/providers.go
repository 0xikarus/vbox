package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
)

var providerSchemas = map[string]map[string]string{
	"shared-worker": {"endpoint": "string"},
	"railway":       {"projectId": "string", "environmentId": "string", "tokenEnvironment": "string", "image": "string"},
	"docker":        {"context": "string", "host": "string", "tlsVerify": "boolean", "certPath": "string", "image": "string"},
	"incus":         {"remote": "string", "project": "string", "vm": "boolean", "image": "string"},
}

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
	if name == "railway" {
		for _, k := range []string{"projectId", "environmentId"} {
			if values[k] == nil || values[k] == "" {
				return fmt.Errorf("Railway config requires %s", k)
			}
		}
		if v, ok := values["tokenEnvironment"]; ok && v != "RAILWAY_TOKEN" && v != "RAILWAY_API_TOKEN" {
			return fmt.Errorf("invalid tokenEnvironment")
		}
	}
	return nil
}

func (s *Server) providerSchemasHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, 200, map[string]any{"providers": providerSchemas, "required": map[string][]string{"railway": {"projectId", "environmentId"}}, "secretInput": "JSON object; railway requires token", "update": "PATCH with If-Match=updatedAt; null deletes config fields; omitted secret preserved; replaceSecret required", "retarget": "Target fields are immutable; create a new alias. Deletion requires reviewed migration."})
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

package browser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"
)

const MaxStateBytes = 1 << 20

// StateImport is a private credential payload, never a tool result. Version 1
// supports exact-host cookies and string localStorage entries on HTTPS origins.
// Domain cookies, IndexedDB and sessionStorage need separate explicit formats.
type StateImport struct {
	Version int           `json:"version"`
	Origins []OriginState `json:"origins"`
}

type OriginState struct {
	Origin         string          `json:"origin"`
	Cookies        []StateCookie   `json:"cookies,omitempty"`
	LocalStorage   []StorageEntry  `json:"localStorage,omitempty"`
	IndexedDB      json.RawMessage `json:"indexedDB,omitempty"`
	SessionStorage json.RawMessage `json:"sessionStorage,omitempty"`
}

type StorageEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type StateCookie struct {
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Path     string   `json:"path"`
	HTTPOnly bool     `json:"httpOnly,omitempty"`
	SameSite string   `json:"sameSite,omitempty"`
	Expires  *float64 `json:"expires,omitempty"`
}

func DecodeStateImport(data []byte) (StateImport, error) {
	var state StateImport
	if len(data) == 0 || len(data) > MaxStateBytes || !utf8.Valid(data) {
		return state, fmt.Errorf("browser state must be UTF-8 JSON up to 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF {
		return StateImport{}, fmt.Errorf("invalid browser state format or unsupported fields")
	}
	if err := state.Validate(); err != nil {
		return StateImport{}, err
	}
	return state, nil
}

func (s StateImport) Validate() error {
	if s.Version != 1 || len(s.Origins) == 0 || len(s.Origins) > 32 {
		return fmt.Errorf("browser state version 1 requires 1–32 origins")
	}
	origins := map[string]bool{}
	cookies, entries := 0, 0
	for _, site := range s.Origins {
		normalized, err := Origin(site.Origin)
		if err != nil || normalized != site.Origin || origins[normalized] {
			return fmt.Errorf("browser state requires unique canonical HTTPS origins")
		}
		origins[normalized] = true
		if len(site.IndexedDB) > 0 || len(site.SessionStorage) > 0 {
			return fmt.Errorf("IndexedDB and sessionStorage imports are not supported in state version 1")
		}
		cookieNames := map[string]bool{}
		for _, cookie := range site.Cookies {
			cookies++
			if cookies > 1024 {
				return fmt.Errorf("browser state exceeds 1024 cookies")
			}
			if cookie.Path == "" || !strings.HasPrefix(cookie.Path, "/") || len(cookie.Name)+len(cookie.Value) > 4096 || len(cookie.Path) > 1024 {
				return fmt.Errorf("invalid cookie size or path")
			}
			canonical := http.Cookie{Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, Secure: true}
			if canonical.Valid() != nil {
				return fmt.Errorf("invalid cookie syntax")
			}
			if strings.HasPrefix(cookie.Name, "__Host-") && cookie.Path != "/" {
				return fmt.Errorf("host-prefixed cookies require the root path")
			}
			if cookie.SameSite != "" && cookie.SameSite != "Strict" && cookie.SameSite != "Lax" && cookie.SameSite != "None" {
				return fmt.Errorf("invalid cookie SameSite policy")
			}
			if cookie.Expires != nil && (math.IsNaN(*cookie.Expires) || math.IsInf(*cookie.Expires, 0) || *cookie.Expires <= 0 || *cookie.Expires > 253402300799) {
				return fmt.Errorf("cookie expiration must be a positive Unix timestamp")
			}
			key := cookie.Name + "\x00" + cookie.Path
			if cookieNames[key] {
				return fmt.Errorf("duplicate cookie binding")
			}
			cookieNames[key] = true
		}
		names := map[string]bool{}
		for _, entry := range site.LocalStorage {
			entries++
			if entries > 4096 || len(entry.Name) > 4096 || len(entry.Value) > 256*1024 {
				return fmt.Errorf("localStorage import limit exceeded")
			}
			if names[entry.Name] {
				return fmt.Errorf("duplicate localStorage key")
			}
			names[entry.Name] = true
		}
	}
	return nil
}

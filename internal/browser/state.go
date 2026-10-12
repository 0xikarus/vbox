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

const MaxStateBytes = 8 << 20

// StateImport is a private credential payload, never a tool result. Version 1
// supports exact-host cookies and string localStorage entries on HTTPS origins.
// Version 2 also accepts domain cookies exported from a local Chromium profile.
// IndexedDB and sessionStorage need separate explicit formats.
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
	Domain   string   `json:"domain,omitempty"`
	HTTPOnly bool     `json:"httpOnly,omitempty"`
	SameSite string   `json:"sameSite,omitempty"`
	Expires  *float64 `json:"expires,omitempty"`
}

func DecodeStateImport(data []byte) (StateImport, error) {
	var state StateImport
	if len(data) == 0 || len(data) > MaxStateBytes || !utf8.Valid(data) {
		return state, fmt.Errorf("browser state must be UTF-8 JSON up to 8 MiB")
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
	maxOrigins := 32
	if s.Version == 2 {
		maxOrigins = 512
	}
	if (s.Version != 1 && s.Version != 2) || len(s.Origins) == 0 || len(s.Origins) > maxOrigins {
		return fmt.Errorf("browser state requires version 1 (1–32 origins) or version 2 (1–512 origins)")
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
			return fmt.Errorf("IndexedDB and sessionStorage imports are not supported")
		}
		cookieNames := map[string]bool{}
		for _, cookie := range site.Cookies {
			cookies++
			maxCookies := 1024
			if s.Version == 2 {
				maxCookies = 10000
			}
			if cookies > maxCookies {
				return fmt.Errorf("browser state exceeds cookie limit")
			}
			if cookie.Domain != "" {
				if s.Version != 2 || !validCookieDomain(site.Origin, cookie.Domain) {
					return fmt.Errorf("cookie domain does not match HTTPS origin")
				}
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
			if strings.HasPrefix(cookie.Name, "__Host-") && cookie.Domain != "" {
				return fmt.Errorf("host-prefixed cookies cannot have a domain")
			}
			if cookie.SameSite != "" && cookie.SameSite != "Strict" && cookie.SameSite != "Lax" && cookie.SameSite != "None" {
				return fmt.Errorf("invalid cookie SameSite policy")
			}
			if cookie.Expires != nil && (math.IsNaN(*cookie.Expires) || math.IsInf(*cookie.Expires, 0) || *cookie.Expires <= 0 || *cookie.Expires > 253402300799) {
				return fmt.Errorf("cookie expiration must be a positive Unix timestamp")
			}
			key := cookie.Name + "\x00" + cookie.Path + "\x00" + cookie.Domain
			if cookieNames[key] {
				return fmt.Errorf("duplicate cookie binding")
			}
			cookieNames[key] = true
		}
		names := map[string]bool{}
		for _, entry := range site.LocalStorage {
			entries++
			maxEntries := 4096
			if s.Version == 2 {
				maxEntries = 10000
			}
			if entries > maxEntries || len(entry.Name) > 4096 || len(entry.Value) > 256*1024 {
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

func validCookieDomain(origin, domain string) bool {
	// Exported domain cookies retain their leading dot. Requiring the origin
	// host to be the cookie domain prevents moving one site's cookie to another.
	if !strings.HasPrefix(domain, ".") || strings.Count(domain, ".") < 2 {
		return false
	}
	host := strings.TrimPrefix(domain, ".")
	return origin == "https://"+host && host == strings.ToLower(host) &&
		!strings.ContainsAny(host, ":/\\ ") && !strings.Contains(host, "..")
}

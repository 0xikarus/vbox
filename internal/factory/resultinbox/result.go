package resultinbox

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// Result is a worker's report, never verified completion. ExitCode must be an
// actual process exit status (0..255), or null with a nonempty Signal. Document
// is optional inert JSON: no paths are opened and no URLs are fetched.
type Result struct {
	Version   int             `json:"version"`
	AttemptID string          `json:"attemptID"`
	ExitCode  *int            `json:"exitCode"`
	Signal    *string         `json:"signal,omitempty"`
	Document  json.RawMessage `json:"document,omitempty"`
	Truncated bool            `json:"truncated"`
}

// Decode validates the strict version 1 envelope. Duplicate and unknown envelope
// keys, missing required fields, and trailing JSON are rejected.
func Decode(body []byte) (Result, error) {
	var r Result
	if len(body) > MaxBodyBytes {
		return r, ErrTooLarge
	}
	if !utf8.Valid(body) {
		return r, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return r, ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return r, ErrInvalid
		}
		name, ok := key.(string)
		if !ok {
			return r, ErrInvalid
		}
		switch name {
		case "version", "attemptID", "exitCode", "signal", "document", "truncated":
		default:
			return r, ErrInvalid
		}
		if _, ok := fields[name]; ok {
			return r, ErrInvalid
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return r, ErrInvalid
		}
		fields[name] = v
	}
	if _, err = d.Token(); err != nil {
		return r, ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return r, ErrInvalid
	}
	for _, key := range []string{"version", "attemptID", "exitCode", "truncated"} {
		v, ok := fields[key]
		if !ok || (key != "exitCode" && bytes.Equal(v, []byte("null"))) {
			return r, ErrInvalid
		}
	}
	if json.Unmarshal(body, &r) != nil || r.Version != 1 || !validID(r.AttemptID) {
		return Result{}, ErrInvalid
	}
	if r.ExitCode == nil {
		if r.Signal == nil || !validID(*r.Signal) {
			return Result{}, ErrInvalid
		}
	} else if *r.ExitCode < 0 || *r.ExitCode > 255 || r.Signal != nil {
		return Result{}, ErrInvalid
	}
	return r, nil
}

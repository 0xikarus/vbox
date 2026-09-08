package reviewer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const schema = `{"type":"object","additionalProperties":false,"required":["candidateSha","summary","blockingFindings","approved"],"properties":{"candidateSha":{"type":"string"},"summary":{"type":"string"},"blockingFindings":{"type":"array","items":{"type":"string"}},"approved":{"type":"boolean"}}}`

func decode(b []byte, sha string) (Review, error) {
	var r Review
	// Token walk rejects duplicate keys as well as trailing documents.
	d := json.NewDecoder(bytes.NewReader(b))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return r, errors.New("malformed review")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return r, errors.New("malformed review")
		}
		key, ok := k.(string)
		if !ok {
			return r, errors.New("invalid key")
		}
		if _, ok := fields[key]; ok {
			return r, errors.New("duplicate review field")
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return r, errors.New("malformed field")
		}
		fields[key] = v
	}
	if _, e = d.Token(); e != nil {
		return r, errors.New("truncated review")
	}
	if _, e = d.Token(); e != io.EOF {
		return r, errors.New("trailing review data")
	}
	if len(fields) != 4 {
		return r, errors.New("review fields missing or unknown")
	}
	for _, k := range []string{"candidateSha", "summary", "blockingFindings", "approved"} {
		v, ok := fields[k]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return r, errors.New("required review field missing or null")
		}
	}
	if json.Unmarshal(b, &r) != nil || r.CandidateSHA != sha || len(strings.TrimSpace(r.Summary)) < 20 || r.BlockingFindings == nil {
		return r, errors.New("invalid or wrong-candidate review")
	}
	for _, s := range r.BlockingFindings {
		if len(strings.TrimSpace(s)) < 10 {
			return r, errors.New("empty blocking finding")
		}
	}
	if r.Approved && len(r.BlockingFindings) > 0 {
		return r, errors.New("approval contradicts blocking findings")
	}
	return r, nil
}

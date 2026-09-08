package buildjob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Reject duplicate keys at every level, including case aliases understood by Go.
func uniqueJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("JSON nesting limit")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if t == nil {
			return nil
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok {
					return fmt.Errorf("invalid key")
				}
				key = strings.ToLower(key)
				if seen[key] {
					return fmt.Errorf("duplicate key")
				}
				seen[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			_, e = d.Token()
			return e
		case json.Delim('['):
			for d.More() {
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := value(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

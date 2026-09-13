package boxruntime

import (
	"strings"
	"testing"
)

func TestDesktopPasswordPayloadIsBoundedAndPrivate(t *testing.T) {
	for _, data := range []string{
		`{"origin":"https://site.test","value":"synthetic-private"}`,
		`{"origin":"http://site.test","value":"synthetic-private"}`,
		`{"origin":"https://site.test/path","value":"synthetic-private"}`,
		`{"origin":"https://site.test","value":""}`,
		`{"origin":"https://site.test","value":"synthetic-private","extra":true}`,
		`{"origin":"https://site.test","value":"synthetic-private"} {}`,
		`{"origin":"https://site.test","value":"` + strings.Repeat("x", 4097) + `"}`,
		`synthetic-private`,
	} {
		request, err := DecodeDesktopPassword([]byte(data))
		valid := data == `{"origin":"https://site.test","value":"synthetic-private"}`
		if (err == nil) != valid {
			t.Fatalf("request validation mismatch: %v", err)
		}
		if err != nil && (strings.Contains(err.Error(), "synthetic-private") || request.Value != "") {
			t.Fatal("failed decode exposed private input")
		}
	}
}

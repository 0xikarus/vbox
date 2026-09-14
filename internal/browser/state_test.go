package browser

import (
	"strings"
	"testing"
)

func TestStateImportValidationAndPrivateErrors(t *testing.T) {
	good := `{"version":1,"origins":[{"origin":"https://site.test","cookies":[{"name":"session","value":"synthetic-private","path":"/","httpOnly":true}],"localStorage":[{"name":"token","value":"synthetic-private"}]}]}`
	if _, err := DecodeStateImport([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(good, "https://site.test", "http://site.test", 1),
		strings.Replace(good, "https://site.test", "https://site.test/path", 1),
		strings.Replace(good, `"version":1`, `"version":2`, 1),
		strings.Replace(good, `"path":"/"`, `"path":"/","domain":"other.test"`, 1),
		strings.Replace(good, `"cookies":`, `"indexedDB":[],"cookies":`, 1),
		strings.Replace(good, `"cookies":`, `"sessionStorage":[],"cookies":`, 1),
		strings.Replace(good, `"httpOnly":true`, `"sameSite":"bogus"`, 1),
		strings.Replace(good, `"name":"session"`, `"name":"bad name"`, 1),
		good + ` {}`,
	} {
		state, err := DecodeStateImport([]byte(invalid))
		if err == nil {
			t.Fatal("unsupported state accepted")
		}
		if len(state.Origins) != 0 || strings.Contains(err.Error(), "synthetic-private") {
			t.Fatal("invalid payload exposed")
		}
	}
}

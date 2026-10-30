package boxruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneTransportRequiresExplicitModeAndLoopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "vmbox")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		mode, url string
		ok        bool
	}{{"", "http://127.0.0.1:8080", false}, {"standalone", "http://127.0.0.1:8080", true}, {"standalone", "http://192.168.1.2:8080", false}, {"standalone", "http://127.0.0.1:8080/proxy", false}, {"", "https://controller.example", true}} {
		b, _ := json.Marshal(DesktopAgentConfig{Mode: tc.mode, Controller: tc.url, Assignment: "test", Token: strings.Repeat("a", 64)})
		if e := os.WriteFile(filepath.Join(dir, "desktop-agent.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
		_, e := readDesktopAgentConfig("test")
		if (e == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, e)
		}
	}
}

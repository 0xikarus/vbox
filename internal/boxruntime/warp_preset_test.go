package boxruntime

import (
	"strings"
	"testing"
)

func TestWarpAptEntryUsesSupportedDistroCodename(t *testing.T) {
	entry, err := warpAptEntry([]byte("ID=debian\nVERSION_CODENAME=bookworm\n"))
	if err != nil || !strings.Contains(entry, "https://pkg.cloudflareclient.com/ bookworm main") || !strings.Contains(entry, "signed-by="+warpKeyring) {
		t.Fatalf("entry=%q err=%v", entry, err)
	}
	for _, release := range []string{"ID=alpine\nVERSION_CODENAME=bookworm\n", "ID=ubuntu\nVERSION_CODENAME=noble;rm -rf /\n", "ID=debian\n"} {
		if _, err := warpAptEntry([]byte(release)); err == nil {
			t.Fatalf("accepted unsupported release %q", release)
		}
	}
}

func TestWarpCooldownOnlyAppliesToEdgeHandshakeFailures(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"unavailable: Cloudflare WARP could not connect; check Cloudflare edge UDP access\n", true},
		{"unavailable: warp-cli registration new: exit status 1\n", false},
		{"unavailable: WARP service unavailable: context deadline exceeded\n", false},
		{"connected\n", false},
	} {
		if got := warpEdgeHandshakeFailed(tc.status); got != tc.want {
			t.Errorf("status %q: got %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestStandaloneDebianDevelopmentUsesPublishedWarpRepository(t *testing.T) {
	for _, suite := range []string{"sid", "forky"} {
		entry, err := warpAptEntry([]byte("ID=debian\nVERSION_CODENAME=" + suite + "\n"))
		if err != nil || !strings.Contains(entry, " trixie main") {
			t.Fatalf("%s: %s %v", suite, entry, err)
		}
	}
}

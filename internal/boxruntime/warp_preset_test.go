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

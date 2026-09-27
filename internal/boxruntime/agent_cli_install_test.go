package boxruntime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAgentCLIUsesPersistentHomeAndVerifiesVersion(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	npm := `#!/bin/sh
if [ "$1" != install ] || [ "$2" != --global ] || [ "$5" != --prefix ] || [ "$7" != opencode-ai@1.2.3 ]; then exit 31; fi
mkdir -p "$6/bin"
printf '#!/bin/sh\necho 1.2.3\n' > "$6/bin/opencode"
chmod +x "$6/bin/opencode"
`
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := InstallAgentCLI(context.Background(), home, "opencode", "1.2.3", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "opencode")); err != nil {
		t.Fatal(err)
	}
	if err := InstallAgentCLI(context.Background(), home, "opencode", "latest", io.Discard); err == nil || !strings.Contains(err.Error(), "exact release") {
		t.Fatalf("invalid version error=%v", err)
	}
}

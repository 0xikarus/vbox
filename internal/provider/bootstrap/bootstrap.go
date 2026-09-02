package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

// Exec invokes an exact argv vector inside a workload. Implementations must
// stream stdin to the remote process without including it in argv.
type Exec func(context.Context, []string, io.Reader) (provider.ExecResult, error)

const installScript = `set -eu
export DEBIAN_FRONTEND=noninteractive
components="$1"
mkdir -p /data/home /data/workspace /usr/local/lib /opt/bun /opt/foundry/bin
mkdir -p /data/.vmbox
if [ "$components" = "__restore__" ]; then
  if [ -f /data/.vmbox/components ]; then
    components="$(cat /data/.vmbox/components)"
  else
    components="bun,claude,codex,foundry,opencode"
  fi
fi
chmod 700 /data/home
if ! command -v apt-get >/dev/null 2>&1; then
  echo "vmbox bootstrap requires a Debian/Ubuntu-compatible image with apt-get" >&2
  exit 1
fi
apt-get update -qq
packages="bash bubblewrap ca-certificates curl gh git jq openssh-client sudo tmux unzip util-linux"
case ",$components," in
  *,codex,*|*,claude,*|*,opencode,*)
    command -v npm >/dev/null 2>&1 || packages="$packages nodejs npm"
    ;;
esac
apt-get install -y -qq --no-install-recommends $packages
rm -rf /var/lib/apt/lists/*
if ! getent group vmbox >/dev/null 2>&1; then groupadd --gid 10001 vmbox; fi
if ! id -u vmbox >/dev/null 2>&1; then
  useradd --uid 10001 --gid vmbox --home-dir /data/home --shell /bin/bash --no-create-home vmbox
fi
install -d -m 0750 /etc/sudoers.d
printf '%s\n' 'vmbox ALL=(ALL) NOPASSWD:ALL' >/etc/sudoers.d/vmbox
chmod 0440 /etc/sudoers.d/vmbox
chown -R vmbox:vmbox /data/home /data/workspace /data/.vmbox
npm_packages=""
case ",$components," in *,codex,*) npm_packages="$npm_packages @openai/codex" ;; esac
case ",$components," in *,claude,*) npm_packages="$npm_packages @anthropic-ai/claude-code" ;; esac
case ",$components," in *,opencode,*) npm_packages="$npm_packages opencode-ai" ;; esac
if [ -n "$npm_packages" ]; then npm install --global --silent $npm_packages; fi
case ",$components," in
  *,bun,*)
    if [ ! -x /opt/bun/bin/bun ]; then BUN_INSTALL=/opt/bun curl -fsSL https://bun.sh/install | bash; fi
    ;;
esac
case ",$components," in
  *,foundry,*)
    if [ ! -x /opt/foundry/bin/forge ]; then
      export FOUNDRY_DIR=/opt/foundry
      curl -fsSL https://foundry.paradigm.xyz | bash
      /opt/foundry/bin/foundryup
    fi
    ;;
esac
`

const finalizeScript = `set -eu
components="$1"
fingerprint="$2"
mkdir -p /data/.vmbox
if [ "$components" = "__restore__" ]; then
  if [ -f /data/.vmbox/components ]; then
    components="$(cat /data/.vmbox/components)"
  else
    components="bun,claude,codex,foundry,opencode"
  fi
fi
chmod 0755 /usr/local/bin/vmbox-runtime /usr/local/bin/vmbox-entrypoint
ln -sfn vmbox-runtime /usr/local/bin/vmbox-report
ln -sfn vmbox-runtime /usr/local/bin/vmbox-finish
ln -sfn vmbox-runtime /usr/local/bin/vmbox-ask
HOME=/data/home /usr/local/bin/vmbox-entrypoint --configure-agent-trust /data/workspace
chown -R vmbox:vmbox /data/home /data/workspace /data/.vmbox
printf '%s\n' "$components" >/usr/local/lib/vmbox-bootstrap-components
printf '%s\n' "$components" >/data/.vmbox/components
printf '%s\n' "$fingerprint" >/usr/local/lib/vmbox-bootstrap-fingerprint
chmod 0644 /usr/local/lib/vmbox-bootstrap-components
chmod 0644 /usr/local/lib/vmbox-bootstrap-fingerprint
`

func Install(ctx context.Context, request provider.BootstrapRequest, exec Exec) error {
	components, err := normalizeComponents(request.Components)
	if err != nil {
		return err
	}
	componentText := strings.Join(components, ",")
	componentArgument := componentText
	if request.RestoreComponents {
		componentArgument = "__restore__"
	}
	fingerprint := assetFingerprint(request)
	check, err := exec(ctx, []string{"sh", "-c", `test -x /usr/local/bin/vmbox-runtime && /usr/local/bin/vmbox-runtime health >/dev/null 2>&1 && test -x /usr/local/bin/vmbox-entrypoint && [ "$(cat /usr/local/lib/vmbox-bootstrap-fingerprint 2>/dev/null)" = "$2" ] && { [ "$1" = "__restore__" ] || [ "$(cat /usr/local/lib/vmbox-bootstrap-components 2>/dev/null)" = "$1" ]; }`, "vmbox-bootstrap", componentArgument, fingerprint}, nil)
	if err == nil && check.ExitCode == 0 {
		return nil
	}
	var archResult provider.ExecResult
	for attempt := 0; attempt < 60; attempt++ {
		archResult, err = exec(ctx, []string{"uname", "-m"}, nil)
		if err == nil && archResult.ExitCode == 0 && strings.TrimSpace(archResult.Stdout) != "" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return fmt.Errorf("detect workload architecture: %w", err)
	}
	if archResult.ExitCode != 0 || strings.TrimSpace(archResult.Stdout) == "" {
		return fmt.Errorf("workload did not become ready for bootstrap: exit %d: %s", archResult.ExitCode, strings.TrimSpace(archResult.Stderr))
	}
	arch := normalizeArchitecture(archResult.Stdout)
	runtime, ok := request.RuntimeBinaries[arch]
	if !ok || len(runtime) == 0 {
		return fmt.Errorf("no vmbox runtime binary is installed for linux/%s; reinstall the Go CLI", arch)
	}
	if len(request.Entrypoint) == 0 {
		return fmt.Errorf("vmbox entrypoint asset is unavailable; reinstall the Go CLI")
	}
	installed, err := exec(ctx, []string{"sh", "-c", installScript, "vmbox-bootstrap", componentArgument}, strings.NewReader(""))
	if err != nil {
		return fmt.Errorf("install workload dependencies: %w", err)
	}
	if installed.ExitCode != 0 {
		return fmt.Errorf("install workload dependencies exited with status %d: %s", installed.ExitCode, strings.TrimSpace(installed.Stderr))
	}
	if err := put(ctx, exec, "/usr/local/bin/vmbox-runtime", runtime); err != nil {
		return err
	}
	if err := put(ctx, exec, "/usr/local/bin/vmbox-entrypoint", request.Entrypoint); err != nil {
		return err
	}
	finalized, err := exec(ctx, []string{"sh", "-c", finalizeScript, "vmbox-bootstrap", componentArgument, fingerprint}, strings.NewReader(""))
	if err != nil {
		return fmt.Errorf("finalize workload bootstrap: %w", err)
	}
	if finalized.ExitCode != 0 {
		return fmt.Errorf("finalize workload bootstrap exited with status %d: %s", finalized.ExitCode, strings.TrimSpace(finalized.Stderr))
	}
	return nil
}

func assetFingerprint(request provider.BootstrapRequest) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, installScript)
	_, _ = io.WriteString(hash, finalizeScript)
	_, _ = hash.Write(request.Entrypoint)
	architectures := make([]string, 0, len(request.RuntimeBinaries))
	for architecture := range request.RuntimeBinaries {
		architectures = append(architectures, architecture)
	}
	sort.Strings(architectures)
	for _, architecture := range architectures {
		_, _ = io.WriteString(hash, architecture+"\x00")
		_, _ = hash.Write(request.RuntimeBinaries[architecture])
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func put(ctx context.Context, exec Exec, path string, data []byte) error {
	result, err := exec(ctx, []string{"sh", "-c", `set -eu; destination="$1"; temporary="${destination}.tmp"; cat >"$temporary"; chmod 0755 "$temporary"; mv -f "$temporary" "$destination"`, "vmbox-put", path}, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("upload %s: %w", path, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("upload %s exited with status %d: %s", path, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

func normalizeArchitecture(value string) string {
	switch strings.TrimSpace(value) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.TrimSpace(value)
	}
}

func normalizeComponents(values []string) ([]string, error) {
	allowed := map[string]bool{"codex": true, "claude": true, "opencode": true, "bun": true, "foundry": true}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !allowed[value] {
			return nil, fmt.Errorf("unsupported bootstrap component %q", value)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result, nil
}

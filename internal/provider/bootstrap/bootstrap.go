package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

const dependencyCheckScript = `set -eu
components="$1"
if [ "$components" = "__restore__" ]; then
  if [ -f /data/.vmbox/components ]; then
    components="$(cat /data/.vmbox/components)"
  elif [ -f /usr/local/lib/vmbox-bootstrap-components ]; then
    components="$(cat /usr/local/lib/vmbox-bootstrap-components)"
  else
    components="bun,claude,codex,foundry,opencode"
  fi
fi
for command in bash bwrap curl gh git jq ssh sudo tmux; do
  command -v "$command" >/dev/null 2>&1
done
id -u vmbox >/dev/null 2>&1
test -r /etc/sudoers.d/vmbox
case ",$components," in *,codex,*) command -v codex >/dev/null 2>&1 ;; esac
case ",$components," in *,claude,*) command -v claude >/dev/null 2>&1 ;; esac
case ",$components," in *,opencode,*) command -v opencode >/dev/null 2>&1 ;; esac
case ",$components," in *,bun,*) test -x /opt/bun/bin/bun ;; esac
case ",$components," in *,foundry,*) test -x /opt/foundry/bin/forge ;; esac
`

const fingerprintCheckScript = `set -eu
components="$1"
if [ "$components" = "__restore__" ]; then
  if [ -f /data/.vmbox/components ]; then
    components="$(cat /data/.vmbox/components)"
  else
    printf "vmbox-bootstrap-architecture:%s\n" "$(uname -m)"
    exit 1
  fi
fi
if test -x /usr/local/bin/vmbox-runtime \
  && /usr/local/bin/vmbox-runtime health >/dev/null 2>&1 \
  && test -x /usr/local/bin/vmbox-entrypoint \
  && test "$(cat /usr/local/lib/vmbox-bootstrap-fingerprint 2>/dev/null)" = "$2" \
  && test "$(cat /usr/local/lib/vmbox-bootstrap-components 2>/dev/null)" = "$components"; then
  printf "vmbox-bootstrap-ready:%s\n" "$2"
  exit 0
fi
printf "vmbox-bootstrap-architecture:%s\n" "$(uname -m)"
exit 1
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
	check, err := exec(ctx, []string{"sh", "-c", fingerprintCheckScript, "vmbox-bootstrap", componentArgument, fingerprint}, nil)
	readyMarker := "vmbox-bootstrap-ready:" + fingerprint
	if err == nil && check.ExitCode == 0 && strings.Contains(check.Stdout, readyMarker) {
		return nil
	}
	architecture := bootstrapArchitecture(check.Stdout)
	for attempt := 0; architecture == "" && attempt < 60; attempt++ {
		probe, probeErr := exec(ctx, []string{"sh", "-c", `printf "vmbox-bootstrap-architecture:%s\n" "$(uname -m)"`}, nil)
		if probeErr == nil && probe.ExitCode == 0 {
			architecture = bootstrapArchitecture(probe.Stdout)
		}
		if architecture != "" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if architecture == "" {
		return fmt.Errorf("workload did not become ready for bootstrap")
	}
	arch := normalizeArchitecture(architecture)
	runtime, ok := request.RuntimeBinaries[arch]
	if !ok || len(runtime) == 0 {
		return fmt.Errorf("no vmbox runtime binary is installed for linux/%s; reinstall the Go CLI", arch)
	}
	if len(request.Entrypoint) == 0 {
		return fmt.Errorf("vmbox entrypoint asset is unavailable; reinstall the Go CLI")
	}
	payload := bootstrapPayload(runtime, request.Entrypoint)
	finalized, err := exec(ctx, []string{"sh", "-s", "--", componentArgument, fingerprint}, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("stream workload bootstrap: %w", err)
	}
	if finalized.ExitCode != 0 {
		return fmt.Errorf("workload bootstrap exited with status %d: %s", finalized.ExitCode, strings.TrimSpace(finalized.Stderr))
	}
	if !strings.Contains(finalized.Stdout, readyMarker) {
		return fmt.Errorf("workload bootstrap did not return its completion marker")
	}
	return nil
}

func bootstrapArchitecture(output string) string {
	const prefix = "vmbox-bootstrap-architecture:"
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return value
		}
	}
	return ""
}

func bootstrapPayload(runtime, entrypoint []byte) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("work=\"$(mktemp -d)\"\n")
	script.WriteString("runtime_tmp=/usr/local/bin/.vmbox-runtime-tmp.$$\n")
	script.WriteString("entrypoint_tmp=/usr/local/bin/.vmbox-entrypoint-tmp.$$\n")
	script.WriteString("trap 'rm -rf \"$work\"; rm -f \"$runtime_tmp\" \"$entrypoint_tmp\"' EXIT HUP INT TERM\n")
	writeScriptFile := func(name, body string) {
		fmt.Fprintf(&script, "cat >\"$work/%s\" <<'VMBOX_SCRIPT'\n%s\nVMBOX_SCRIPT\n", name, body)
	}
	writeScriptFile("dependency-check", dependencyCheckScript)
	writeScriptFile("install", installScript)
	writeScriptFile("finalize", finalizeScript)
	script.WriteString("if ! sh \"$work/dependency-check\" \"$1\"; then sh \"$work/install\" \"$1\"; fi\n")
	script.WriteString("base64 -d >\"$runtime_tmp\" <<'VMBOX_RUNTIME'\n")
	writeBase64(&script, runtime)
	script.WriteString("VMBOX_RUNTIME\n")
	script.WriteString("base64 -d >\"$entrypoint_tmp\" <<'VMBOX_ENTRYPOINT'\n")
	writeBase64(&script, entrypoint)
	script.WriteString("VMBOX_ENTRYPOINT\n")
	script.WriteString("chmod 0755 \"$runtime_tmp\" \"$entrypoint_tmp\"\n")
	script.WriteString("mv -f \"$runtime_tmp\" /usr/local/bin/vmbox-runtime\n")
	script.WriteString("mv -f \"$entrypoint_tmp\" /usr/local/bin/vmbox-entrypoint\n")
	script.WriteString("sh \"$work/finalize\" \"$1\" \"$2\"\n")
	script.WriteString("printf \"vmbox-bootstrap-ready:%s\\n\" \"$2\"\n")
	return script.String()
}

func writeBase64(destination *strings.Builder, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		destination.WriteString(encoded[:76])
		destination.WriteByte('\n')
		encoded = encoded[76:]
	}
	destination.WriteString(encoded)
	destination.WriteByte('\n')
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

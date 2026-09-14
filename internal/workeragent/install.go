package workeragent

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

// InstallationPayload is sent only on the trusted bootstrap stdin stream. It
// installs a new sidecar, never replaces a configured agent or restarts compute.
func InstallationPayload(config Config, binding workerprotocol.Binding, binary []byte, architecture string) ([]byte, error) {
	return installationPayload(config, binding, binary, architecture, "/var/lib/vmbox-worker/journal", "worker-agent-installed", "")
}

// ReplacementInstallationPayload installs identity on a newly materialized
// Railway container. Credentials and assignment state remain outside the
// workspace; only the non-secret operation journal is retained on the attached
// volume so completed operations reconcile and incomplete claims never replay.
func ReplacementInstallationPayload(config Config, binding workerprotocol.Binding, binary []byte, architecture string) ([]byte, error) {
	return replacementInstallationPayloadAt(config, binding, binary, architecture, "/data")
}

func replacementInstallationPayloadAt(config Config, binding workerprotocol.Binding, binary []byte, architecture, dataRoot string) ([]byte, error) {
	journal, setup := durableJournalSetup(config, dataRoot)
	return installationPayload(config, binding, binary, architecture, journal, "worker-agent-replacement-installed", setup)
}

func DurableInstallationPayload(config Config, binding workerprotocol.Binding, binary []byte, architecture string) ([]byte, error) {
	journal, setup := durableJournalSetup(config, "/data")
	return installationPayload(config, binding, binary, architecture, journal, "worker-agent-installed", setup)
}

func durableJournalSetup(config Config, dataRoot string) (string, string) {
	scope := sha256.Sum256([]byte(config.AccountID + "\x00" + config.WorkerID))
	journal := fmt.Sprintf("%s/.vmbox-worker/%x/journal", dataRoot, scope)
	setup := fmt.Sprintf(`command -v stat >/dev/null
test -d '%s'
test ! -L '%s'
secure_directory() {
 path=$1
 if test -e "$path"; then
  test ! -L "$path"
 else
  mkdir "$path"
 fi
 test -d "$path"
 test "$(stat -c '%%u:%%g:%%a' "$path")" = '0:0:700'
}
secure_directory '%s/.vmbox-worker'
secure_directory '%s/.vmbox-worker/%x'
secure_directory '%s/.vmbox-worker/%x/journal'
`, dataRoot, dataRoot, dataRoot, dataRoot, scope, dataRoot, scope)
	return journal, setup
}

func ValidateReplacementInstallation(controllerURL string, binary []byte, architecture string) error {
	if _, err := (&Agent{Config: Config{ControllerURL: controllerURL}}).endpoint("/v1/workers/enroll"); err != nil {
		return err
	}
	if architecture != "amd64" && architecture != "arm64" {
		return errors.New("unsupported agent architecture")
	}
	if len(binary) == 0 || len(binary) > 32*1024*1024 {
		return errors.New("invalid agent binary size")
	}
	return nil
}

func installationPayload(config Config, binding workerprotocol.Binding, binary []byte, architecture, journalDirectory, success, durableSetup string) ([]byte, error) {
	if _, err := (&Agent{Config: config}).endpoint("/v1/workers/enroll"); err != nil {
		return nil, err
	}
	if architecture != "amd64" && architecture != "arm64" {
		return nil, errors.New("unsupported agent architecture")
	}
	token, err := base64.RawURLEncoding.DecodeString(config.EnrollmentToken)
	if err != nil || len(token) != 32 || config.Credential != "" || config.WorkerID == "" || config.AccountID == "" || config.SlotID == "" {
		return nil, errors.New("invalid worker enrollment configuration")
	}
	if binding.AccountID != config.AccountID || binding.SlotID != config.SlotID || binding.BoxID == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(binding.Assignment) || binding.Incarnation != "" {
		return nil, errors.New("invalid initial worker assignment")
	}
	if len(binary) == 0 || len(binary) > 32*1024*1024 {
		return nil, errors.New("invalid agent binary size")
	}
	config.JournalDirectory = journalDirectory
	config.BindingFile = "/var/lib/vmbox-worker/binding.json"
	configuration, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	assignment, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	if len(configuration) > 16*1024 || len(assignment) > 4096 {
		return nil, errors.New("worker enrollment payload too large")
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, file := range []struct {
		name string
		data []byte
		mode int64
	}{{"agent", binary, 0700}, {"config.json", configuration, 0600}, {"binding.json", assignment, 0600}} {
		if err := writer.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	fmt.Fprintf(&payload, `set -eu
umask 077
test "$(id -u)" = 0
case "$(uname -m)" in x86_64) architecture=amd64 ;; aarch64) architecture=arm64 ;; *) exit 64 ;; esac
test "$architecture" = '%s'
command -v flock >/dev/null
command -v setsid >/dev/null
test ! -L /var/lib/vmbox-worker
mkdir -p /var/lib/vmbox-worker
chmod 0700 /var/lib/vmbox-worker
exec 9>/var/lib/vmbox-worker/install.lock
flock -n 9
# Refuse replacement, including symlinks and partially installed configurations.
test ! -e /var/lib/vmbox-worker/config.json
	test ! -L /var/lib/vmbox-worker/config.json
%s
work=$(mktemp -d /var/lib/vmbox-worker/install.XXXXXXXX)
trap 'rm -rf "$work"' EXIT HUP INT TERM
base64 -d >"$work/payload.tar" <<'VMBOX_WORKER_PAYLOAD'
`, architecture, durableSetup)
	payload.WriteString(base64.StdEncoding.EncodeToString(archive.Bytes()))
	fmt.Fprintf(&payload, `
VMBOX_WORKER_PAYLOAD
tar -xf "$work/payload.tar" -C "$work"
chmod 0600 "$work/config.json" "$work/binding.json"
chmod 0755 "$work/agent"
install -m 0755 "$work/agent" /usr/local/bin/vmbox-worker-agent
mv "$work/binding.json" /var/lib/vmbox-worker/binding.json
mv "$work/config.json" /var/lib/vmbox-worker/config.json
# setsid detaches only the new supervisor from the bootstrap command's group.
setsid -f /usr/local/bin/vmbox-worker-agent --supervise --config /var/lib/vmbox-worker/config.json 9>&- </dev/null >/var/lib/vmbox-worker/agent.log 2>&1
printf '%s\n'
`, success)
	return payload.Bytes(), nil
}

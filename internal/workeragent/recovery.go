package workeragent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

type InstallationIdentity struct {
	ControllerURL string `json:"controllerUrl"`
	AccountID     string `json:"accountId"`
	SlotID        string `json:"slotId"`
	WorkerID      string `json:"workerId"`
}

func (i InstallationIdentity) valid() bool {
	_, err := (&Agent{Config: Config{ControllerURL: i.ControllerURL}}).endpoint("/v1/workers/connect")
	return err == nil && i.AccountID != "" && i.SlotID != "" && i.WorkerID != ""
}

// VerifyInstallation reads public expected identity from stdin and checks the
// private configuration without returning credentials or changing any files.
func VerifyInstallation(path string, input io.Reader) error {
	var expected InstallationIdentity
	decoder := json.NewDecoder(io.LimitReader(input, 8193))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&expected) != nil || !expected.valid() {
		return errors.New("invalid installation identity")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("invalid installation identity")
	}
	config, err := Load(path)
	if err != nil {
		return err
	}
	if config.WorkerID != expected.WorkerID || config.AccountID != expected.AccountID || config.SlotID != expected.SlotID || config.ControllerURL != expected.ControllerURL {
		return errors.New("installation identity mismatch")
	}
	scope := sha256.Sum256([]byte(expected.AccountID + "\x00" + expected.WorkerID))
	durableJournal := fmt.Sprintf("/data/.vmbox-worker/%x/journal", scope)
	legacyJournal := filepath.Join(filepath.Dir(path), "journal")
	if config.BindingFile != filepath.Join(filepath.Dir(path), "binding.json") || (config.JournalDirectory != legacyJournal && config.JournalDirectory != durableJournal) {
		return errors.New("installation private paths mismatch")
	}
	// Scope checking leaves the existing assignment untouched. Activation will
	// separately compare current database assignment and surviving sessions.
	agent := Agent{Config: config}
	binding, err := agent.localBinding(context.Background())
	if err != nil || binding.AccountID != expected.AccountID || binding.SlotID != expected.SlotID || binding.BoxID == "" || binding.Assignment == "" {
		return errors.New("installation binding unavailable")
	}
	return nil
}

func RecoveryPayload(expected InstallationIdentity) ([]byte, error) {
	if !expected.valid() {
		return nil, errors.New("invalid installation identity")
	}
	data, _ := json.Marshal(expected)
	return []byte(`set -eu
umask 077
test "$(id -u)" = 0
test -d /var/lib/vmbox-worker
test ! -L /var/lib/vmbox-worker
exec 9>/var/lib/vmbox-worker/install.lock
flock -n 9
base64 -d <<'VMBOX_RECOVERY_IDENTITY' | /usr/local/bin/vmbox-worker-agent --verify-installation --config /var/lib/vmbox-worker/config.json
` + base64.StdEncoding.EncodeToString(data) + `
VMBOX_RECOVERY_IDENTITY
# A healthy supervisor retains its lock. Recovery neither stops it nor replaces
# configuration, binding, credentials or the installed binary.
test ! -L /var/lib/vmbox-worker/config.json.supervisor.lock
if flock -n /var/lib/vmbox-worker/config.json.supervisor.lock true; then
 setsid -f /usr/local/bin/vmbox-worker-agent --supervise --config /var/lib/vmbox-worker/config.json 9>&- </dev/null >>/var/lib/vmbox-worker/agent.log 2>&1
fi
printf 'worker-agent-recovery-started\n'
`), nil
}

// InstallationRecoveryPayload distinguishes a definitely absent ephemeral
// configuration from an ambiguous transport failure. Only the former permits
// the controller to rotate the pending enrollment and install once more.
func InstallationRecoveryPayload(expected InstallationIdentity) ([]byte, error) {
	if !expected.valid() {
		return nil, errors.New("invalid installation identity")
	}
	data, _ := json.Marshal(expected)
	return []byte(`set -eu
umask 077
test "$(id -u)" = 0
test -d /var/lib/vmbox-worker
test ! -L /var/lib/vmbox-worker
exec 9>/var/lib/vmbox-worker/install.lock
flock -n 9
if test ! -e /var/lib/vmbox-worker/config.json; then
 printf 'worker-agent-installation-absent\n'
 exit 66
fi
base64 -d <<'VMBOX_RECOVERY_IDENTITY' | /usr/local/bin/vmbox-worker-agent --verify-installation --config /var/lib/vmbox-worker/config.json
` + base64.StdEncoding.EncodeToString(data) + `
VMBOX_RECOVERY_IDENTITY
test ! -L /var/lib/vmbox-worker/config.json.supervisor.lock
if flock -n /var/lib/vmbox-worker/config.json.supervisor.lock true; then
 setsid -f /usr/local/bin/vmbox-worker-agent --supervise --config /var/lib/vmbox-worker/config.json 9>&- </dev/null >>/var/lib/vmbox-worker/agent.log 2>&1
fi
printf 'worker-agent-installation-recovery-started\n'
`), nil
}

func ReplacementRecoveryPayload(expected InstallationIdentity) ([]byte, error) {
	return InstallationRecoveryPayload(expected)
}

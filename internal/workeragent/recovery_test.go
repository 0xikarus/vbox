package workeragent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func TestRecoveryVerifiesIdentityWithoutChangingPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	expected := InstallationIdentity{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker"}
	config := Config{ControllerURL: expected.ControllerURL, AccountID: expected.AccountID, SlotID: expected.SlotID, WorkerID: expected.WorkerID, Credential: "private-existing-credential", BindingFile: filepath.Join(dir, "binding.json"), JournalDirectory: filepath.Join(dir, "journal")}
	configData, _ := json.Marshal(config)
	bindingData, _ := json.Marshal(workerprotocol.Binding{AccountID: expected.AccountID, SlotID: expected.SlotID, BoxID: "box", Assignment: "fence", Incarnation: "existing-agent"})
	if err := os.WriteFile(path, configData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.BindingFile, bindingData, 0600); err != nil {
		t.Fatal(err)
	}
	check := func(identity InstallationIdentity) error {
		data, _ := json.Marshal(identity)
		return VerifyInstallation(path, bytes.NewReader(data))
	}
	if err := check(expected); err != nil {
		t.Fatal(err)
	}
	durableConfig := config
	scope := sha256.Sum256([]byte(expected.AccountID + "\x00" + expected.WorkerID))
	durableConfig.JournalDirectory = fmt.Sprintf("/data/.vmbox-worker/%x/journal", scope)
	durableData, _ := json.Marshal(durableConfig)
	if err := os.WriteFile(path, durableData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := check(expected); err != nil {
		t.Fatal("durable replacement journal rejected", err)
	}
	durableConfig.JournalDirectory = "/data/.vmbox-worker/unscoped/journal"
	invalidData, _ := json.Marshal(durableConfig)
	if err := os.WriteFile(path, invalidData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := check(expected); err == nil {
		t.Fatal("unscoped durable journal accepted")
	}
	if err := os.WriteFile(path, configData, 0600); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*InstallationIdentity){
		func(i *InstallationIdentity) { i.WorkerID = "other" },
		func(i *InstallationIdentity) { i.AccountID = "other" },
		func(i *InstallationIdentity) { i.SlotID = "other" },
		func(i *InstallationIdentity) { i.ControllerURL = "https://other.example" },
	} {
		identity := expected
		change(&identity)
		if err := check(identity); err == nil {
			t.Fatal("cross-identity recovery accepted")
		}
	}
	got, _ := os.ReadFile(path)
	bindingGot, _ := os.ReadFile(config.BindingFile)
	if !bytes.Equal(got, configData) || !bytes.Equal(bindingGot, bindingData) {
		t.Fatal("verification altered private state")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := check(expected); err == nil {
		t.Fatal("public configuration accepted")
	}
}
func TestRecoveryPayloadUsesVerificationAndNeverReplacesInstallation(t *testing.T) {
	payload, err := RecoveryPayload(InstallationIdentity{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-n")
	command.Stdin = bytes.NewReader(payload)
	if err := command.Run(); err != nil {
		t.Fatalf("invalid recovery shell: %v", err)
	}
	for _, forbidden := range []string{"kill ", "pkill", "mv ", "rm ", "tar ", "install -", "enrollmentToken"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("unexpected recovery operation: %s", forbidden)
		}
	}
	if !bytes.Contains(payload, []byte("--verify-installation")) || !bytes.Contains(payload, []byte("flock -n")) {
		t.Fatal("recovery omitted verification or singleton guard")
	}
}

func TestReplacementRecoveryReportsOnlyDefiniteAbsence(t *testing.T) {
	payload, err := ReplacementRecoveryPayload(InstallationIdentity{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-n")
	command.Stdin = bytes.NewReader(payload)
	if err := command.Run(); err != nil {
		t.Fatal("invalid replacement recovery shell", err)
	}
	if !bytes.Contains(payload, []byte("test ! -e /var/lib/vmbox-worker/config.json")) || !bytes.Contains(payload, []byte("worker-agent-installation-absent")) || !bytes.Contains(payload, []byte("--verify-installation")) {
		t.Fatal("replacement recovery lacks an exact absence/identity distinction")
	}
}

func TestRecoveryScriptPreservesExistingSupervisor(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("recovery script requires root; uses only a private temporary installation")
	}
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock unavailable")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid unavailable")
	}
	dir := t.TempDir()
	installed := filepath.Join(dir, "agent")
	started := filepath.Join(dir, "started")
	// A disposable stand-in records supervisor launches. Identity verification
	// itself is exercised separately against real private files above.
	script := "#!/bin/sh\ncase \"$1\" in --verify-installation) cat >/dev/null; exit 0 ;; --supervise) printf started > '" + started + "'; exit 0 ;; *) exit 9 ;; esac\n"
	if err := os.WriteFile(installed, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	payload, err := RecoveryPayload(InstallationIdentity{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.ReplaceAll(payload, []byte("/var/lib/vmbox-worker"), []byte(dir))
	payload = bytes.ReplaceAll(payload, []byte("/usr/local/bin/vmbox-worker-agent"), []byte(installed))
	lock, err := Lock(filepath.Join(dir, "config.json.supervisor.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	run := func() {
		command := exec.Command("sh", "-xs")
		command.Stdin = bytes.NewReader(payload)
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		out, err := command.Output()
		if err != nil || string(out) != "worker-agent-recovery-started\n" {
			t.Fatalf("recovery script: %q %v %s", out, err, diagnostics.String())
		}
	}
	run()
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatal("recovery launched a supervisor while existing lock was held")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	run()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing supervisor was not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

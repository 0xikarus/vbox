package workeragent

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
)

func TestInstallationPayloadContainsPrivateScopedAssets(t *testing.T) {
	token, _ := secret()
	config := Config{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker", EnrollmentToken: token, BindingFile: "/data/unsafe", JournalDirectory: "/data/unsafe"}
	binding := workerprotocol.Binding{AccountID: "account", SlotID: "slot", BoxID: "box", Assignment: strings.Repeat("a", 64)}
	binary := []byte("disposable-agent-fixture")
	payload, err := InstallationPayload(config, binding, binary, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("sh", "-n")
	check.Stdin = bytes.NewReader(payload)
	if err := check.Run(); err != nil {
		t.Fatal("invalid installation shell syntax", err)
	}
	_, encoded, ok := strings.Cut(string(payload), "<<'VMBOX_WORKER_PAYLOAD'\n")
	if !ok {
		t.Fatal("missing private payload")
	}
	encoded, _, ok = strings.Cut(encoded, "\nVMBOX_WORKER_PAYLOAD\n")
	if !ok {
		t.Fatal("missing payload end")
	}
	archive, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	files := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || header.Mode&0077 != 0 {
			t.Fatal("non-private or non-regular installation asset")
		}
		files[header.Name], err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) != 3 || !bytes.Equal(files["agent"], binary) {
		t.Fatal("incorrect installation assets")
	}
	var installed Config
	if err := json.Unmarshal(files["config.json"], &installed); err != nil {
		t.Fatal(err)
	}
	if installed.EnrollmentToken != token || installed.Credential != "" || installed.BindingFile != "/var/lib/vmbox-worker/binding.json" || installed.JournalDirectory != "/var/lib/vmbox-worker/journal" {
		t.Fatal("incorrect private enrollment configuration")
	}
	var installedBinding workerprotocol.Binding
	if err := json.Unmarshal(files["binding.json"], &installedBinding); err != nil || installedBinding != binding {
		t.Fatal("binding was not preserved")
	}
	binding.SlotID = "other-slot"
	if _, err := InstallationPayload(config, binding, binary, "amd64"); err == nil {
		t.Fatal("cross-slot binding accepted")
	}
}

func TestReplacementPayloadPersistsOnlyOperationJournal(t *testing.T) {
	token, _ := secret()
	config := Config{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker", EnrollmentToken: token}
	binding := workerprotocol.Binding{AccountID: "account", SlotID: "slot", BoxID: "box", Assignment: strings.Repeat("b", 64)}
	payload, err := ReplacementInstallationPayload(config, binding, []byte("replacement-agent"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	check := exec.Command("sh", "-n")
	check.Stdin = bytes.NewReader(payload)
	if err := check.Run(); err != nil {
		t.Fatal("invalid replacement shell syntax", err)
	}
	_, encoded, ok := strings.Cut(string(payload), "<<'VMBOX_WORKER_PAYLOAD'\n")
	if !ok {
		t.Fatal("missing replacement archive")
	}
	encoded, _, ok = strings.Cut(encoded, "\nVMBOX_WORKER_PAYLOAD\n")
	if !ok {
		t.Fatal("missing replacement archive end")
	}
	archive, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	var installed Config
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if header.Name == "config.json" && json.Unmarshal(data, &installed) != nil {
			t.Fatal("invalid replacement config")
		}
	}
	if installed.BindingFile != "/var/lib/vmbox-worker/binding.json" || !strings.HasPrefix(installed.JournalDirectory, "/data/.vmbox-worker/") || !strings.HasSuffix(installed.JournalDirectory, "/journal") {
		t.Fatalf("replacement private paths: %+v", installed)
	}
	if strings.Contains(installed.JournalDirectory, config.AccountID) {
		t.Fatal("journal path exposed unhashed identity")
	}
	text := string(payload)
	if !strings.Contains(text, "worker-agent-replacement-installed") || strings.Contains(text, "rm -rf /data") || strings.Contains(text, "config.json /data") || strings.Contains(text, "binding.json /data") {
		t.Fatal("replacement payload did not preserve journal-only durability")
	}
}

func TestReplacementPayloadReusesJournalAcrossEphemeralInstallations(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("replacement installation fixture requires root ownership")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	stateDir := filepath.Join(root, "state")
	agentPath := filepath.Join(root, "agent")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	binding := workerprotocol.Binding{AccountID: "account", SlotID: "slot", BoxID: "box", Assignment: strings.Repeat("c", 64)}
	binary := []byte("#!/bin/sh\nexit 0\n")
	run := func() Config {
		token, _ := secret()
		payload, err := replacementInstallationPayloadAt(Config{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker", EnrollmentToken: token}, binding, binary, "amd64", dataDir)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.ReplaceAll(payload, []byte("/usr/local/bin/vmbox-worker-agent"), []byte(agentPath))
		payload = bytes.ReplaceAll(payload, []byte("/var/lib/vmbox-worker"), []byte(stateDir))
		command := exec.Command("sh", "-s")
		command.Stdin = bytes.NewReader(payload)
		output, err := command.Output()
		if err != nil || string(output) != "worker-agent-replacement-installed\n" {
			t.Fatalf("replacement install output=%q err=%v", output, err)
		}
		config, err := Load(filepath.Join(stateDir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	first := run()
	marker := filepath.Join(first.JournalDirectory, "retained-operation.json")
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	second := run()
	if second.JournalDirectory != first.JournalDirectory {
		t.Fatal("replacement changed durable journal scope")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "retained" {
		t.Fatal("replacement lost retained operation journal", err)
	}

	insecureRoot := t.TempDir()
	insecureData := filepath.Join(insecureRoot, "data")
	insecureState := filepath.Join(insecureRoot, "state")
	if err := os.MkdirAll(filepath.Join(insecureData, ".vmbox-worker"), 0755); err != nil {
		t.Fatal(err)
	}
	token, _ := secret()
	payload, err := replacementInstallationPayloadAt(Config{ControllerURL: "https://controller.example", AccountID: "account", SlotID: "slot", WorkerID: "worker", EnrollmentToken: token}, binding, binary, "amd64", insecureData)
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.ReplaceAll(payload, []byte("/usr/local/bin/vmbox-worker-agent"), []byte(filepath.Join(insecureRoot, "agent")))
	payload = bytes.ReplaceAll(payload, []byte("/var/lib/vmbox-worker"), []byte(insecureState))
	command := exec.Command("sh", "-s")
	command.Stdin = bytes.NewReader(payload)
	if err := command.Run(); err == nil {
		t.Fatal("replacement accepted user-mode durable state")
	}
	info, err := os.Stat(filepath.Join(insecureData, ".vmbox-worker"))
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("replacement chmodded untrusted durable state", err)
	}
}

package bootstrap

import (
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

func TestInstallStreamsMatchingRuntimeAndIsIdempotent(t *testing.T) {
	request := provider.BootstrapRequest{
		Components:      []string{"foundry", "codex", "codex"},
		RuntimeBinaries: map[string][]byte{"amd64": []byte("runtime-amd64"), "arm64": []byte("runtime-arm64")},
		Entrypoint:      []byte("entrypoint"),
	}
	var calls [][]string
	fingerprint := assetFingerprint(request)
	exec := func(_ context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
		calls = append(calls, append([]string(nil), argv...))
		switch len(calls) {
		case 1:
			if argv[2] != fingerprintCheckScript {
				t.Fatalf("fingerprint check argv=%#v", argv)
			}
			return provider.ExecResult{ExitCode: 1, Stdout: "vmbox-bootstrap-architecture:x86_64\n"}, nil
		case 2:
			if stdin == nil || len(argv) != 5 || argv[0] != "sh" || argv[1] != "-s" || argv[3] != "codex,foundry" || argv[4] != fingerprint {
				t.Fatalf("streamed bootstrap argv=%#v stdin=%v", argv, stdin)
			}
			payload, err := io.ReadAll(stdin)
			if err != nil {
				t.Fatal(err)
			}
			text := string(payload)
			if !strings.Contains(text, "useradd --uid 10001") || !strings.Contains(text, "NOPASSWD:ALL") || !strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("runtime-amd64"))) || strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("runtime-arm64"))) || !strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("entrypoint"))) {
				t.Fatalf("streamed bootstrap payload did not contain only matching assets")
			}
			return provider.ExecResult{Stdout: "vmbox-bootstrap-ready:" + fingerprint + "\n"}, nil
		default:
			t.Fatalf("unexpected bootstrap call %#v", argv)
			return provider.ExecResult{}, nil
		}
	}
	if err := Install(context.Background(), request, exec); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("bootstrap calls=%d", len(calls))
	}

	calls = nil
	if err := Install(context.Background(), request, func(_ context.Context, argv []string, _ io.Reader) (provider.ExecResult, error) {
		calls = append(calls, argv)
		return provider.ExecResult{Stdout: "vmbox-bootstrap-ready:" + fingerprint + "\n"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("idempotent bootstrap calls=%d", len(calls))
	}
}

func TestInstallSkipsDependencyInstallWhenImageIsPrebuilt(t *testing.T) {
	request := provider.BootstrapRequest{
		Components:      []string{"claude", "codex"},
		RuntimeBinaries: map[string][]byte{"amd64": []byte("runtime")},
		Entrypoint:      []byte("entrypoint"),
	}
	calls := 0
	fingerprint := assetFingerprint(request)
	exec := func(_ context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
		calls++
		switch calls {
		case 1:
			return provider.ExecResult{ExitCode: 1, Stdout: "vmbox-bootstrap-architecture:x86_64\n"}, nil
		case 2:
			payload, err := io.ReadAll(stdin)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), `if ! sh "$work/dependency-check" "$1"; then sh "$work/install" "$1"; fi`) {
				t.Fatal("streamed bootstrap does not skip install when the dependency check succeeds")
			}
			return provider.ExecResult{Stdout: "vmbox-bootstrap-ready:" + fingerprint + "\n"}, nil
		default:
			t.Fatalf("unexpected bootstrap call %#v", argv)
			return provider.ExecResult{}, nil
		}
	}
	if err := Install(context.Background(), request, exec); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("prebuilt bootstrap calls=%d", calls)
	}
}

func TestInstallRejectsUnknownComponent(t *testing.T) {
	err := Install(context.Background(), provider.BootstrapRequest{Components: []string{"unknown"}}, func(context.Context, []string, io.Reader) (provider.ExecResult, error) {
		return provider.ExecResult{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported bootstrap component") {
		t.Fatalf("error=%v", err)
	}
}

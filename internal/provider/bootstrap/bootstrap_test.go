package bootstrap

import (
	"context"
	"io"
	"reflect"
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
	var uploads []string
	exec := func(_ context.Context, argv []string, stdin io.Reader) (provider.ExecResult, error) {
		calls = append(calls, append([]string(nil), argv...))
		switch len(calls) {
		case 1:
			return provider.ExecResult{ExitCode: 1}, nil
		case 2:
			return provider.ExecResult{Stdout: "x86_64\n"}, nil
		case 3:
			if stdin == nil || argv[len(argv)-1] != "codex,foundry" {
				t.Fatalf("install argv=%#v stdin=%v", argv, stdin)
			}
			return provider.ExecResult{}, nil
		case 4, 5:
			data, err := io.ReadAll(stdin)
			if err != nil {
				t.Fatal(err)
			}
			uploads = append(uploads, string(data))
			return provider.ExecResult{}, nil
		case 6:
			return provider.ExecResult{}, nil
		default:
			t.Fatalf("unexpected bootstrap call %#v", argv)
			return provider.ExecResult{}, nil
		}
	}
	if err := Install(context.Background(), request, exec); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(uploads, []string{"runtime-amd64", "entrypoint"}) {
		t.Fatalf("uploads=%#v", uploads)
	}

	calls = nil
	if err := Install(context.Background(), request, func(_ context.Context, argv []string, _ io.Reader) (provider.ExecResult, error) {
		calls = append(calls, argv)
		return provider.ExecResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("idempotent bootstrap calls=%d", len(calls))
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

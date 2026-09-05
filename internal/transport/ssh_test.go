package transport

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type recorder struct{ calls [][]string }

func (r *recorder) Run(_ context.Context, a []string, _ io.Reader, _ io.Writer, _ io.Writer) (procexec.Result, error) {
	r.calls = append(r.calls, a)
	return procexec.Result{ExitCode: 255}, nil
}
func TestSSHDoesNotRetryOrRepairKeys(t *testing.T) {
	r := &recorder{}
	s := SSH{Runner: r}
	_, err := s.ExecConnection(context.Background(), provider.Connection{Transport: "openssh", Endpoint: "user@host"}, []string{"printf", "ü'; touch /tmp/no"}, provider.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatal("retried transport")
	}
	args := strings.Join(r.calls[0], " ")
	for _, v := range []string{"StrictHostKeyChecking=accept-new", "ControlPath=none", "-F /dev/null", "'\"'\"'"} {
		if !strings.Contains(args, v) {
			t.Fatalf("missing %s", v)
		}
	}
	for _, endpoint := range []string{"-oProxyCommand=bad", "u@host --port 1", "u@host\ncommand", "u@host;command"} {
		if _, err := s.ExecConnection(context.Background(), provider.Connection{Transport: "openssh", Endpoint: endpoint}, []string{"true"}, provider.ExecOptions{}); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

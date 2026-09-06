package railway

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/procexec"
	"github.com/0xikarus/vmbox-service/internal/provider"
)

type streamTestRunner struct {
	procexec.FakeRunner
	attached int
}

func (r *streamTestRunner) RunAttached(ctx context.Context, argv []string, in io.Reader, out, stderr io.Writer) (procexec.Result, error) {
	r.attached++
	return r.Run(ctx, argv, in, out, stderr)
}

func TestWorkspaceStreamDoesNotCaptureOrRetry(t *testing.T) {
	runner := &streamTestRunner{FakeRunner: procexec.FakeRunner{Results: []procexec.Result{{ExitCode: 255, Stdout: []byte("must not return captured data")}}}}
	p := New(Config{}, runner)
	conn := provider.Connection{Transport: "openssh", Endpoint: "deployment-test@ssh.railway.com", Metadata: map[string]string{"deploymentInstanceId": "deployment-test"}}
	result, _ := p.StreamConnection(context.Background(), conn, []string{"vmbox-runtime", "desktop-stream", "fence"}, provider.ExecOptions{Stdin: strings.NewReader("test")})
	if runner.attached != 1 || len(runner.Calls) != 1 || result.Stdout != "" {
		t.Fatal("stream captured or retried")
	}
	args := runner.Calls[0].Argv
	command := args[len(args)-1]
	if !strings.Contains(command, "'direct-json'") || strings.Contains(command, "'exec-json'") || strings.Contains(strings.Join(args, " "), "-tt") {
		t.Fatal("stream used recorded execution or SSH tty")
	}
}

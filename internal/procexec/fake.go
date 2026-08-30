package procexec

import (
	"context"
	"io"
	"sync"
)

type Call struct{ Argv []string }

type FakeRunner struct {
	mu      sync.Mutex
	Calls   []Call
	Results []Result
	Errors  []error
}

func (f *FakeRunner) Run(_ context.Context, argv []string, _ io.Reader, stdout, stderr io.Writer) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Argv: append([]string(nil), argv...)})
	i := len(f.Calls) - 1
	var result Result
	if i < len(f.Results) {
		result = f.Results[i]
	}
	if stdout != nil {
		_, _ = stdout.Write(result.Stdout)
	}
	if stderr != nil {
		_, _ = stderr.Write(result.Stderr)
	}
	if i < len(f.Errors) {
		return result, f.Errors[i]
	}
	return result, nil
}

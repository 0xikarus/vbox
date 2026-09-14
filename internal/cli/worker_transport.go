package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/0xikarus/vmbox-service/internal/config"
	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/transport"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// resolvedExec keeps controller credentials local and preserves the legacy SSH
// path until a worker has explicitly selected the new transport.
func (a *App) resolvedExec(ctx context.Context, c config.Context, token string, conn provider.Connection, argv []string, opts provider.ExecOptions, stream bool) (provider.ExecResult, error) {
	if conn.Transport != "controller-worker" {
		if stream {
			return a.nativeTransport().StreamConnection(ctx, conn, provider.AsWorkloadUser(argv), opts)
		}
		return a.nativeTransport().ExecConnection(ctx, conn, provider.AsWorkloadUser(argv), opts)
	}
	if replacement, ok := a.authReplacements[c.Controller+"\x00"+token]; ok {
		token = replacement
	}
	worker := transport.Worker{ControllerURL: c.Controller, Token: token, HTTP: a.HTTP}
	if opts.Interactive {
		// The runtime owns the PTY; stdin carries the same resize/input frames as
		// browser terminals. Never ask Railway SSH to allocate a second terminal.
		if len(argv) != 5 || argv[0] != "vmbox-runtime" || argv[1] != "native-attach" {
			return provider.ExecResult{}, provider.ErrUnsupported
		}
		argv = append([]string{"vmbox-runtime", "web-terminal"}, argv[2:]...)
		input, closeInput := framedWorkerInput(ctx, opts.Stdin)
		defer closeInput()
		opts.Stdin, opts.Interactive = input, false
		return worker.StreamConnection(ctx, conn, argv, opts)
	}
	if stream {
		return worker.StreamConnection(ctx, conn, argv, opts)
	}
	return worker.ExecConnection(ctx, conn, argv, opts)
}

func framedWorkerInput(parent context.Context, source io.Reader) (io.Reader, func()) {
	ctx, cancel := context.WithCancel(parent)
	terminal, isFile := source.(*os.File)
	isTerminal := isFile && term.IsTerminal(int(terminal.Fd()))
	if isTerminal {
		// Do not leave a blocked stdin goroutine behind to consume keystrokes
		// from the next attachment after a remote disconnect.
		source = workerTerminalReader{ctx, terminal}
	}
	reader, writer := io.Pipe()
	var mu sync.Mutex
	encode := func(value any) error {
		mu.Lock()
		defer mu.Unlock()
		return json.NewEncoder(writer).Encode(value)
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		defer writer.Close()
		if source == nil {
			return
		}
		data := make([]byte, 32*1024)
		for {
			n, err := source.Read(data)
			if n > 0 {
				if writeErr := encode(struct {
					Data []byte `json:"data"`
				}{data[:n]}); writeErr != nil {
					return
				}
			}
			if err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	if isTerminal {
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			lastCols, lastRows := 0, 0
			for {
				cols, rows, err := term.GetSize(int(terminal.Fd()))
				cols, rows = max(2, min(cols, 500)), max(2, min(rows, 300))
				if err == nil && (cols != lastCols || rows != lastRows) {
					if encode(struct {
						Cols int `json:"cols"`
						Rows int `json:"rows"`
					}{cols, rows}) != nil {
						return
					}
					lastCols, lastRows = cols, rows
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	return reader, func() {
		cancel()
		reader.Close()
		writer.Close()
		if isTerminal {
			<-inputDone
		}
	}
}

type workerTerminalReader struct {
	ctx  context.Context
	file *os.File
}

func (r workerTerminalReader) Read(data []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		poll := []unix.PollFd{{Fd: int32(r.file.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if n > 0 {
			return r.file.Read(data)
		}
	}
}

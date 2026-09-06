package boxruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// TerminalFrame is newline-framed JSON on the SSH stdin transport. Data is
// base64-encoded by encoding/json, preserving arbitrary terminal input bytes.
type TerminalFrame struct {
	Data []byte `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func ApplyTerminalFrames(input io.Reader, output io.Writer, resize func(uint16, uint16) error) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	for scanner.Scan() {
		var frame TerminalFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return fmt.Errorf("invalid terminal frame")
		}
		if len(frame.Data) > 0 {
			if len(frame.Data) > 65536 || frame.Cols != 0 || frame.Rows != 0 {
				return fmt.Errorf("invalid input frame")
			}
			if _, err := output.Write(frame.Data); err != nil {
				return err
			}
		} else {
			if frame.Cols < 2 || frame.Cols > 500 || frame.Rows < 2 || frame.Rows > 300 {
				return fmt.Errorf("invalid terminal size")
			}
			if err := resize(frame.Cols, frame.Rows); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// WebTerminal attaches only the identified existing session. Ending this stream
// kills its tmux client, never the tmux server or session.
func WebTerminal(ctx context.Context, assignment, id, incarnation string, input io.Reader, output io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "native-attach", assignment, id, incarnation)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return err
	}
	defer terminal.Close()
	done := make(chan error, 2)
	go func() {
		done <- ApplyTerminalFrames(input, terminal, func(cols, rows uint16) error { return pty.Setsize(terminal, &pty.Winsize{Cols: cols, Rows: rows}) })
	}()
	go func() { _, err := io.Copy(output, terminal); done <- err }()
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-done:
	}
	cancel()
	_ = terminal.Close()
	_ = cmd.Wait()
	return err
}

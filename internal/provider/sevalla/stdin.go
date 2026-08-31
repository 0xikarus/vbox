package sevalla

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/coder/websocket"
)

const maxSevallaStdin = 16 << 20

// execStdin uses Sevalla's documented bidirectional terminal transport. It
// waits until terminal echo is disabled before sending base64-encoded input,
// so credentials never appear in local argv, remote argv, or terminal output.
func (p *Provider) execStdin(ctx context.Context, app application, process process, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	started := time.Now().UTC()
	input, err := io.ReadAll(io.LimitReader(opts.Stdin, maxSevallaStdin+1))
	if err != nil {
		return provider.ExecResult{}, err
	}
	if len(input) > maxSevallaStdin {
		return provider.ExecResult{}, fmt.Errorf("Sevalla stdin exceeds %d bytes", maxSevallaStdin)
	}
	nonceBytes := make([]byte, 12)
	if _, err := rand.Read(nonceBytes); err != nil {
		return provider.ExecResult{}, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	ready, done := "__VMBOX_STDIN_READY_"+nonce+"__", "__VMBOX_STDIN_DONE_"+nonce+"__"
	end := done + "_END"
	command := "stty -echo; printf '" + ready + "\\n'; base64 -d | " + shellCommand(argv) + "; code=$?; stty echo; printf '\\n" + done + ":%s\\n" + end + "\\n' \"$code\"\n"

	connection, err := p.Connection(ctx, app.ID)
	if err != nil {
		return provider.ExecResult{}, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+p.cfg.Token)
	ws, _, err := websocket.Dial(ctx, connection.Endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("Sevalla terminal stdin: %w", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "complete")
	conn := websocket.NetConn(ctx, ws, websocket.MessageText)
	defer conn.Close()
	select {
	case <-ctx.Done():
		return provider.ExecResult{}, ctx.Err()
	case <-time.After(time.Second):
	}
	if _, err := io.WriteString(conn, command); err != nil {
		return provider.ExecResult{}, err
	}
	if _, err := readTerminalUntil(conn, ready, nil); err != nil {
		return provider.ExecResult{}, fmt.Errorf("wait for Sevalla stdin readiness: %w", err)
	}
	if err := writeBase64Input(conn, input); err != nil {
		return provider.ExecResult{}, err
	}
	output, err := readTerminalUntil(conn, end, nil)
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("wait for Sevalla stdin command: %w", err)
	}
	position := strings.LastIndex(output, done+":")
	if position < 0 {
		return provider.ExecResult{}, fmt.Errorf("Sevalla stdin command returned no exit marker")
	}
	remainder := output[position+len(done)+1:]
	fields := strings.Fields(strings.ReplaceAll(remainder, "\r", ""))
	if len(fields) == 0 {
		return provider.ExecResult{}, fmt.Errorf("Sevalla stdin command returned no exit status")
	}
	exitCode, err := strconv.Atoi(fields[0])
	if err != nil {
		return provider.ExecResult{}, fmt.Errorf("decode Sevalla stdin exit status: %w", err)
	}
	if opts.Stdout != nil {
		_, _ = io.WriteString(opts.Stdout, output[:position])
	}
	return provider.ExecResult{ExitCode: exitCode, StartedAt: started, FinishedAt: time.Now().UTC()}, nil
}

// Linux N_TTY canonical input is capped at 4096 bytes per line. Keep each
// base64 line below that as well as below Sevalla's WebSocket message limit.
const sevallaStdinFrame = 3 << 10

// websocket.NetConn maps every Write to a WebSocket message. Sevalla accepts
// multi-megabyte stdin streams but rejects a single message of that size, so
// keep frames small. Newlines are valid base64 whitespace and also prevent a
// PTY's canonical input buffer from accumulating one enormous line.
func writeBase64Input(dst io.Writer, input []byte) error {
	encoded := base64.StdEncoding.EncodeToString(input)
	for len(encoded) > 0 {
		size := min(len(encoded), sevallaStdinFrame)
		if _, err := io.WriteString(dst, encoded[:size]+"\n"); err != nil {
			return err
		}
		encoded = encoded[size:]
	}
	_, err := io.WriteString(dst, "\n\x04")
	return err
}

func readTerminalUntil(conn net.Conn, marker string, output io.Writer) (string, error) {
	var collected strings.Builder
	buffer := make([]byte, 4096)
	for collected.Len() <= 2<<20 {
		n, err := conn.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			collected.Write(chunk)
			if output != nil {
				_, _ = output.Write(chunk)
			}
			if strings.Contains(collected.String(), marker) {
				return collected.String(), nil
			}
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return collected.String(), io.ErrUnexpectedEOF
			}
			return collected.String(), err
		}
	}
	return collected.String(), fmt.Errorf("Sevalla terminal output exceeded limit")
}

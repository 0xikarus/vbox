package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xikarus/vmbox-service/internal/provider"
	"github.com/0xikarus/vmbox-service/internal/workerprotocol"
	"github.com/coder/websocket"
)

// Worker connects only to the locally configured controller. A server-provided
// connection cannot redirect the controller credential to another host.
type Worker struct {
	ControllerURL string
	Token         string
	HTTP          *http.Client
}

func (w Worker) ExecConnection(ctx context.Context, conn provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	var stdout, stderr workerCapture
	if opts.Stdout == nil {
		opts.Stdout = &stdout
	} else {
		opts.Stdout = io.MultiWriter(&stdout, opts.Stdout)
	}
	if opts.Stderr == nil {
		opts.Stderr = &stderr
	} else {
		opts.Stderr = io.MultiWriter(&stderr, opts.Stderr)
	}
	result, err := w.StreamConnection(ctx, conn, argv, opts)
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result, err
}

type workerCapture struct{ bytes.Buffer }

func (b *workerCapture) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 16*1024*1024 {
		return 0, errors.New("worker output capture limit exceeded")
	}
	return b.Buffer.Write(data)
}

func (w Worker) StreamConnection(ctx context.Context, conn provider.Connection, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	result := provider.ExecResult{StartedAt: time.Now().UTC()}
	if conn.Transport != "controller-worker" || opts.Interactive || opts.Detach || len(argv) == 0 || w.Token == "" {
		return result, errors.New("invalid direct worker connection options")
	}
	u, err := url.Parse(w.ControllerURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return result, errors.New("direct worker requires an HTTPS controller URL")
	}
	binding := workerprotocol.Binding{AccountID: conn.Metadata["accountId"], BoxID: conn.Metadata["boxId"], SlotID: conn.Metadata["slotId"], Assignment: conn.Metadata["assignment"], Incarnation: conn.Metadata["connectionRevision"]}
	if binding.AccountID == "" || binding.BoxID == "" || binding.SlotID == "" || binding.Assignment == "" || binding.Incarnation == "" {
		return result, errors.New("incomplete worker connection binding")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/logical-boxes/" + binding.BoxID + "/worker/stream"
	u.RawPath = ""
	// Box IDs are UUIDs. Reject path separators rather than allowing handoff
	// metadata to select another controller route.
	if strings.ContainsAny(binding.BoxID, "/\\?#%") || binding.BoxID == "." || binding.BoxID == ".." {
		return result, errors.New("invalid box identity")
	}
	client := http.Client{}
	if w.HTTP != nil {
		client = *w.HTTP
	}
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dialCtx, stopDial := context.WithTimeout(ctx, 15*time.Second)
	ws, response, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPClient: &client, HTTPHeader: http.Header{"Authorization": {"Bearer " + w.Token}}})
	stopDial()
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return result, errors.New("controller worker connection failed")
	}
	peer := workerprotocol.New(ctx, ws, true)
	defer peer.Close()
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return result, err
	}
	stream, err := peer.Open(ctx, workerprotocol.Request{Binding: binding, OperationID: hex.EncodeToString(nonce[:]), Argv: argv})
	if err != nil {
		return result, err
	}
	defer stream.Close()
	if opts.Stdin == nil {
		if err = stream.CloseWrite(); err != nil {
			return result, err
		}
	} else {
		go func() {
			if _, err := io.Copy(stream, opts.Stdin); err != nil {
				cancel()
				return
			}
			_ = stream.CloseWrite()
		}()
	}
	result.ExitCode, err = workerprotocol.ReadOutput(stream, opts.Stdout, opts.Stderr)
	result.FinishedAt = time.Now().UTC()
	return result, err
}

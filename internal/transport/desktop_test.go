package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDesktopTunnelStreamsBinaryAndClosesWithViewer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte{0, 255, 128, 10, 13, 27}, 10000)
	var address string
	err := DesktopTunnel(ctx, func(ctx context.Context, file *os.File) error {
		data := make([]byte, len(payload))
		if _, err := io.ReadFull(file, data); err != nil {
			return err
		}
		if !bytes.Equal(data, payload) {
			t.Error("binary input changed")
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}, func(_ context.Context, target string) error {
		conn, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write(payload); err != nil {
			return err
		}
		data := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, data); err != nil {
			return err
		}
		if !bytes.Equal(data, payload) {
			t.Error("binary output changed")
		}
		return nil
	}, func(target string) { address = target })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("public listener: %s", address)
	}
	if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("viewer exit left the listener open")
	}
}

func TestDesktopViewerExitBeforeConnectDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := DesktopTunnel(ctx, func(context.Context, *os.File) error {
		t.Error("transport started without a viewer")
		return nil
	}, func(context.Context, string) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDesktopTunnelCancellationBeforeConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := DesktopTunnel(ctx, func(context.Context, *os.File) error {
		t.Error("transport started without a viewer")
		return nil
	}, nil, func(string) { cancel() })
	if err != context.Canceled {
		t.Fatalf("cancel returned %v", err)
	}
}

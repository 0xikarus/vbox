package transport

import (
	"context"
	"fmt"
	"net"
	"os"
)

// DesktopTunnel accepts one local viewer and streams it over the caller's
// authenticated transport. It never binds a public interface or retries input.
// The file descriptor avoids os/exec's blocked stdin-copy goroutine on exit.
func DesktopTunnel(ctx context.Context, stream func(context.Context, *os.File) error, viewer func(context.Context, string) error, ready func(string)) error {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fmt.Errorf("open private desktop connection: %w", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	address := listener.Addr().String()
	streamDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptTCP()
		if err != nil {
			streamDone <- err
			return
		}
		listener.Close() // A second local client cannot take over this tunnel.
		file, err := conn.File()
		conn.Close()
		if err != nil {
			streamDone <- err
			return
		}
		defer file.Close()
		streamDone <- stream(ctx, file)
	}()
	if ready != nil {
		ready(address)
	}
	var viewerDone chan error
	if viewer != nil {
		viewerDone = make(chan error, 1)
		go func() { viewerDone <- viewer(ctx, address) }()
	}
	select {
	case err = <-streamDone:
		cancel()
		listener.Close()
		if viewerDone != nil {
			<-viewerDone
		}
		return err
	case err = <-viewerDone:
		cancel()
		listener.Close()
		<-streamDone
		return err
	case <-ctx.Done():
		cancel()
		listener.Close()
		<-streamDone
		if viewerDone != nil {
			<-viewerDone
		}
		return ctx.Err()
	}
}

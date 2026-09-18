package boxruntime

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForDesktopSocketCompletesRFBHandshake(t *testing.T) {
	assignment := "desktop-probe-test"
	socket := desktopSocket(assignment)
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.Remove(socket) })
	served := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		read := func(n int) ([]byte, error) {
			value := make([]byte, n)
			_, err := io.ReadFull(conn, value)
			return value, err
		}
		if _, err = conn.Write([]byte("RFB 003.008\n")); err != nil {
			served <- err
			return
		}
		if version, readErr := read(12); readErr != nil || string(version) != "RFB 003.008\n" {
			served <- fmt.Errorf("client did not complete version handshake: %q %v", version, readErr)
			return
		}
		if _, err = conn.Write([]byte{1, 1}); err != nil {
			served <- err
			return
		}
		if selected, readErr := read(1); readErr != nil || selected[0] != 1 {
			served <- fmt.Errorf("client did not select private None security: %v %v", selected, readErr)
			return
		}
		if _, err = conn.Write([]byte{0, 0, 0, 0}); err != nil {
			served <- err
			return
		}
		if shared, readErr := read(1); readErr != nil || shared[0] != 1 {
			served <- fmt.Errorf("client did not request a shared framebuffer: %v %v", shared, readErr)
			return
		}
		init := make([]byte, 24)
		binary.BigEndian.PutUint16(init, 1280)
		binary.BigEndian.PutUint16(init[2:], 800)
		_, err = conn.Write(init)
		served <- err
	}()

	ready, err := waitForDesktopSocket(context.Background(), assignment, 2*time.Second)
	if err != nil || !ready {
		t.Fatalf("desktop readiness = %t, %v", ready, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

package desktop

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestCaptureRFBPrivateSharedFramebuffer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		read := func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(server, b); return b, err }
		_, err := server.Write([]byte("RFB 003.008\n"))
		if err != nil {
			done <- err
			return
		}
		if _, err = read(12); err != nil {
			done <- err
			return
		}
		server.Write([]byte{1, 1})
		if _, err = read(1); err != nil {
			done <- err
			return
		}
		server.Write([]byte{0, 0, 0, 0})
		shared, err := read(1)
		if err != nil {
			done <- err
			return
		}
		if shared[0] != 1 {
			done <- io.ErrUnexpectedEOF
			return
		}
		init := make([]byte, 24)
		binary.BigEndian.PutUint16(init, 2)
		binary.BigEndian.PutUint16(init[2:], 1)
		server.Write(init)
		if _, err = read(20); err != nil {
			done <- err
			return
		}
		if _, err = read(8); err != nil {
			done <- err
			return
		}
		if _, err = read(10); err != nil {
			done <- err
			return
		}
		// One raw rectangle containing two RGB pixels.
		_, err = server.Write([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 2, 0, 1, 0, 0, 0, 0, 255, 0, 0, 0, 0, 255, 0, 0})
		done <- err
	}()
	frame, err := CaptureRFB(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if frame.Bounds().Dx() != 2 || frame.Pix[0] != 255 || frame.Pix[3] != 255 || frame.Pix[5] != 255 {
		t.Fatalf("incorrect pixels: %v", frame.Pix)
	}
}

func TestCaptureRFBRejectsForeignProtocol(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); server.Write([]byte("HTTP/1.1 200")) }()
	if _, err := CaptureRFB(client); err == nil {
		t.Fatal("accepted non-RFB server")
	}
}

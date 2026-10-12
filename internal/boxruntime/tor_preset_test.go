package boxruntime

import (
	"io"
	"net"
	"testing"
)

func TestSOCKS5ReadinessChecksHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var greeting [3]byte
			if _, err := io.ReadFull(conn, greeting[:]); err == nil && greeting == [3]byte{5, 1, 0} {
				_, _ = conn.Write([]byte{5, 0})
			}
			conn.Close()
		}
	}()
	if !socks5Ready(listener.Addr().String()) {
		t.Fatal("valid SOCKS5 listener was not ready")
	}
}

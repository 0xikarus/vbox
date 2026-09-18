package desktop

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net"
	"time"
)

// CaptureRFB reads a complete framebuffer from an already authenticated private
// worker socket. It deliberately supports only RFB 3.8/None and raw encoding,
// matching the private TigerVNC server. Never use this on a public listener.
func CaptureRFB(conn net.Conn) (*image.RGBA, error) {
	read, write, w, h, err := initializeRFB(conn, 10*time.Second)
	if err != nil {
		return nil, err
	}
	// 32 bits, 24 depth, little endian, true colour, RGB shifts 0/8/16.
	format := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0}
	if err = write(format); err != nil {
		return nil, err
	}
	if err = write([]byte{2, 0, 0, 1, 0, 0, 0, 0}); err != nil {
		return nil, err
	} // Raw only.
	req := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(req[6:], uint16(w))
	binary.BigEndian.PutUint16(req[8:], uint16(h))
	if err = write(req); err != nil {
		return nil, err
	}
	frame := image.NewRGBA(image.Rect(0, 0, w, h))
	covered := make([]bool, w*h)
	remaining := w * h
	for messages := 0; messages < 256; messages++ {
		kind, err := read(1)
		if err != nil {
			return nil, err
		}
		switch kind[0] {
		case 0:
			header, err := read(3)
			if err != nil {
				return nil, err
			}
			n := int(binary.BigEndian.Uint16(header[1:]))
			if n > 4096 {
				return nil, fmt.Errorf("too many desktop rectangles")
			}
			for i := 0; i < n; i++ {
				r, err := read(12)
				if err != nil {
					return nil, err
				}
				x, y := int(binary.BigEndian.Uint16(r)), int(binary.BigEndian.Uint16(r[2:]))
				rw, rh := int(binary.BigEndian.Uint16(r[4:])), int(binary.BigEndian.Uint16(r[6:]))
				if binary.BigEndian.Uint32(r[8:]) != 0 || rw == 0 || rh == 0 || x+rw > w || y+rh > h {
					return nil, fmt.Errorf("invalid desktop rectangle")
				}
				row := make([]byte, rw*4)
				for yy := y; yy < y+rh; yy++ {
					if _, err = io.ReadFull(conn, row); err != nil {
						return nil, err
					}
					for xx := 0; xx < rw; xx++ {
						p := yy*w + x + xx
						copy(frame.Pix[p*4:p*4+3], row[xx*4:xx*4+3])
						frame.Pix[p*4+3] = 255
						if !covered[p] {
							covered[p] = true
							remaining--
						}
					}
				}
			}
			if remaining == 0 {
				return frame, nil
			}
			if err = write(req); err != nil {
				return nil, err
			}
		case 2: // Bell has no payload.
		case 3: // Clipboard is private data, discard without logging or returning it.
			header, err := read(7)
			if err != nil {
				return nil, err
			}
			n := binary.BigEndian.Uint32(header[3:])
			if n > 1<<20 {
				return nil, fmt.Errorf("desktop clipboard exceeds limit")
			}
			if _, err = io.CopyN(io.Discard, conn, int64(n)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported desktop message")
		}
	}
	return nil, fmt.Errorf("desktop did not return a complete frame")
}

// ProbeRFB completes a shared, unauthenticated RFB handshake on the private
// desktop socket. Unlike a bare connect-and-close readiness probe, this is a
// valid RFB client session and does not make TigerVNC blacklist the socket.
func ProbeRFB(conn net.Conn) error {
	_, _, _, _, err := initializeRFB(conn, 2*time.Second)
	return err
}

func initializeRFB(conn net.Conn, timeout time.Duration) (func(int) ([]byte, error), func([]byte) error, int, int, error) {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, 0, 0, err
	}
	read := func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(conn, b); return b, err }
	write := func(b []byte) error {
		for len(b) > 0 {
			n, err := conn.Write(b)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			b = b[n:]
		}
		return nil
	}
	version, err := read(12)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if string(version) != "RFB 003.008\n" {
		return nil, nil, 0, 0, fmt.Errorf("unsupported desktop protocol")
	}
	if err = write(version); err != nil {
		return nil, nil, 0, 0, err
	}
	count, err := read(1)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if count[0] == 0 {
		return nil, nil, 0, 0, fmt.Errorf("desktop rejected connection")
	}
	types, err := read(int(count[0]))
	if err != nil {
		return nil, nil, 0, 0, err
	}
	none := false
	for _, v := range types {
		if v == 1 {
			none = true
		}
	}
	if !none {
		return nil, nil, 0, 0, fmt.Errorf("private desktop security mode unavailable")
	}
	if err = write([]byte{1}); err != nil {
		return nil, nil, 0, 0, err
	}
	security, err := read(4)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if binary.BigEndian.Uint32(security) != 0 {
		return nil, nil, 0, 0, fmt.Errorf("desktop authentication failed")
	}
	if err = write([]byte{1}); err != nil {
		return nil, nil, 0, 0, err
	} // Shared: never evict a human viewer.
	init, err := read(24)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	w, h := int(binary.BigEndian.Uint16(init)), int(binary.BigEndian.Uint16(init[2:]))
	if w == 0 || h == 0 || w > 4096 || h > 4096 {
		return nil, nil, 0, 0, fmt.Errorf("desktop dimensions exceed capture limit")
	}
	nameLen := binary.BigEndian.Uint32(init[20:])
	if nameLen > 4096 {
		return nil, nil, 0, 0, fmt.Errorf("desktop name exceeds limit")
	}
	if _, err = read(int(nameLen)); err != nil {
		return nil, nil, 0, 0, err
	}
	return read, write, w, h, nil
}

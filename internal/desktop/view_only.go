package desktop

import (
	"encoding/binary"
	"fmt"
	"io"
)

// FilterViewOnlyRFB forwards display requests for our private RFB 3.8/None
// server. Input, clipboard writes and desktop resizing are consumed locally.
// Unknown extensions fail closed, including file transfer and power control.
// Framing is independent of WebSocket boundaries and limits every allocation.
func FilterViewOnlyRFB(dst io.Writer, src io.Reader) error {
	read := func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(src, b); return b, err }
	version, err := read(12)
	if err != nil {
		return err
	}
	if string(version) != "RFB 003.008\n" {
		return fmt.Errorf("view requires RFB 3.8")
	}
	if _, err = dst.Write(version); err != nil {
		return err
	}
	security, err := read(1)
	if err != nil {
		return err
	}
	if security[0] != 1 {
		return fmt.Errorf("unsupported security type")
	}
	if _, err = dst.Write(security); err != nil {
		return err
	}
	if _, err = read(1); err != nil {
		return err
	}
	if _, err = dst.Write([]byte{1}); err != nil {
		return err
	} // Always shared: never disconnect another viewer.
	for {
		kind, err := read(1)
		if err != nil {
			return err
		}
		size, forward := 0, false
		switch kind[0] {
		case 0:
			size, forward = 19, true
		case 2:
			size, forward = 3, true
		case 3, 150:
			size, forward = 9, true
		case 4:
			size = 7
		case 5:
			size = 5
		case 6:
			size = 7
		case 248:
			size, forward = 8, true
		case 251:
			size = 7
		default:
			return fmt.Errorf("unsupported view message")
		}
		header, err := read(size)
		if err != nil {
			return err
		}
		extra := 0
		switch kind[0] {
		case 2:
			extra = int(binary.BigEndian.Uint16(header[1:])) * 4
			if extra > 4096 {
				return fmt.Errorf("too many encodings")
			}
		case 6:
			n := int64(int32(binary.BigEndian.Uint32(header[3:])))
			if n < 0 {
				n = -n
			}
			if n > 1<<20 {
				return fmt.Errorf("clipboard too large")
			}
			extra = int(n)
		case 248:
			extra = int(header[7])
			if extra > 64 {
				return fmt.Errorf("fence too large")
			}
		case 251:
			extra = int(header[5]) * 16
		}
		tail, err := read(extra)
		if err != nil {
			return err
		}
		if forward {
			packet := append(append(kind, header...), tail...)
			if _, err = dst.Write(packet); err != nil {
				return err
			}
		}
	}
}

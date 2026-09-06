package boxruntime

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTerminalFramesPreserveBytesAndOrdering(t *testing.T) {
	data := []byte("üßzY\n\r\x7f\x1b[A\x03\x00")
	var input, output bytes.Buffer
	enc := json.NewEncoder(&input)
	_ = enc.Encode(TerminalFrame{Data: data[:5]})
	_ = enc.Encode(TerminalFrame{Cols: 120, Rows: 40})
	_ = enc.Encode(TerminalFrame{Data: data[5:]})
	resized := 0
	err := ApplyTerminalFrames(&input, &output, func(cols, rows uint16) error {
		resized++
		if cols != 120 || rows != 40 {
			t.Fatal("size changed")
		}
		return nil
	})
	if err != nil || resized != 1 || !bytes.Equal(data, output.Bytes()) {
		t.Fatal("terminal frame corruption", err)
	}
}

func TestTerminalFramesRejectInvalidInput(t *testing.T) {
	for _, data := range []string{"{}\n", "{\"cols\":501,\"rows\":24}\n", "{\"data\":\"YQ==\",\"cols\":80}\n", "bad\n"} {
		if err := ApplyTerminalFrames(bytes.NewBufferString(data), &bytes.Buffer{}, func(uint16, uint16) error { return nil }); err == nil {
			t.Fatal("accepted invalid frame")
		}
	}
}

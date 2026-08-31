package sevalla

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

type frameWriter struct{ frames [][]byte }

func (w *frameWriter) Write(data []byte) (int, error) {
	w.frames = append(w.frames, append([]byte(nil), data...))
	return len(data), nil
}

func TestWriteBase64InputUsesBoundedFramesAndPreservesBytes(t *testing.T) {
	input := bytes.Repeat([]byte("vmbox-binary\x00"), 10000)
	var dst frameWriter
	if err := writeBase64Input(&dst, input); err != nil {
		t.Fatal(err)
	}
	var wire strings.Builder
	for _, frame := range dst.frames {
		if len(frame) > sevallaStdinFrame+1 {
			t.Fatalf("frame size=%d", len(frame))
		}
		wire.Write(frame)
	}
	encoded := strings.TrimSuffix(wire.String(), "\n\x04")
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encoded, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, input) {
		t.Fatal("chunked stdin changed the payload")
	}
}

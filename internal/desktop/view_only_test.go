package desktop

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestViewOnlyRFBFiltersInputAcrossArbitraryReads(t *testing.T) {
	handshake := append([]byte("RFB 003.008\n"), 1, 0)
	input := append([]byte{}, handshake...)
	// Input and clipboard packets contain bytes that look like valid messages;
	// framing must not reinterpret their payload as new commands.
	input = append(input, 4, 1, 0, 0, 0, 0, 0, 65, 5, 1, 0, 10, 0, 20)
	input = append(input, 6, 0, 0, 0, 0, 0, 0, 4, 3, 0, 0, 0)
	input = append(input, 251, 0, 0, 80, 0, 60, 1, 0)
	input = append(input, make([]byte, 16)...)
	update := []byte{3, 1, 0, 0, 0, 0, 0, 80, 0, 60}
	input = append(input, update...)
	var out bytes.Buffer
	err := FilterViewOnlyRFB(&out, &oneByteReader{bytes.NewReader(input)})
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	expected := append(append([]byte{}, handshake[:13]...), 1)
	expected = append(expected, update...)
	if !bytes.Equal(out.Bytes(), expected) {
		t.Fatalf("forwarded unexpected commands: %x", out.Bytes())
	}
}

type oneByteReader struct{ io.Reader }

func (r *oneByteReader) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return r.Reader.Read(b)
}
func TestViewOnlyRFBRejectsExtensionsAndOversizedPayload(t *testing.T) {
	for _, packet := range [][]byte{{250, 0, 1, 2}, {7, 0}, {255, 0}, {248, 0, 0, 0, 0, 0, 0, 0, 65}, {2, 0, 255, 255}, {6, 0, 0, 0, 127, 255, 255, 255}} {
		var out bytes.Buffer
		err := FilterViewOnlyRFB(&out, bytes.NewReader(append(append([]byte("RFB 003.008\n"), 1, 1), packet...)))
		if err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("accepted unsafe packet %x", packet)
		}
		if out.Len() != 14 {
			t.Fatalf("forwarded unsafe packet %x", out.Bytes())
		}
	}
}
func TestViewOnlyRFBConsumesExtendedClipboard(t *testing.T) {
	packet := []byte{6, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(packet[4:], ^uint32(3))
	packet = append(packet, 0, 0, 0, 0)
	var out bytes.Buffer
	err := FilterViewOnlyRFB(&out, bytes.NewReader(append(append([]byte("RFB 003.008\n"), 1, 1), packet...)))
	if !errors.Is(err, io.EOF) || out.Len() != 14 {
		t.Fatalf("err=%v output=%x", err, out.Bytes())
	}
}

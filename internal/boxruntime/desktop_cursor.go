package boxruntime

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const screenshotHeaderHeight = 24
const cursorPNGKeyword = "vbox-cursor\x00"

// Read the real X11 pointer, including owner movements. Failure must not prevent
// a screenshot or invent a location. This neither moves nor focuses anything.
func captureCursor(ctx context.Context, bounds image.Rectangle) *image.Point {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := desktopInputCommand(ctx, "", "getmouselocation", "--shell")
	if err != nil {
		return nil
	}
	values := make(map[string]int)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "X" && key != "Y") {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil
		}
		values[key] = n
	}
	p := image.Pt(values["X"], values["Y"])
	if len(values) != 2 || !p.In(bounds) {
		return nil
	}
	return &p
}

// Carry the capture-time pointer through the existing PNG-only worker and
// controller transports. Ordinary previews keep their dimensions and pixels;
// only MCP results render the metadata as a separate band in the image canvas.
func encodeCapture(output io.Writer, frame image.Image, cursor *image.Point) error {
	if cursor == nil {
		return png.Encode(output, frame)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, frame); err != nil {
		return err
	}
	data := []byte(fmt.Sprintf("%s%d %d", cursorPNGKeyword, cursor.X, cursor.Y))
	chunk := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(chunk, uint32(len(data)))
	copy(chunk[4:], "tEXt")
	copy(chunk[8:], data)
	binary.BigEndian.PutUint32(chunk[8+len(data):], crc32.ChecksumIEEE(chunk[4:8+len(data)]))
	// png.Encode always ends in the twelve-byte IEND chunk.
	pixels := encoded.Bytes()
	_, err := io.Copy(output, io.MultiReader(bytes.NewReader(pixels[:len(pixels)-12]), bytes.NewReader(chunk), bytes.NewReader(pixels[len(pixels)-12:])))
	return err
}

func capturePNGCursor(pixels []byte) *image.Point {
	if !bytes.HasPrefix(pixels, []byte("\x89PNG\r\n\x1a\n")) {
		return nil
	}
	for rest := pixels[8:]; len(rest) >= 12; {
		n := uint64(binary.BigEndian.Uint32(rest))
		if n > uint64(len(rest)-12) {
			return nil
		}
		data := rest[8 : 8+int(n)]
		if string(rest[4:8]) == "tEXt" && bytes.HasPrefix(data, []byte(cursorPNGKeyword)) {
			if len(data) > 64 || crc32.ChecksumIEEE(rest[4:8+int(n)]) != binary.BigEndian.Uint32(rest[8+int(n):]) {
				return nil
			}
			fields := strings.Fields(string(data[len(cursorPNGKeyword):]))
			if len(fields) != 2 {
				return nil
			}
			x, ex := strconv.Atoi(fields[0])
			y, ey := strconv.Atoi(fields[1])
			if ex != nil || ey != nil || x < 0 || y < 0 || x >= 4096 || y >= 4096 {
				return nil
			}
			return &image.Point{X: x, Y: y}
		}
		rest = rest[12+int(n):]
	}
	return nil
}

// Add canvas ABOVE the screenshot, never on top of desktop pixels. Apply after
// thumbnailing/cropping so the label stays readable and uses desktop coordinates.
func screenshotWithCursorHeader(pixels []byte) ([]byte, error) {
	config, err := png.DecodeConfig(bytes.NewReader(pixels))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return nil, fmt.Errorf("invalid desktop screenshot")
	}
	frame, err := png.Decode(bytes.NewReader(pixels))
	if err != nil {
		return nil, err
	}
	label := "Cursor: unavailable"
	if cursor := capturePNGCursor(pixels); cursor != nil {
		label = fmt.Sprintf("Cursor: %dpx %dpx", cursor.X, cursor.Y)
	}
	width := max(frame.Bounds().Dx(), font.MeasureString(basicfont.Face7x13, label).Ceil()+16)
	result := image.NewRGBA(image.Rect(0, 0, width, frame.Bounds().Dy()+screenshotHeaderHeight))
	draw.Draw(result, result.Bounds(), image.NewUniform(color.RGBA{R: 24, G: 26, B: 30, A: 255}), image.Point{}, draw.Src)
	draw.Draw(result, image.Rect(0, screenshotHeaderHeight, frame.Bounds().Dx(), result.Bounds().Max.Y), frame, frame.Bounds().Min, draw.Src)
	drawer := font.Drawer{Dst: result, Src: image.White, Face: basicfont.Face7x13, Dot: fixed.P(8, 16)}
	drawer.DrawString(label)
	var encoded bytes.Buffer
	err = png.Encode(&encoded, result)
	return encoded.Bytes(), err
}

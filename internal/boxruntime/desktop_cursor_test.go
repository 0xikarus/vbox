package boxruntime

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func assertScreenshotPixelsPreserved(t *testing.T, original, annotated []byte) {
	t.Helper()
	src, err := png.Decode(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	dst, err := png.Decode(bytes.NewReader(annotated))
	if err != nil {
		t.Fatal(err)
	}
	if dst.Bounds().Dx() < src.Bounds().Dx() || dst.Bounds().Dy() != src.Bounds().Dy()+24 {
		t.Fatalf("footer changed content dimensions: %v -> %v", src.Bounds(), dst.Bounds())
	}
	for y := 0; y < src.Bounds().Dy(); y++ {
		for x := 0; x < src.Bounds().Dx(); x++ {
			before := color.NRGBAModel.Convert(src.At(x, y))
			after := color.NRGBAModel.Convert(dst.At(x, y))
			if before != after {
				t.Fatalf("covered or changed desktop pixel at %d,%d: %v != %v", x, y, before, after)
			}
		}
	}
	// The bottom-left footer must contain visible lettering, not just empty padding.
	ink := 0
	for y := src.Bounds().Dy(); y < dst.Bounds().Dy(); y++ {
		for x := 8; x < 56; x++ {
			r, g, b, _ := dst.At(x, y).RGBA()
			if r > 50000 && g > 50000 && b > 50000 {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Fatal("footer has no visible text at the top left")
	}
}

func TestScreenshotCursorFooterPreservesContent(t *testing.T) {
	for _, size := range []image.Point{{1024, 640}, {320, 200}, {23, 15}} {
		t.Run(size.String(), func(t *testing.T) {
			frame := image.NewRGBA(image.Rectangle{Max: size})
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					frame.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
				}
			}
			// Desktop coordinates must survive a thumbnail or a window crop even
			// when the cursor is outside that image's smaller pixel dimensions.
			cursor := image.Pt(1023, 639)
			var captured bytes.Buffer
			if err := encodeCapture(&captured, frame, &cursor); err != nil {
				t.Fatal(err)
			}
			if got := capturePNGCursor(captured.Bytes()); got == nil || *got != cursor {
				t.Fatalf("lost original desktop coordinates: %v", got)
			}
			annotated, err := screenshotWithCursorFooter(captured.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			assertScreenshotPixelsPreserved(t, captured.Bytes(), annotated)
			cursor.X = 0
			var moved bytes.Buffer
			if err := encodeCapture(&moved, frame, &cursor); err != nil {
				t.Fatal(err)
			}
			movedFooter, err := screenshotWithCursorFooter(moved.Bytes())
			if err != nil || bytes.Equal(annotated, movedFooter) {
				t.Fatal("pointer movement did not change visible footer", err)
			}
		})
	}
}

func TestScreenshotCursorUnavailableAndMalformedMetadata(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 320, 200))
	var plain bytes.Buffer
	if err := encodeCapture(&plain, frame, nil); err != nil {
		t.Fatal(err)
	}
	if capturePNGCursor(plain.Bytes()) != nil {
		t.Fatal("invented cursor for legacy capture")
	}
	annotated, err := screenshotWithCursorFooter(plain.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	assertScreenshotPixelsPreserved(t, plain.Bytes(), annotated)
	var captured bytes.Buffer
	cursor := image.Pt(15, 20)
	if err := encodeCapture(&captured, frame, &cursor); err != nil {
		t.Fatal(err)
	}
	data := captured.Bytes()
	for i := 0; i < len(data)-12; i++ {
		if capturePNGCursor(data[:i]) != nil {
			t.Fatalf("accepted truncated cursor chunk at %d", i)
		}
	}
	corrupt := bytes.Clone(data)
	corrupt[bytes.Index(corrupt, []byte(cursorPNGKeyword))+len(cursorPNGKeyword)] ^= 1
	if capturePNGCursor(corrupt) != nil {
		t.Fatal("accepted corrupt metadata CRC")
	}
	for _, bad := range []image.Point{{-1, 0}, {4096, 0}, {0, 4096}} {
		captured.Reset()
		if err := encodeCapture(&captured, frame, &bad); err != nil {
			t.Fatal(err)
		}
		if capturePNGCursor(captured.Bytes()) != nil {
			t.Fatalf("accepted out-of-range coordinates %v", bad)
		}
	}
	if _, err := screenshotWithCursorFooter([]byte("not a PNG")); err == nil {
		t.Fatal("accepted non-image")
	}
}

func TestCaptureCursorReadsPointerWithoutInput(t *testing.T) {
	dir := t.TempDir()
	// Reject any invocation that would move, focus, or query another display.
	script := "#!/bin/sh\n[ \"$*\" = 'getmouselocation --shell' ] || exit 1\n[ \"$DISPLAY\" = ':123' ] || exit 1\nprintf '%s\\n' \"$CURSOR_FIXTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "xdotool"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("VMBOX_DESKTOP_DISPLAY", ":123")
	for _, tc := range []struct {
		output string
		want   *image.Point
	}{
		{"X=650\nY=500\nSCREEN=0\nWINDOW=42", &image.Point{650, 500}},
		{"X=0\nY=0", &image.Point{0, 0}},
		{"X=1024\nY=5", nil},
		{"X=10", nil},
		{"X=oops\nY=5", nil},
	} {
		t.Setenv("CURSOR_FIXTURE", tc.output)
		got := captureCursor(context.Background(), image.Rect(0, 0, 1024, 640))
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Fatalf("pointer %q: got %v, want %v", tc.output, got, tc.want)
		}
	}
}

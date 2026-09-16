package boxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"testing"
)

func TestWindowGeometry(t *testing.T) {
	bounds, err := windowGeometry("WINDOW=42\nX=-20\nY=30\nWIDTH=200\nHEIGHT=100\nSCREEN=0\n")
	if err != nil || bounds != image.Rect(-20, 30, 180, 130) {
		t.Fatalf("unexpected bounds %v: %v", bounds, err)
	}
	for _, output := range []string{
		"X=0\nY=0\nWIDTH=0\nHEIGHT=20",
		"X=0\nY=0\nWIDTH=20",
		"X=oops\nY=0\nWIDTH=20\nHEIGHT=20",
		"X=999999999999999999\nY=0\nWIDTH=20\nHEIGHT=20",
	} {
		if _, err := windowGeometry(output); err == nil {
			t.Fatalf("accepted malformed geometry: %q", output)
		}
	}
}

func TestWindowID(t *testing.T) {
	for _, value := range []string{"42", "0x2a", "00042"} {
		if identifier, err := windowID(value); err != nil || identifier != "42" {
			t.Fatalf("invalid normalization %q: %s, %v", value, identifier, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "0x0", "4294967296", "42;id", "--shell", "42 getactivewindow"} {
		if _, err := windowID(value); err == nil {
			t.Fatalf("accepted invalid window ID %q", value)
		}
	}
}

func TestCaptureWindowRejectsInvalidArgumentsAndAssignment(t *testing.T) {
	for _, arguments := range []string{`{"window_id":null}`, `{"window_id":42}`, `{"window_id":""}`, `{"window_id":"--shell"}`, `{"command":"id"}`} {
		if _, err := callDesktopTool(context.Background(), "invalid", "capture_window", json.RawMessage(arguments)); err == nil {
			t.Fatalf("accepted invalid arguments %s", arguments)
		}
	}
	var output bytes.Buffer
	if _, err := CaptureWindow(context.Background(), "invalid", "42", &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid assignment must not release pixels")
	}
}

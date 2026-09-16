package boxruntime

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"strconv"
	"strings"
)

func windowID(value string) (string, error) {
	base := 10
	if strings.HasPrefix(value, "0x") {
		base = 16
		value = strings.TrimPrefix(value, "0x")
	}
	parsed, err := strconv.ParseUint(value, base, 32)
	if err != nil || parsed == 0 {
		return "", fmt.Errorf("window_id must be a positive decimal or hexadecimal X11 window ID")
	}
	return strconv.FormatUint(parsed, 10), nil
}

func windowGeometry(output string) (image.Rectangle, error) {
	values := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "X" && key != "Y" && key != "WIDTH" && key != "HEIGHT") {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < -1000000 || number > 1000000 {
			return image.Rectangle{}, fmt.Errorf("invalid window geometry")
		}
		values[key] = number
	}
	if len(values) != 4 || values["WIDTH"] <= 0 || values["HEIGHT"] <= 0 {
		return image.Rectangle{}, fmt.Errorf("window geometry unavailable")
	}
	return image.Rect(values["X"], values["Y"], values["X"]+values["WIDTH"], values["Y"]+values["HEIGHT"]), nil
}

func CaptureWindow(ctx context.Context, assignment, requestedID string, output io.Writer) (image.Rectangle, error) {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return image.Rectangle{}, err
	}
	if requestedID == "" {
		active, err := desktopInputCommand(ctx, "", "getactivewindow")
		if err != nil {
			return image.Rectangle{}, fmt.Errorf("no active desktop window")
		}
		requestedID = strings.TrimSpace(string(active))
	}
	identifier, err := windowID(requestedID)
	if err != nil {
		return image.Rectangle{}, err
	}
	visible, err := desktopInputCommand(ctx, "", "search", "--onlyvisible", "--name", ".*")
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("visible windows unavailable")
	}
	found := false
	for _, candidate := range strings.Fields(string(visible)) {
		if candidate == identifier {
			found = true
			break
		}
	}
	if !found {
		return image.Rectangle{}, fmt.Errorf("window is not visible")
	}
	geometry, err := desktopInputCommand(ctx, "", "getwindowgeometry", "--shell", identifier)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("window geometry unavailable")
	}
	bounds, err := windowGeometry(string(geometry))
	if err != nil {
		return image.Rectangle{}, err
	}
	var desktopPNG bytes.Buffer
	if err := CaptureDesktop(ctx, assignment, &desktopPNG); err != nil {
		return image.Rectangle{}, err
	}
	frame, err := png.Decode(&desktopPNG)
	if err != nil {
		return image.Rectangle{}, err
	}
	bounds = bounds.Intersect(frame.Bounds())
	if bounds.Empty() {
		return image.Rectangle{}, fmt.Errorf("window is outside the visible desktop")
	}
	view := frame.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(bounds)
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return image.Rectangle{}, err
	}
	return bounds, png.Encode(output, view)
}

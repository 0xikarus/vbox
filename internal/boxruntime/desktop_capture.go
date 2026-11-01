package boxruntime

import (
	"context"
	"image/png"
	"io"
	"net"
	"time"

	"github.com/0xikarus/vmbox-service/internal/desktop"
)

// CaptureDesktop captures inside the worker without starting the desktop or
// requiring a viewer. Recheck the assignment before releasing private pixels.
func CaptureDesktop(ctx context.Context, assignment string, output io.Writer) error {
	return captureDesktop(ctx, assignment, output, false)
}

func CaptureDesktopThumbnail(ctx context.Context, assignment string, output io.Writer) error {
	return captureDesktop(ctx, assignment, output, true)
}

func captureDesktop(ctx context.Context, assignment string, output io.Writer, thumbnail bool) error {
	if _, err := NativeSessions(ctx, assignment); err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", desktopSocket(assignment))
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	frame, err := desktop.CaptureRFB(conn)
	if err != nil {
		return err
	}
	if _, err = NativeSessions(ctx, assignment); err != nil {
		return err
	}
	if thumbnail {
		frame = desktop.Thumbnail(frame, 320, 200)
	}
	return png.Encode(output, frame)
}

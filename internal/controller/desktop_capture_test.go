package controller

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/0xikarus/vmbox-service/internal/provider"
)

type captureProvider struct {
	fakeProvider
	data    []byte
	useSink bool
}

func (p *captureProvider) Exec(_ context.Context, _ string, argv []string, opts provider.ExecOptions) (provider.ExecResult, error) {
	p.argv = argv
	if p.useSink {
		_, err := opts.Stdout.Write(p.data)
		return provider.ExecResult{}, err
	}
	return provider.ExecResult{Stdout: string(p.data)}, nil
}

func TestReadDesktopCaptureBothProviderOutputModes(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 320, 200))); err != nil {
		t.Fatal(err)
	}
	for _, sink := range []bool{false, true} {
		p := &captureProvider{data: pngData.Bytes(), useSink: sink}
		got, err := readDesktopCapture(context.Background(), p, "service", "fence", true)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pngData.Bytes()) || strings.Join(p.argv, " ") != "vmbox-runtime desktop-thumbnail fence" {
			t.Fatal("capture altered or wrong command")
		}
	}
}

func TestReadDesktopCaptureRejectsNonImageAndOversize(t *testing.T) {
	for _, data := range [][]byte{[]byte("worker failed"), make([]byte, desktopCaptureLimit+1)} {
		if _, err := readDesktopCapture(context.Background(), &captureProvider{data: data}, "s", "f", false); err == nil {
			t.Fatal("accepted invalid capture")
		}
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 640, 400)))
	if _, err := readDesktopCapture(context.Background(), &captureProvider{data: pngData.Bytes()}, "s", "f", true); err == nil {
		t.Fatal("accepted oversized thumbnail")
	}
}

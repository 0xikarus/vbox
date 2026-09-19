package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEncodeDesktopReplayBoundsImage(t *testing.T) {
	image := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1280; x++ {
			image.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, image); err != nil {
		t.Fatal(err)
	}
	frame, width, height, err := encodeDesktopReplay(source.Bytes())
	if err != nil || width != 960 || height != 600 || len(frame) > desktopReplayMaxBytes {
		t.Fatalf("bounded replay frame: %dx%d bytes=%d err=%v", width, height, len(frame), err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(frame))
	if err != nil || config.Width != width || config.Height != height {
		t.Fatalf("replay is not a valid bounded JPEG: %+v %v", config, err)
	}
}

func testDesktopReplayRoutes(t *testing.T, store *Store, owner, other Principal, box, frameID string, frameBytes []byte) {
	t.Helper()
	server := NewServer(store, nil)
	call := func(handler func(http.ResponseWriter, *http.Request, Principal), principal Principal) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", "/", nil)
		request.SetPathValue("id", box)
		request.SetPathValue("frame", frameID)
		response := httptest.NewRecorder()
		handler(response, request, principal)
		return response
	}
	response := call(server.desktopReplayList, owner)
	if response.Code != 200 {
		t.Fatalf("owner replay list: %d %s", response.Code, response.Body.String())
	}
	var frames []desktopReplayFrameInfo
	if err := json.Unmarshal(response.Body.Bytes(), &frames); err != nil || len(frames) != 1 || frames[0].ID != frameID {
		t.Fatalf("replay metadata: %+v %v", frames, err)
	}
	if response = call(server.desktopReplayList, other); response.Code != 404 {
		t.Fatal("cross-account replay metadata exposed")
	}
	response = call(server.desktopReplayFrame, owner)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), frameBytes) || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("owner replay image unavailable or cacheable")
	}
	if response = call(server.desktopReplayFrame, other); response.Code != 404 {
		t.Fatal("cross-account replay image exposed")
	}
	if _, err := store.DB.Exec(`UPDATE desktop_replay_frames SET captured_at=now()-interval '31 minutes' WHERE id=$1`, frameID); err != nil {
		t.Fatal(err)
	}
	if response = call(server.desktopReplayList, owner); response.Code != 200 || response.Body.String() != "[]\n" {
		t.Fatal("expired replay frame remained visible")
	}
	if response = call(server.desktopReplayFrame, owner); response.Code != 404 {
		t.Fatal("expired replay image remained visible")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.ReconcileDesktopReplayNow(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.DB.QueryRow(`SELECT count(*) FROM desktop_replay_frames WHERE id=$1`, frameID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("expired replay image was not purged", remaining, err)
	}
}

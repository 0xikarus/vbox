package controller

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestValidateRunOnceImage(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 12, 7))); err != nil {
		t.Fatal(err)
	}
	media, err := validateRunOnceImage(data.Bytes())
	if err != nil || media != "image/png" {
		t.Fatalf("%s %v", media, err)
	}
	for _, data := range [][]byte{nil, []byte("<svg onload='alert(1)'/>"), make([]byte, (25<<20)+1)} {
		if _, err := validateRunOnceImage(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}

func TestValidateRunOnceVideoContainers(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18}, []byte("ftypisom")...)
	mp4 = append(mp4, make([]byte, 32)...)
	if media, err := validateRunOnceImage(mp4); err != nil || media != "video/mp4" {
		t.Fatalf("mp4: %q %v", media, err)
	}
	webm := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 32)...)
	if media, err := validateRunOnceImage(webm); err != nil || media != "video/webm" {
		t.Fatalf("webm: %q %v", media, err)
	}
	// An oversized non-video blob is still rejected as an image.
	if _, err := validateRunOnceImage(make([]byte, (25<<20)+1)); err == nil {
		t.Fatal("oversized image accepted")
	}
}

// HEIC and AVIF stills ride in the same ISO base-media container as MP4 and
// are told apart only by the brand. Accepting them as video/mp4 would store a
// still that, under nosniff, renders as neither picture nor clip.
func TestValidateRunOnceRejectsStillISOContainers(t *testing.T) {
	for _, brand := range []string{"heic", "avif", "mif1", "heix"} {
		still := append([]byte{0, 0, 0, 0x18}, []byte("ftyp"+brand)...)
		still = append(still, make([]byte, 32)...)
		if media, err := validateRunOnceImage(still); err == nil {
			t.Fatalf("brand %q accepted as %q", brand, media)
		}
	}
}

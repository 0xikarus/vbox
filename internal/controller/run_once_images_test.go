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
	for _, data := range [][]byte{nil, []byte("<svg onload='alert(1)'/>"), make([]byte, (8<<20)+1)} {
		if _, err := validateRunOnceImage(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}

package controller

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"
)

func TestOptimizeStoredImageShrinksLargeOpaquePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3000, 200))
	rng := rand.New(rand.NewSource(29))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	optimized, media, err := optimizeStoredImage(context.Background(), original.Bytes(), "image/png")
	if err != nil || media != "image/jpeg" || len(optimized) >= original.Len()/2 {
		t.Fatalf("image was not meaningfully optimized: %d -> %d bytes, media %s, error %v", original.Len(), len(optimized), media, err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(optimized))
	if err != nil || config.Width > maxStoredImageDimension || config.Height > maxStoredImageDimension {
		t.Fatalf("optimized dimensions: %+v error %v", config, err)
	}
}

func TestOptimizeStoredImagePreservesSmallAndAnimatedMedia(t *testing.T) {
	for _, media := range []string{"image/png", "image/gif"} {
		original := bytes.Repeat([]byte{0x42}, 1024)
		optimized, resultMedia, err := optimizeStoredImage(context.Background(), original, media)
		if err != nil || resultMedia != media || !bytes.Equal(optimized, original) {
			t.Fatalf("%s changed: media=%s error=%v", media, resultMedia, err)
		}
	}
}

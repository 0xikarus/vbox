package controller

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

const (
	minImageOptimizationBytes = 256 << 10
	maxStoredImageDimension   = 2560
)

// Decoding a valid 40-megapixel upload can use hundreds of MiB. Bound the
// number of simultaneous encoders, including agent-generated attachments.
var imageOptimizationSlot = make(chan struct{}, 1)

// optimizeStoredImage keeps small inputs and animated GIFs intact. Larger
// opaque stills become bounded JPEGs; transparent PNGs stay PNG. Only a smaller
// result replaces the original, so optimization can never worsen the quota.
func optimizeStoredImage(ctx context.Context, data []byte, media string) ([]byte, string, error) {
	if len(data) < minImageOptimizationBytes || media == "image/gif" || (media != "image/jpeg" && media != "image/png") {
		return data, media, nil
	}
	select {
	case imageOptimizationSlot <- struct{}{}:
		defer func() { <-imageOptimizationSlot }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// DecodeConfig already validated the upload. Preserve its original bytes
		// if the full decoder cannot optimize it.
		return data, media, nil
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > maxStoredImageDimension || height > maxStoredImageDimension {
		scale := float64(maxStoredImageDimension) / float64(max(width, height))
		resized := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))))
		draw.CatmullRom.Scale(resized, resized.Bounds(), decoded, bounds, draw.Src, nil)
		decoded = resized
	}
	targetMedia := "image/jpeg"
	if media == "image/png" {
		if opaque, ok := decoded.(interface{ Opaque() bool }); !ok || !opaque.Opaque() {
			targetMedia = "image/png"
		}
	}
	var output bytes.Buffer
	if targetMedia == "image/png" {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&output, decoded)
	} else {
		err = jpeg.Encode(&output, decoded, &jpeg.Options{Quality: 74})
	}
	if err != nil || output.Len() >= len(data) {
		return data, media, nil
	}
	return output.Bytes(), targetMedia, nil
}

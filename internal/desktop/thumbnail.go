package desktop

import "image"

// Thumbnail bounds both dimensions without upscaling. Capture always occurs in
// the worker; this reduction avoids sending full frames for small UI previews.
func Thumbnail(src *image.RGBA, maxWidth, maxHeight int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if maxWidth <= 0 || maxHeight <= 0 || (w <= maxWidth && h <= maxHeight) {
		return src
	}
	nw, nh := maxWidth, h*maxWidth/w
	if nh > maxHeight {
		nw, nh = w*maxHeight/h, maxHeight
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			sx, sy := src.Rect.Min.X+x*w/nw, src.Rect.Min.Y+y*h/nh
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

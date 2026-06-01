package ragindex

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // register decoders
	"image/jpeg"
	_ "image/png"
)

// Pixel-path limits.
const (
	imageSendLimit = 4 << 20
	imageJPEGQual  = 85
)

// imageEdges are the long-edge sizes tried, in order, to fit the send limit.
var imageEdges = []int{1536, 1024, 768}

// prepareImage returns the bytes and MIME type to send to the provider: the
// original when it fits 4 MiB, otherwise a JPEG downscaled to a long edge of
// 1536, 1024 or 768 pixels, whichever is the first to fit.
func prepareImage(data []byte, mime string) ([]byte, string, error) {
	mime = baseMIME(mime)
	if !pixelMIMEs[mime] {
		return nil, "", errImageFormat
	}
	if len(data) <= imageSendLimit {
		return data, mime, nil
	}
	if mime == "image/webp" {
		// The standard library cannot decode WebP, so it cannot be shrunk.
		return nil, "", errImageTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errImageFormat
	}
	for _, edge := range imageEdges {
		out, err := encodeScaled(src, edge)
		if err != nil {
			return nil, "", errImageFormat
		}
		if len(out) <= imageSendLimit {
			return out, "image/jpeg", nil
		}
	}
	return nil, "", errImageTooLarge
}

// encodeScaled shrinks src so its long edge is at most edge pixels (never
// enlarging it) and encodes it as JPEG over a white background.
func encodeScaled(src image.Image, edge int) ([]byte, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if long := max(w, h); long > edge {
		nw = max(1, w*edge/long)
		nh = max(1, h*edge/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for y := 0; y < nh; y++ {
		sy0, sy1 := y*h/nh, max(y*h/nh+1, (y+1)*h/nh)
		for x := 0; x < nw; x++ {
			sx0, sx1 := x*w/nw, max(x*w/nw+1, (x+1)*w/nw)
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			if n == 0 {
				continue
			}
			// Composite the averaged (premultiplied) colour over white.
			r, g, bl, a = r/n, g/n, bl/n, a/n
			inv := 0xffff - a
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((r + inv) >> 8), G: uint8((g + inv) >> 8), B: uint8((bl + inv) >> 8), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: imageJPEGQual}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

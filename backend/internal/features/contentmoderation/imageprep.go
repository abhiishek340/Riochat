package contentmoderation

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
)

const (
	maxImageBytes     = 20 << 20
	defaultImageMaxPx = 1024
)

// Reasons an image cannot be prepared.
var (
	errImageTooLarge    = errors.New("image is larger than 20 MiB")
	errImageUnsupported = errors.New("unsupported image format")
)

// sniffMIME detects the media type of data.
func sniffMIME(data []byte) string {
	m := http.DetectContentType(data)
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}

// prepareImage returns the bytes to send to the moderation service: the input
// itself when it already fits maxDim (or cannot be re-encoded), otherwise a
// PNG scaled so that its longest side is maxDim.
func prepareImage(data []byte, maxDim int) (out []byte, mime string, err error) {
	if len(data) == 0 {
		return nil, "", ErrFileEmpty
	}
	if len(data) > maxImageBytes {
		return nil, "", errImageTooLarge
	}
	if maxDim <= 0 {
		maxDim = defaultImageMaxPx
	}
	mime = sniffMIME(data)
	switch mime {
	case "image/webp":
		// The standard library cannot decode WebP; the service accepts it as is.
		return data, mime, nil
	case "image/png", "image/jpeg", "image/gif":
	default:
		return nil, "", errImageUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", errImageUnsupported
	}
	if cfg.Width <= maxDim && cfg.Height <= maxDim {
		return data, mime, nil
	}
	src, err := decodeImage(data, mime)
	if err != nil {
		return nil, "", errImageUnsupported
	}
	dst := scaleDown(src, maxDim)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

func decodeImage(data []byte, mime string) (image.Image, error) {
	r := bytes.NewReader(data)
	switch mime {
	case "image/png":
		return png.Decode(r)
	case "image/jpeg":
		return jpeg.Decode(r)
	default:
		return gif.Decode(r)
	}
}

// scaleDown shrinks src with area averaging so that its longest side is limit.
func scaleDown(src image.Image, limit int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw >= sh {
		dw = limit
		dh = sh * limit / sw
	} else {
		dh = limit
		dw = sw * limit / sh
	}
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0 := b.Min.Y + y*sh/dh
		y1 := b.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := b.Min.X + x*sw/dw
			x1 := b.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, pa := src.At(xx, yy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					bl += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			if n == 0 {
				continue
			}
			c := color.RGBA64{R: uint16(r / n), G: uint16(g / n), B: uint16(bl / n), A: uint16(a / n)}
			dst.Set(x, y, c)
		}
	}
	return dst
}

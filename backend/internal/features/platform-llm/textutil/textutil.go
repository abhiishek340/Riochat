// Package textutil holds small text, token, signature and image helpers shared
// by embedding, extraction and prompt budgeting code.
package textutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"path"
	"strings"
	"unicode/utf8"
)

func isCJK(r rune) bool {
	return r >= 0x2E80 && r <= 0x9FFF || r >= 0xAC00 && r <= 0xD7AF || r >= 0xF900 && r <= 0xFAFF || r >= 0x20000 && r <= 0x2A6DF
}

// EstimateTokens estimates the token count of s: CJK characters count 1.5 per
// token, every other character 4 per token, each group rounded up.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return (2*cjk+2)/3 + (other+3)/4
}

// Chunk splits text for embedding. Sizes are estimated tokens at two
// characters per token and are measured in Unicode characters. Non-positive
// size means 512; negative overlap means 64; an overlap that is not smaller
// than the size becomes a quarter of it.
func Chunk(text string, sizeTokens, overlapTokens int) []string {
	if sizeTokens <= 0 {
		sizeTokens = 512
	}
	if overlapTokens < 0 {
		overlapTokens = 64
	}
	if overlapTokens >= sizeTokens {
		overlapTokens = sizeTokens / 4
	}
	size, overlap := sizeTokens*2, overlapTokens*2
	runes := []rune(text)
	n := len(runes)
	if n <= size {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{text}
	}
	var out []string
	start := 0
	for start < n {
		end := start + size
		if end >= n {
			end = n
		} else {
			end = chooseBreak(runes, start, end)
		}
		if c := strings.TrimSpace(string(runes[start:end])); c != "" {
			out = append(out, c)
		}
		if end >= n {
			break
		}
		next := end - overlap
		if next <= start {
			next = start + 1
		}
		start = next
	}
	return out
}

// chooseBreak picks the end of a chunk window: after the last paragraph break
// past the half-way point, else after the last line break there, else the hard
// limit.
func chooseBreak(r []rune, start, limit int) int {
	half := start + (limit-start)/2
	for i := limit - 1; i > half; i-- {
		if r[i] == '\n' && r[i-1] == '\n' {
			return i + 1
		}
	}
	for i := limit - 1; i > half; i-- {
		if r[i] == '\n' {
			return i + 1
		}
	}
	return limit
}

// ModelSignature identifies an embedding model and width.
func ModelSignature(model string, dims int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d", model, dims)))
	return fmt.Sprintf("%s@%d", hex.EncodeToString(sum[:4]), dims)
}

// SpaceSignature additionally pins the provider endpoint, so switching model,
// width or provider never mixes incompatible vectors.
func SpaceSignature(model string, dims int, endpoint string) string {
	ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d@%s", model, dims, ep)))
	return fmt.Sprintf("%s@%d", hex.EncodeToString(sum[:8]), dims)
}

// ---- file types -----------------------------------------------------------

var textExts = map[string]bool{
	"txt": true, "md": true, "markdown": true, "csv": true, "tsv": true, "json": true, "xml": true, "html": true, "htm": true,
	"css": true, "js": true, "mjs": true, "ts": true, "tsx": true, "jsx": true, "py": true, "go": true, "rs": true, "java": true,
	"c": true, "h": true, "cpp": true, "hpp": true, "cc": true, "cs": true, "rb": true, "php": true, "swift": true, "kt": true,
	"sh": true, "bash": true, "yaml": true, "yml": true, "toml": true, "ini": true, "conf": true, "sql": true, "log": true,
}

// IsTextMIME reports MIME types that hold text.
func IsTextMIME(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.Index(m, ";"); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch {
	case strings.HasPrefix(m, "text/"):
		return true
	case m == "application/json", m == "application/xml", m == "application/javascript", m == "application/typescript",
		m == "application/x-yaml", m == "application/yaml", m == "application/toml", m == "application/x-sh":
		return true
	case strings.HasSuffix(m, "+json"), strings.HasSuffix(m, "+xml"):
		return true
	}
	return false
}

// IsTextFile reports text by extension (with or without the dot) or MIME.
func IsTextFile(name, mime string) bool {
	if IsTextMIME(mime) {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if ext == "" && !strings.Contains(name, ".") {
		ext = strings.ToLower(strings.TrimPrefix(name, "."))
	}
	return textExts[ext]
}

// ImageExt returns the file extension for an image MIME type.
func ImageExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".png"
}

// ---- opaque payloads ------------------------------------------------------

func isBase64Data(s string) bool {
	if !strings.HasPrefix(s, "data:") {
		return false
	}
	i := strings.Index(s, ";base64,")
	return i > 0 && i < 200
}

// IsOpaque reports a string that is a base64 data URL, or a long run (at least
// 1024 characters) without whitespace, braces, brackets, commas or colons that
// is more than 95 percent base64/URL-safe characters.
func IsOpaque(s string) bool {
	if isBase64Data(s) {
		return true
	}
	if utf8.RuneCountInString(s) < 1024 {
		return false
	}
	good := 0
	total := 0
	for _, r := range s {
		total++
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '{' || r == '}' || r == '[' || r == ']' || r == ',' || r == ':':
			return false
		case r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_':
			good++
		}
	}
	return float64(good)/float64(total) > 0.95
}

// OpaquePlaceholder is the replacement text for an opaque payload.
func OpaquePlaceholder(n int) string {
	return fmt.Sprintf("[Opaque tool payload omitted from model context: %d characters]", n)
}

// SanitizeOpaque replaces opaque strings in a tool result before it enters
// model context or the UI. JSON documents are walked; other text is judged as
// a whole.
func SanitizeOpaque(s string) string {
	t := strings.TrimSpace(s)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[' || t[0] == '"') && json.Valid([]byte(t)) {
		var v any
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		if dec.Decode(&v) == nil {
			changed := false
			v = walkOpaque(v, &changed)
			if !changed {
				return s
			}
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if enc.Encode(v) == nil {
				return strings.TrimRight(buf.String(), "\n")
			}
		}
		return s
	}
	if IsOpaque(s) {
		return OpaquePlaceholder(utf8.RuneCountInString(s))
	}
	return s
}

func walkOpaque(v any, changed *bool) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = walkOpaque(e, changed)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = walkOpaque(e, changed)
		}
		return x
	case string:
		if IsOpaque(x) {
			*changed = true
			return OpaquePlaceholder(utf8.RuneCountInString(x))
		}
	}
	return v
}

// ---- images ---------------------------------------------------------------

// NormalizeImageMIME lower-cases and trims; unknown values resolve to
// image/jpeg.
func NormalizeImageMIME(m string) string {
	switch n := strings.ToLower(strings.TrimSpace(m)); n {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
		return n
	}
	return "image/jpeg"
}

// ResizeIfNeeded scales an image down so its longer side equals maxDim
// (non-positive disables), using nearest-neighbor sampling with a 1x1 floor.
// PNG stays PNG; everything else becomes JPEG at quality 85. The returned MIME
// always matches the returned bytes. Decode or encode failures return the
// original bytes and MIME unchanged.
func ResizeIfNeeded(data []byte, mime string, maxDim int) ([]byte, string) {
	if maxDim <= 0 || len(data) == 0 {
		return data, mime
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	if long <= maxDim {
		return data, mime
	}
	nw, nh := w*maxDim/long, h*maxDim/long
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if format == "png" {
		if png.Encode(&buf, dst) != nil {
			return data, mime
		}
		return buf.Bytes(), "image/png"
	}
	flat := image.NewRGBA(dst.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), dst, image.Point{}, draw.Over)
	if jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 85}) != nil {
		return data, mime
	}
	return buf.Bytes(), "image/jpeg"
}

var _ = gif.Decode // register the GIF decoder

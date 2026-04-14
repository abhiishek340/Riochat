package textutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":                         0,
		"abcd":                     1,
		"abcde":                    2,
		"\u4f60\u597d":             2, // ceil(2*2/3)
		"\u4f60\u597d\u4e16":       2, // 3 CJK → 2
		"\u4f60\u597d\u4e16\u754c": 3, // 4 CJK → ceil(8/3)=3
		"hello \u4e16\u754c":       4, // 6 other → 2, 2 CJK → 2
		strings.Repeat("a", 4000):  1000,
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("%q → %d want %d", in, got, want)
		}
	}
	// Plane-2 CJK and Hangul count as CJK.
	if EstimateTokens("\U00020000\U00020001\U00020002") != 2 || EstimateTokens("\ud55c\uad6d\uc5b4") != 2 {
		t.Fatal("extended CJK ranges")
	}
}

func TestChunkDefaultsAndSingle(t *testing.T) {
	if Chunk("   \n ", 100, 10) != nil {
		t.Fatal("blank text yields nothing")
	}
	got := Chunk("short text", 100, 10)
	if len(got) != 1 || got[0] != "short text" {
		t.Fatalf("%v", got)
	}
	// Exactly one chunk long (size 5 tokens = 10 characters).
	if got := Chunk("0123456789", 5, 1); len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if got := Chunk("01234567890", 5, 1); len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestChunkBreaksAndOverlap(t *testing.T) {
	// Window is 40 characters (20 tokens).
	para1 := strings.Repeat("a", 30)
	para2 := strings.Repeat("b", 30)
	text := para1 + "\n\n" + para2
	got := Chunk(text, 20, 2)
	if len(got) < 2 || got[0] != para1 {
		t.Fatalf("a chunk ends at the paragraph break past the half-way point: %q", got)
	}
	// Line break fallback.
	text = strings.Repeat("x", 30) + "\n" + strings.Repeat("y", 30)
	got = Chunk(text, 20, 2)
	if got[0] != strings.Repeat("x", 30) {
		t.Fatalf("line break: %q", got)
	}
	// Hard limit and overlap: 100 chars of text, window 40, overlap 10 chars.
	text = strings.Repeat("z", 100)
	got = Chunk(text, 20, 5)
	if len(got[0]) != 40 {
		t.Fatalf("hard limit %d", len(got[0]))
	}
	total := 0
	for _, c := range got {
		total += len(c)
	}
	if total <= 100 {
		t.Fatal("overlapping chunks cover more than the text")
	}
	// A break before the half-way point is ignored.
	text = "ab\n\n" + strings.Repeat("c", 80)
	got = Chunk(text, 20, 0)
	if len(got[0]) != 40 {
		t.Fatalf("early breaks are ignored: %d", len(got[0]))
	}
}

func TestChunkParameterNormalization(t *testing.T) {
	text := strings.Repeat("w", 5000)
	def := Chunk(text, 0, -1) // 512 tokens → 1024 chars, overlap 64 tokens
	if len(def[0]) != 1024 {
		t.Fatalf("%d", len(def[0]))
	}
	// Overlap not smaller than size becomes a quarter of the size, so progress is made.
	got := Chunk(text, 10, 10)
	if len(got) < 2 || len(got[0]) != 20 {
		t.Fatalf("%d chunks", len(got))
	}
	// Always advances, even with absurd overlap.
	if n := len(Chunk(strings.Repeat("q", 60), 1, 1)); n == 0 || n > 60 {
		t.Fatalf("%d", n)
	}
	// Characters, not bytes.
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	cjk := strings.Repeat("\u5b57", 50)
	got = Chunk(cjk, 10, 0)
	if len([]rune(got[0])) != 20 {
		t.Fatalf("%d", len([]rune(got[0])))
	}
}

func TestSignatures(t *testing.T) {
	sum := sha256.Sum256([]byte("text-embedding-3-small@1536"))
	want := hex.EncodeToString(sum[:4]) + "@1536"
	if got := ModelSignature("text-embedding-3-small", 1536); got != want || len(want) != len("a1b2c3d4@1536") {
		t.Fatalf("%s want %s", got, want)
	}
	s1 := SpaceSignature("m", 8, "https://api.example.com/v1/")
	s2 := SpaceSignature("m", 8, "https://api.example.com/v1")
	s3 := SpaceSignature("m", 8, "https://other.example.com/v1")
	if s1 != s2 || s1 == s3 || !strings.HasSuffix(s1, "@8") || len(strings.Split(s1, "@")[0]) != 16 {
		t.Fatalf("%s %s %s", s1, s2, s3)
	}
	if ModelSignature("m", 8) == ModelSignature("m", 9) {
		t.Fatal("dimensions are part of the signature")
	}
}

func TestFileTypeHelpers(t *testing.T) {
	for _, m := range []string{"text/plain", "text/x-go", "application/json", "application/ld+json", "application/xml", "application/x-yaml", "application/javascript", "TEXT/HTML; charset=utf-8"} {
		if !IsTextMIME(m) {
			t.Errorf("%s is text", m)
		}
	}
	if IsTextMIME("image/png") || IsTextMIME("application/pdf") {
		t.Fatal("binary mime")
	}
	if !IsTextFile("main.go", "") || !IsTextFile("README.MD", "application/octet-stream") || !IsTextFile("x.bin", "text/plain") || IsTextFile("a.png", "image/png") {
		t.Fatal("IsTextFile")
	}
	if ImageExt("image/jpeg") != ".jpg" || ImageExt("image/webp") != ".webp" || ImageExt("image/gif") != ".gif" || ImageExt("image/x") != ".png" {
		t.Fatal("ImageExt")
	}
}

func TestOpaqueSanitizer(t *testing.T) {
	b64 := strings.Repeat("QUJD", 400) // 1600 chars
	if got := SanitizeOpaque(b64); got != "[Opaque tool payload omitted from model context: 1600 characters]" {
		t.Fatal(got)
	}
	if got := SanitizeOpaque("data:image/png;base64,AAAA"); !strings.Contains(got, "26 characters") {
		t.Fatal(got)
	}
	if SanitizeOpaque(strings.Repeat("QUJD", 100)) != strings.Repeat("QUJD", 100) {
		t.Fatal("short strings are not opaque")
	}
	withSpace := strings.Repeat("QUJD ", 400)
	if SanitizeOpaque(withSpace) != withSpace {
		t.Fatal("whitespace disqualifies")
	}
	if IsOpaque(strings.Repeat("{", 1500)) || IsOpaque(strings.Repeat("a,", 800)) {
		t.Fatal("braces and commas disqualify")
	}
	if IsOpaque(strings.Repeat("é", 1500)) {
		t.Fatal("under 95 percent base64 characters")
	}
	js := `{"ok":true,"image":"` + b64 + `","list":["fine","data:image/gif;base64,R0lGOD"],"n":1.50}`
	out := SanitizeOpaque(js)
	if strings.Contains(out, "QUJD") || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "1600 characters") || !strings.Contains(out, "fine") || !strings.Contains(out, "1.50") {
		t.Fatal(out)
	}
	clean := `{"a": 1, "b": "text"}`
	if SanitizeOpaque(clean) != clean {
		t.Fatal("untouched JSON is returned verbatim")
	}
	if SanitizeOpaque("plain words") != "plain words" {
		t.Fatal("plain text")
	}
}

func makePNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 7, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestResizeIfNeeded(t *testing.T) {
	src := makePNG(200, 100)
	out, mime := ResizeIfNeeded(src, "image/png", 50)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "png" || mime != "image/png" || cfg.Width != 50 || cfg.Height != 25 {
		t.Fatalf("%v %s %s %+v", err, format, mime, cfg)
	}
	same, m2 := ResizeIfNeeded(src, "image/png", 200)
	if !bytes.Equal(same, src) || m2 != "image/png" {
		t.Fatal("images within the limit are untouched")
	}
	if o, _ := ResizeIfNeeded(src, "image/png", 0); !bytes.Equal(o, src) {
		t.Fatal("non-positive disables")
	}
	// JPEG input stays JPEG; extreme aspect ratios keep a 1x1 floor.
	var jbuf bytes.Buffer
	jpeg.Encode(&jbuf, image.NewRGBA(image.Rect(0, 0, 1000, 4)), nil)
	out, mime = ResizeIfNeeded(jbuf.Bytes(), "image/jpeg", 10)
	cfg, format, _ = image.DecodeConfig(bytes.NewReader(out))
	if format != "jpeg" || mime != "image/jpeg" || cfg.Width != 10 || cfg.Height != 1 {
		t.Fatalf("%s %s %+v", format, mime, cfg)
	}
	// Undecodable data comes back unchanged.
	bad, bm := ResizeIfNeeded([]byte("not an image"), "image/webp", 10)
	if string(bad) != "not an image" || bm != "image/webp" {
		t.Fatal("decode failures return the original")
	}
	if NormalizeImageMIME(" IMAGE/PNG ") != "image/png" || NormalizeImageMIME("image/bmp") != "image/jpeg" || NormalizeImageMIME("") != "image/jpeg" {
		t.Fatal("mime normalization")
	}
}

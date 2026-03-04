package llm

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestSanitizeLargeBodyOmitted(t *testing.T) {
	body := []byte(`{"x":"` + strings.Repeat("a", 300<<10) + `"}`)
	s := SanitizeBody(body)
	if !s.Truncated || s.OriginalBytes != len(body) {
		t.Fatalf("%+v", s)
	}
	m := decodeMap(t, s.Text)
	d := m["_debug"].(map[string]any)
	if d["bodyOmitted"] != true || d["reason"] != "body_too_large" || int(d["originalBytes"].(float64)) != len(body) {
		t.Fatalf("%s", s.Text)
	}
}

func TestSanitizeB64JSONKeepsSurroundingJSON(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("png", 100)))
	body := `{"created":1,"data":[{"b64_json":"` + payload + `","revised_prompt":"a cat"}]}`
	s := SanitizeBody([]byte(body))
	want := "[binary omitted; mime=application/octet-stream; encoded_bytes=" + strconv.Itoa(len(payload)) + "]"
	if !strings.Contains(s.Text, want) || !strings.Contains(s.Text, `"revised_prompt":"a cat"`) || !strings.Contains(s.Text, `"created":1`) {
		t.Fatalf("%s", s.Text)
	}
	if s.Redacted != 1 || strings.Contains(s.Text, payload) {
		t.Fatalf("redacted=%d", s.Redacted)
	}
}

func TestSanitizeDataURLs(t *testing.T) {
	body := `{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAABBBB"}}]}]}`
	s := SanitizeBody([]byte(body))
	if !strings.Contains(s.Text, "[binary omitted; mime=image/png; encoded_bytes=8]") || s.Redacted != 1 {
		t.Fatalf("%s", s.Text)
	}
}

func TestSanitizeDataKeyRules(t *testing.T) {
	long := strings.Repeat("QUJD", 80) // 320 chars, valid base64 alphabet
	cases := []struct {
		name, body string
		redacted   bool
	}{
		{"inlineData parent", `{"inlineData":{"mimeType":"image/png","data":"short"}}`, true},
		{"source parent", `{"source":{"type":"base64","media_type":"image/png","data":"xyz"}}`, true},
		{"declares base64", `{"x":{"type":"base64","data":"zzz"}}`, true},
		{"image mime", `{"x":{"mime_type":"image/webp","data":"zzz"}}`, true},
		{"looks like base64", `{"x":{"data":"` + long + `"}}`, true},
		{"plain data key", `{"x":{"data":"hello"}}`, false},
		{"short data under unknown parent", `{"x":{"data":"QUJD"}}`, false},
		{"base64 key", `{"image_base64":"abc"}`, true},
		{"file_data", `{"file_data":"abc"}`, true},
	}
	for _, c := range cases {
		s := SanitizeBody([]byte(c.body))
		if (s.Redacted > 0) != c.redacted {
			t.Errorf("%s: redacted=%d text=%s", c.name, s.Redacted, s.Text)
		}
	}
	img := SanitizeBody([]byte(`{"imageBase64":"abcdef"}`))
	if !strings.Contains(img.Text, "mime=image/*") {
		t.Fatalf("%s", img.Text)
	}
	au := SanitizeBody([]byte(`{"audio_base64":"abcdef"}`))
	if !strings.Contains(au.Text, "mime=audio/*") {
		t.Fatalf("%s", au.Text)
	}
}

func TestSanitizeNestedJSONString(t *testing.T) {
	inner := `{"b64_json":"` + strings.Repeat("A", 40) + `"}`
	body := `{"output":` + jsonQuote(inner) + `}`
	s := SanitizeBody([]byte(body))
	if s.Redacted != 1 || strings.Contains(s.Text, strings.Repeat("A", 40)) {
		t.Fatalf("%s", s.Text)
	}
}

func jsonQuote(s string) string {
	b, _ := encodeJSON(s)
	return string(b)
}

func TestSanitizeBinaryAndSSEAndText(t *testing.T) {
	s := SanitizeBody([]byte{0xff, 0xfe, 0x00, 0x80})
	if !s.Truncated || !strings.Contains(s.Text, `"binary_body"`) {
		t.Fatalf("%s", s.Text)
	}
	sse := "event: x\ndata: {\"a\":\"data:image/jpeg;base64,QUJD\"}\n\ndata: [DONE]\n\n"
	o := SanitizeBody([]byte(sse))
	if !strings.Contains(o.Text, "[binary omitted; mime=image/jpeg; encoded_bytes=4]") || !strings.Contains(o.Text, "data: [DONE]") || o.Redacted != 1 {
		t.Fatalf("%q", o.Text)
	}
	plain := SanitizeBody([]byte(`see "data:image/png;base64,AAAA" here`))
	if plain.Text != `see "[binary omitted; mime=image/png; encoded_bytes=4]" here` {
		t.Fatalf("%q", plain.Text)
	}
	if got := SanitizeBody([]byte("hello")).Text; got != "hello" {
		t.Fatal(got)
	}
}

func TestSanitizeReencodedOverflow(t *testing.T) {
	// 100 KiB of small objects plus tiny placeholders stays small; build a
	// document that is under the limit raw but over after nothing shrinks.
	body := `{"a":"` + strings.Repeat("é", 70<<10) + `"}` // 140 KiB of UTF-8 > limit
	s := SanitizeBody([]byte(body))
	if !s.Truncated {
		t.Fatal("oversize documents are summarized")
	}
}

func TestDebugHeadersRedaction(t *testing.T) {
	h := http.Header{}
	for k, v := range map[string]string{"Authorization": "Bearer x", "Proxy-Authorization": "y", "Cookie": "c", "Set-Cookie": "s",
		"X-Api-Key": "k", "Api-Key": "k", "X-Auth-Token": "t", "X-Secret-Thing": "s", "X-Goog-Api-Key": "k", "Accept": "application/json", "X-Trace": "ok"} {
		h.Set(k, v)
	}
	got := debugHeaders(h)
	for k, v := range got {
		keep := k == "accept" || k == "x-trace"
		if keep && v == "[redacted]" || !keep && v != "[redacted]" {
			t.Errorf("%s=%s", k, v)
		}
	}
}

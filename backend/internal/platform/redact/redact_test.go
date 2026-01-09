package redact

import (
	"strings"
	"testing"
)

func TestQueryKeepsOrderAndNames(t *testing.T) {
	cases := map[string]string{
		"token=abc&page=2":                 "token=REDACTED&page=2",
		"page=2&Access_Token=x&flag":       "page=2&Access_Token=REDACTED&flag",
		"X-Amz-Signature=zzz&a=b":          "X-Amz-Signature=REDACTED&a=b",
		"%74oken=abc":                      "%74oken=REDACTED",
		"q=hello+world&state=s1&code=c2":   "q=hello+world&state=REDACTED&code=REDACTED",
		"":                                 "",
		"nokeyvalue":                       "nokeyvalue",
		"keyed=1&key=2&apikey=3&api_key=4": "keyed=1&key=REDACTED&apikey=REDACTED&api_key=REDACTED",
	}
	for in, want := range cases {
		if got := Query(in); got != want {
			t.Errorf("Query(%q) = %q, want %q", in, got, want)
		}
	}
	if got := PathWithQuery("/api/v1/x", "token=abc&page=2"); got != "/api/v1/x?token=REDACTED&page=2" {
		t.Fatalf("PathWithQuery: %q", got)
	}
	if PathWithQuery("/a", "") != "/a" {
		t.Fatal("empty query must not add '?'")
	}
}

func TestTextRedaction(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Authorization: Bearer abcdefghijklmnopqrstuvwxyz failed", "Authorization: Bearer REDACTED failed"},
		{"Bearer short", "Bearer short"},
		{"call https://user:pw@host.example/p?token=abc&x=1 failed", "call https://REDACTED@host.example/p?token=REDACTED&x=1 failed"},
		{"GET https://h/p?code=1&sig=2.", "GET https://h/p?code=REDACTED&sig=REDACTED."},
		{"api_key=sk-123 and client_secret=zzz, password=p@ss", "api_key=REDACTED and client_secret=REDACTED, password=REDACTED"},
		{"api_key: sk-123 stays", "api_key: sk-123 stays"},
		{"plain text", "plain text"},
	}
	for _, c := range cases {
		if got := Text(c.in); got != c.want {
			t.Errorf("Text(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestTruncateOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("a", 8192)
	got := Cause(long)
	if !strings.HasPrefix(got, strings.Repeat("a", 4096)+"...(truncated, 8192 bytes total)") {
		t.Fatalf("unexpected truncation: %q", got[len(got)-60:])
	}
	multi := strings.Repeat("\u4e16", 2000) // 3 bytes each = 6000 bytes
	out := Truncate(multi, 4096)
	body := out[:strings.Index(out, "...(")]
	if len(body) > 4096 || len(body)%3 != 0 {
		t.Fatalf("cut must land on a character boundary, len=%d", len(body))
	}
	if Truncate("short", 4096) != "short" {
		t.Fatal("short text unchanged")
	}
}

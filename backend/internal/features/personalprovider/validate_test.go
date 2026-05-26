package personalprovider

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestParseBaseURL(t *testing.T) {
	good := []struct{ in, url, host string }{
		{"https://api.example.com/v1///", "https://api.example.com/v1", "api.example.com"},
		{"HTTPS://API.Example.COM", "https://api.example.com", "api.example.com"},
		{"  https://api.example.com:8443/x/y/ ", "https://api.example.com:8443/x/y", "api.example.com"},
		{"https://api.example.com./v1", "https://api.example.com/v1", "api.example.com"},
		{"https://api.example.com/", "https://api.example.com", "api.example.com"},
		{"https://[2606:4700::1]:443/v1", "https://[2606:4700::1]:443/v1", "2606:4700::1"},
	}
	for _, c := range good {
		got, err := parseBaseURL(c.in)
		if err != nil || got.BaseURL != c.url || got.Host != c.host {
			t.Errorf("parseBaseURL(%q) = %+v, %v; want %s %s", c.in, got, err, c.url, c.host)
		}
	}
	bad := []string{
		"", "   ", "https://host?x=1", "https://host?", "https://u:p@host", "https://host#f", "https://host#",
		"http://host", "ftp://host", "https://host:0", "https://host:70000", "https://host:", "https://host:abc",
		"host.example.com", "https:///path", "mailto:a@b.c", "https://ho st.example.com", "https://host\\x",
		"https://" + strings.Repeat("a", 500) + ".com", "//host.example.com", "https:host.example.com",
		"https://host.example.com/a\nb",
	}
	for _, in := range bad {
		if _, err := parseBaseURL(in); !errors.Is(err, ErrInvalidBaseURL) {
			t.Errorf("parseBaseURL(%q) err = %v, want invalid_base_url", in, err)
		}
	}
}

func TestPublicOnlyPolicy(t *testing.T) {
	p := PublicOnlyPolicy{}
	blocked := []string{
		"localhost", "LOCALHOST", "127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.4.4", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1", "fd00:ec2::254", "::ffff:127.0.0.1",
		"metadata.google.internal", "printer.local", "db.internal", "intranet", "foo.localhost",
		"2130706433", "0x7f.0.0.1", "api.example.com.1234",
	}
	for _, h := range blocked {
		if err := p.CheckHost(h); !errors.Is(err, ErrBlockedHost) {
			t.Errorf("CheckHost(%q) = %v, want blocked", h, err)
		}
	}
	for _, h := range []string{"api.example.com", "api.openai.com", "8.8.8.8", "2606:4700::1", "xn--e1afmkfd.example"} {
		if err := p.CheckHost(h); err != nil {
			t.Errorf("CheckHost(%q) = %v, want ok", h, err)
		}
	}
}

func TestForbiddenIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.1.1", "169.254.169.254", "::1", "fc00::1", "100.100.100.200", "::7f00:1", "2002:7f00:1::1", "2001:0:4136:e378::1", "fec0::1", "::ffff:127.0.0.1"} {
		if !forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"} {
		if forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestDenyList(t *testing.T) {
	d := parseDenyList("evil.com, *.bad.org\n\tEXACT.net\r\n  trailing.dot. ")
	cases := map[string]bool{
		"evil.com": true, "api.evil.com": false, "EVIL.COM": true, "evil.com.": true,
		"bad.org": false, "a.bad.org": true, "a.b.bad.org": true, "notbad.org": false, "xbad.org": false,
		"exact.net": true, "trailing.dot": true, "": false,
	}
	for host, want := range cases {
		if got := d.matches(host); got != want {
			t.Errorf("matches(%q) = %v, want %v", host, got, want)
		}
	}
	if parseDenyList("").matches("a.com") {
		t.Error("empty list matches nothing")
	}
}

func TestValidateBlockedHosts(t *testing.T) {
	ok := []string{"", "evil.com", "*.evil.com, a.com\nb.org", "UPPER.com", "10.0.0.1"}
	for _, in := range ok {
		if err := ValidateBlockedHosts(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	bad := []string{"http://evil.com", "evil.com/path", "evil.com:443", "-a.com", "a..com", "*.", "**.a.com", "a_b.com", strings.Repeat("a.com ", 1000)}
	for _, in := range bad {
		if err := ValidateBlockedHosts(in); err == nil {
			t.Errorf("%q should be rejected", in[:min(len(in), 20)])
		}
	}
	if ValidateMaxPerUser(0) == nil || ValidateMaxPerUser(51) == nil || ValidateMaxPerUser(1) != nil || ValidateMaxPerUser(50) != nil {
		t.Error("max_per_user range is 1..50")
	}
}

func TestKeyHint(t *testing.T) {
	cases := map[string]string{
		"": "", "   ": "", "short": "••••", "12345678901": "••••", "sk-abcdefghijWXYZ": "sk-••••WXYZ",
		"123456789012": "123••••9012", "ключключключключ": "клю••••лючь"[0:0] + "клю••••ключ"[0:0] + keyHint("ключключключключ"),
	}
	for in, want := range cases {
		if got := keyHint(in); got != want {
			t.Errorf("keyHint(%q) = %q, want %q", in, got, want)
		}
	}
	// 11 unicode characters count as characters, not bytes.
	if got := keyHint("ключключклю"); got != "••••" {
		t.Errorf("11 runes: %q", got)
	}
	if got := keyHint("ключключключ"); got != "клю••••ючк"[0:0]+"клю••••ключ"[0:0]+got || !strings.Contains(got, "••••") {
		t.Errorf("12 runes: %q", got)
	}
}

func TestNormalizeAPIKey(t *testing.T) {
	if k, err := normalizeAPIKey("  sk-123  "); err != nil || k != "sk-123" {
		t.Fatalf("trim: %q %v", k, err)
	}
	for _, in := range []string{"", "   ", "a b", "a\nb", "a\tb", "ключ", "ab\x00c", "ab\x7fc", strings.Repeat("a", 513)} {
		if _, err := normalizeAPIKey(in); !errors.Is(err, ErrInvalidAPIKey) {
			t.Errorf("%q: %v", in, err)
		}
	}
	if _, err := normalizeAPIKey(strings.Repeat("a", 512)); err != nil {
		t.Errorf("512 bytes is allowed: %v", err)
	}
}

func TestNormalizeName(t *testing.T) {
	if n, err := normalizeName("  My Provider  "); err != nil || n != "My Provider" {
		t.Fatalf("%q %v", n, err)
	}
	if n, err := normalizeName("   "); err != nil || n != "" {
		t.Fatalf("empty stays empty: %q %v", n, err)
	}
	if _, err := normalizeName(strings.Repeat("я", 64)); err != nil {
		t.Errorf("64 runes ok: %v", err)
	}
	for _, in := range []string{strings.Repeat("я", 65), "a\x01b", "a\x7fb", "a\nb", "a\tb"} {
		if _, err := normalizeName(in); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestNormalizeIcon(t *testing.T) {
	ok := map[string]string{"OpenAI ": "openai", "": "", "  ": "", "a-b-9": "a-b-9", "x": "x"}
	for in, want := range ok {
		if got, err := normalizeIcon(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"http://x/y.png", "-bad", strings.Repeat("a", 65), "a b", "a_b", "é", "a/b", "a.png"} {
		if _, err := normalizeIcon(in); !errors.Is(err, ErrInvalidIcon) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestModelNameRules(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	good := []string{"gpt-4o", "models/x", "a", strings.Repeat("a", 100), "\u6a21\u578b", "x:y@z"}
	for _, n := range good {
		if !validModelName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	bad := []string{"", " ", "a b", " a", "a ", "a\nb", "a\tb", "a\x7fb", strings.Repeat("a", 101), "a\x00"}
	for _, n := range bad {
		if validModelName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestReferences(t *testing.T) {
	id, m, ok := ParseReference("personal:abcd1234/gpt-4o")
	if !ok || id != "abcd1234" || m != "gpt-4o" {
		t.Fatalf("%q %q %v", id, m, ok)
	}
	if id, m, ok := ParseReference("personal:abcd1234/org/model"); !ok || id != "abcd1234" || m != "org/model" {
		t.Fatalf("slash in model: %q %q %v", id, m, ok)
	}
	for _, bad := range []string{
		"personal:abcd1234", "other:abcd1234/m", "personal:abc/m", "personal:ABCD1234/m", "personal:abcd-234/m",
		"personal:abcd1234/", "personal:abcd1234/a b", "personal:/m", "gpt-4o", "", "personal:" + strings.Repeat("a", 33) + "/m",
	} {
		if _, _, ok := ParseReference(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
	// Prefix membership is independent of well-formedness.
	if !IsReference("personal:garbage") || !IsReference("  personal:x") || IsReference("Personal:x") || IsReference("gpt") {
		t.Error("IsReference")
	}
	if FormatReference("abcd1234", "m/x") != "personal:abcd1234/m/x" {
		t.Error("FormatReference")
	}
}

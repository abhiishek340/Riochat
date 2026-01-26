package user

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	ok := map[string]string{
		"  Bob_7 ":                  "bob_7",
		"abc":                       "abc",
		"a-b":                       "a-b",
		strings.Repeat("a", 16):     strings.Repeat("a", 16),
		"Ab1_2-3":                   "ab1_2-3",
		strings.Repeat("A", 3) + "": "aaa",
	}
	for in, want := range ok {
		got, err := NormalizeUsername(in)
		if err != nil || got != want {
			t.Errorf("NormalizeUsername(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "ab", strings.Repeat("a", 17), "admin", "ADMIN", "user", "users", "rio-chat", "riochat",
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		"user@example.com", "a.b.c", "-bob", "bob-", "_bob", "bob_", "bob.", "bo b", "bób", "\u540d\u524d\u540d\u524d",
		"api", "auth", "billing", "chat", "files", "help", "me ", "root", "settings", "support", "system",
	}
	for _, in := range bad {
		if _, err := NormalizeUsername(in); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("NormalizeUsername(%q) err = %v; want invalid username", in, err)
		}
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	if got, err := NormalizeDisplayName(" Sam Lee "); err != nil || got != "Sam Lee" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, in := range []string{"ab", "", "   ", strings.Repeat("x", 17)} {
		if _, err := NormalizeDisplayName(in); !errors.Is(err, ErrInvalidDisplayName) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
	// Length counts characters, not bytes.
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	for _, in := range []string{"\u540d\u524d\u540d", strings.Repeat("\u540d", 16)} {
		if _, err := NormalizeDisplayName(in); err != nil {
			t.Errorf("%q should be valid: %v", in, err)
		}
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if _, err := NormalizeDisplayName(strings.Repeat("\u540d", 17)); err == nil {
		t.Error("17 characters must be invalid")
	}
}

func TestGeneratedDisplayName(t *testing.T) {
	cases := map[string]string{
		"":                      "user",
		"   ":                   "user",
		"a":                     "a__",
		"ab":                    "ab_",
		"abc":                   "abc",
		strings.Repeat("z", 40): strings.Repeat("z", 16),
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		strings.Repeat("\u540d", 30): strings.Repeat("\u540d", 16),
	}
	for in, want := range cases {
		if got := GeneratedDisplayName(in); got != want {
			t.Errorf("GeneratedDisplayName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalizePassword(t *testing.T) {
	if got, err := NormalizePassword("  longenough99 "); err != nil || got != "longenough99" {
		t.Fatalf("got %q, %v", got, err)
	}
	bad := []string{"abc", "1234567", "12345678", "00000000", strings.Repeat("9", 100), strings.Repeat("a", 129), "    short   "}
	for _, in := range bad {
		if _, err := NormalizePassword(in); !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
	good := []string{"1234567a", "1234567!", strings.Repeat("a", 128), "pass word", "ééééééé8"}
	for _, in := range good {
		if _, err := NormalizePassword(in); err != nil {
			t.Errorf("%q should be valid: %v", in, err)
		}
	}
	if ErrInvalidPassword.Message != "password must be at least 8 characters and not digits only" {
		t.Errorf("message: %q", ErrInvalidPassword.Message)
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{"": "", "   ": "", "a@b.co": "a@b.co", " Bob@Example.COM ": "Bob@Example.COM"} {
		if got, err := NormalizeEmail(in); err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"Bob <a@b.co>", "not-an-email", "<a@b.co>", "a@", "@b.co", "a b@c.de", "a@b.co, c@d.ef"} {
		if _, err := NormalizeEmail(in); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	good := map[string]string{
		"":                      "",
		"  ":                    "",
		"+1 (555) 123-4567":     "+15551234567",
		"123 456":               "123456",
		"+123456":               "+123456",
		"(12)-34 56":            "123456",
		strings.Repeat("1", 20): strings.Repeat("1", 20),
	}
	for in, want := range good {
		if got, err := NormalizePhone(in); err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"+12-34", "12345", "abc", "12+3456", "++123456", "123456a", strings.Repeat("1", 21), "+" + strings.Repeat("1", 21), "12.3456"} {
		if _, err := NormalizePhone(in); !errors.Is(err, ErrInvalidPhone) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
}

func TestNormalizeLocale(t *testing.T) {
	good := map[string]string{
		"": DefaultLocale, "  ": DefaultLocale, "fr_fr": "fr-FR", "pt_BR": "pt-BR", "EN": "en",
		"en-us": "en-US", "fil": "fil", "FIL-ph": "fil-PH", " en ": "en",
	}
	for in, want := range good {
		if got, err := NormalizeLocale(in); err != nil || got != want {
			t.Errorf("NormalizeLocale(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"en-USA", "e", "en-US-x", "fr-Latn-FR", "en-", "-US", "e1", "en-1A", "abcd", "en_"} {
		if _, err := NormalizeLocale(in); !errors.Is(err, ErrInvalidLocale) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
}

func TestNormalizeTimezone(t *testing.T) {
	for in, want := range map[string]string{"": DefaultTimezone, " ": DefaultTimezone, "Asia/Shanghai": "Asia/Shanghai", " UTC ": "UTC", "Local": "Local", "Etc/UTC": "Etc/UTC", "America/New_York": "America/New_York"} {
		if got, err := NormalizeTimezone(in); err != nil || got != want {
			t.Errorf("NormalizeTimezone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"Mars/Base", "../etc/passwd", "utc+8x", "Not A Zone"} {
		if _, err := NormalizeTimezone(in); !errors.Is(err, ErrInvalidTimeZone) {
			t.Errorf("%q should be invalid, got %v", in, err)
		}
	}
	if ErrInvalidTimeZone.Message != "invalid time zone" {
		t.Errorf("message: %q", ErrInvalidTimeZone.Message)
	}
}

func TestAvatarRefs(t *testing.T) {
	valid := []string{
		"", "/a.png", "/avatars/default.png", "generated:github:x", "generated:github:octocat",
		"file:file_demo_9", "file:file_a-b_1", "https://example.com/a.png", "http://example.com/x",
		"HTTPS://example.com/a.png", "https://example.com:8443/a.png?x=1", "file:" + "file_" + strings.Repeat("a", 123),
	}
	for _, s := range valid {
		if !ValidAvatarRef(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	invalid := []string{
		"file:invalid", "file:", "file: file_x", "file:file_x ", "file:file_x/y", "file:file_" + strings.Repeat("a", 124),
		"javascript:alert(1)", "https:///a.png", "http://", "ftp://example.com/a.png", "example.com/a.png",
		"data:image/png;base64,AAAA", "//evil.example/a.png", "/a\\b", " /a.png", "https://", "https://:80/x",
		"relative/path.png", "FILE:file_x",
	}
	for _, s := range invalid {
		if ValidAvatarRef(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
	if ref := AvatarRefForUpload("file_demo_9"); ref != "file:file_demo_9" {
		t.Fatalf("ref = %q", ref)
	}
	if id, ok := UploadIDFromAvatarRef("file:file_demo_9"); !ok || id != "file_demo_9" {
		t.Fatalf("id = %q, %v", id, ok)
	}
	for _, s := range []string{"", "/a.png", "file:invalid", "https://x.y/z"} {
		if _, ok := UploadIDFromAvatarRef(s); ok {
			t.Errorf("%q must not yield an upload id", s)
		}
	}
}

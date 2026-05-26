package ragindex

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSplitShortAndBlank(t *testing.T) {
	text := "hello world"
	got := Split(text, 512, 64)
	if len(got) != 1 || got[0] != text {
		t.Fatalf("short text: %q", got)
	}
	if got := Split("  \n\t \n", 512, 64); len(got) != 0 {
		t.Fatalf("blank text must give no chunks, got %q", got)
	}
	if got := Split("", 512, 64); len(got) != 0 {
		t.Fatalf("empty text: %q", got)
	}
}

func TestSplitPrefersBlankLine(t *testing.T) {
	phrase := "\u77e5\u8bc6\u5e93\u68c0\u7d22\u6d4b\u8bd5\u5185\u5bb9\u3002" // ten CJK characters ending in an ideographic full stop
	block := strings.Repeat(phrase, 900)
	text := block + "\n\n" + block
	got := Split(text, 1024, 64) // window 2048 characters
	if len(got) < 2 {
		t.Fatalf("want at least two chunks, got %d", len(got))
	}
	for i, c := range got {
		if strings.TrimSpace(c) == "" {
			t.Fatalf("chunk %d is blank", i)
		}
	}
	cutAtBlank := false
	for _, c := range got {
		if strings.HasSuffix(c, "\n\n") {
			cutAtBlank = true
		}
	}
	if !cutAtBlank {
		t.Fatal("expected a chunk to end right after the blank line")
	}
	mid := "aaaa\n\n" + strings.Repeat("b", 30) // 36 chars
	parts := Split(mid, 10, 0)                  // window 20: blank line at 6 is not beyond half (10)
	if len(parts) < 2 || strings.HasSuffix(parts[0], "\n\n") {
		t.Fatalf("blank line before half the window must not be used: %q", parts)
	}
	late := strings.Repeat("a", 12) + "\n\n" + strings.Repeat("b", 30)
	parts = Split(late, 10, 0) // window 20: blank line ends at 14 > 10
	if parts[0] != strings.Repeat("a", 12)+"\n\n" {
		t.Fatalf("expected cut after the blank line, got %q", parts[0])
	}
}

func TestSplitPrefersSingleNewline(t *testing.T) {
	text := strings.Repeat("a", 14) + "\n" + strings.Repeat("b", 30)
	parts := Split(text, 10, 0)
	if parts[0] != strings.Repeat("a", 14)+"\n" {
		t.Fatalf("expected cut after the line break, got %q", parts[0])
	}
}

func TestSplitOverlapLargerThanWindow(t *testing.T) {
	text := strings.Repeat("lorem ipsum dolor sit amet ", 400)
	got := Split(text, 128, 512)
	window := 256
	max := len([]rune(text))/(window-window/4) + 3
	if len(got) == 0 || len(got) > max {
		t.Fatalf("chunk count %d outside (0, %d]", len(got), max)
	}
}

func TestSplitMixedTextHasNoBrokenCharacters(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	unit := "Hello \u4e16\u754c 😀 emoji\nline two \u4f60\u597d\n\n"
	text := strings.Repeat(unit, 300)
	for _, c := range Split(text, 64, 8) {
		if !utf8.ValidString(c) || strings.ContainsRune(c, unicode.ReplacementChar) {
			t.Fatalf("broken character in chunk %q", c)
		}
		if strings.TrimSpace(c) == "" {
			t.Fatal("blank chunk")
		}
	}
}

func TestSplitTinyChunkTerminates(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	text := strings.Repeat("\u5b57", 50)
	got := Split(text, 1, 100)
	if len(got) == 0 || len(got) > 50 {
		t.Fatalf("chunk count %d", len(got))
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"abcd":  1,
		"abcde": 2,
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		"\u5b57":             1,
		"\u5b57\u5b57\u5b57": 2,
		"\u5b57abcd":         2,
		"ab \u5b57\u5b57 cd": 4, // 2 cjk -> 2 tokens (ceil 4/3 = 2); 6 other -> 2
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

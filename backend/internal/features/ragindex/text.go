package ragindex

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// isCJK reports whether r counts as a wide (CJK) character for token
// estimation and keyword tokenisation.
func isCJK(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F, // CJK symbols and punctuation
		r >= 0xFF00 && r <= 0xFFEF: // full-width forms
		return true
	}
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// EstimateTokens estimates the token count of s: CJK characters weigh 2/3 of a
// token, every other character 1/4, each group rounded up and summed. Empty
// text is zero.
func EstimateTokens(s string) int {
	var cjk, other int
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return (cjk*2+2)/3 + (other+3)/4
}

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// collapseSpace folds every whitespace run into one space and trims the ends.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// Preview is the compact snippet shown with a knowledge source: whitespace
// collapsed and at most 100 characters.
func Preview(s string) string { return truncateRunes(collapseSpace(s), previewRunes) }

const previewRunes = 100

func hasContent(rs []rune) bool {
	for _, r := range rs {
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

package contentmoderation

import (
	"strings"
	"unicode/utf8"
)

// Request size limits of the moderation client.
const (
	maxTextChunkBytes  = 12 << 10
	maxImageBatchBytes = 20 << 20
)

// splitText trims s and cuts it into pieces of at most limit bytes. A piece
// never ends inside a UTF-8 sequence.
func splitText(s string, limit int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if limit < utf8.UTFMax {
		limit = utf8.UTFMax
	}
	var out []string
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 {
			cut = limit
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

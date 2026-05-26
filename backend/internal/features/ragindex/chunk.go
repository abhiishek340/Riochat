package ragindex

// Defaults applied when the configured chunking values are unusable.
const (
	defaultChunkTokens   = 512
	defaultOverlapTokens = 64
	charsPerToken        = 2
)

// Split cuts text into overlapping chunks for embedding.
//
// Sizes are tokens, converted to a window of two characters per token and
// measured in Unicode characters so a multi-byte character is never cut. A
// chunk size of zero or less means 512 tokens, a negative overlap means 64, and
// an overlap that is not smaller than the window becomes a quarter of it.
//
// Text that fits one window comes back whole (or not at all when it is only
// whitespace). Longer text is cut at the last blank line inside the window,
// else at the last line break, when that point lies past the half of the
// window; otherwise at the window end. Whitespace-only pieces are dropped and
// pieces are not trimmed. Every step advances by at least one character.
func Split(text string, chunkTokens, overlapTokens int) []string {
	if chunkTokens <= 0 {
		chunkTokens = defaultChunkTokens
	}
	if overlapTokens < 0 {
		overlapTokens = defaultOverlapTokens
	}
	window := chunkTokens * charsPerToken
	overlap := overlapTokens * charsPerToken
	if overlap >= window {
		overlap = window / 4
	}
	rs := []rune(text)
	n := len(rs)
	if n <= window {
		if hasContent(rs) {
			return []string{text}
		}
		return nil
	}
	var out []string
	start := 0
	for {
		end := start + window
		if end > n {
			end = n
		}
		cut := end
		if end < n {
			cut = preferredCut(rs, start, end, window)
		}
		if piece := rs[start:cut]; hasContent(piece) {
			out = append(out, string(piece))
		}
		if cut >= n {
			break
		}
		next := cut - overlap
		if next <= start {
			next = start + 1
		}
		start = next
	}
	return out
}

// preferredCut finds where to end the chunk that begins at start and whose
// window ends at end (exclusive).
func preferredCut(rs []rune, start, end, window int) int {
	half := window / 2
	// After the last blank line (two consecutive line breaks).
	for i := end - 1; i > start; i-- {
		if rs[i] == '\n' && rs[i-1] == '\n' {
			if i+1-start > half {
				return i + 1
			}
			break
		}
	}
	// After the last single line break.
	for i := end - 1; i >= start; i-- {
		if rs[i] == '\n' {
			if i+1-start > half {
				return i + 1
			}
			break
		}
	}
	return end
}

package mcp

import (
	"strconv"
	"strings"
)

const maxModelToolName = 64

func isNameLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isNameDigit(c byte) bool  { return c >= '0' && c <= '9' }

// modelToolName derives the identifier a model sees from a remote tool name:
// every run of characters outside letters, digits, "_" and "-" becomes a single
// "_", leading and trailing "_" or "-" are dropped, a first character that is
// not a letter or "_" gets the prefix "tool_", and the result is cut to 64
// bytes. An empty result means the tool cannot be offered.
func modelToolName(remote string) string {
	var b strings.Builder
	pendingSep := false
	for i := 0; i < len(remote); i++ {
		c := remote[i]
		if isNameLetter(c) || isNameDigit(c) || c == '_' || c == '-' {
			if pendingSep {
				b.WriteByte('_')
				pendingSep = false
			}
			b.WriteByte(c)
		} else {
			pendingSep = true
		}
	}
	if pendingSep {
		b.WriteByte('_')
	}
	name := strings.Trim(b.String(), "_-")
	if name == "" {
		return ""
	}
	if c := name[0]; !isNameLetter(c) && c != '_' {
		name = "tool_" + name
	}
	if len(name) > maxModelToolName {
		name = name[:maxModelToolName]
	}
	return name
}

// uniqueModelName resolves a collision by suffixing _2, _3, ... while keeping
// the whole name within the byte limit.
func uniqueModelName(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "_" + strconv.Itoa(n)
		cut := base
		if len(cut) > maxModelToolName-len(suffix) {
			cut = cut[:maxModelToolName-len(suffix)]
		}
		if cand := cut + suffix; !used[cand] {
			return cand
		}
	}
}

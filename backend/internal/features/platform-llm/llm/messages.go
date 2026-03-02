package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// normalizeRole maps unknown roles to user.
func normalizeRole(r Role) Role {
	switch Role(strings.ToLower(strings.TrimSpace(string(r)))) {
	case RoleSystem:
		return RoleSystem
	case RoleDeveloper:
		return RoleDeveloper
	case RoleAssistant:
		return RoleAssistant
	case RoleTool:
		return RoleTool
	case RoleFunction:
		return RoleFunction
	}
	return RoleUser
}

// effectiveParts returns the ordered content of a message. Parts are
// authoritative; Content is used when there are no parts, or is prepended when
// the parts carry no text part of their own.
func effectiveParts(m Message) []Part {
	if len(m.Parts) == 0 {
		if strings.TrimSpace(m.Content) == "" {
			return nil
		}
		return []Part{{Kind: PartText, Text: m.Content}}
	}
	if strings.TrimSpace(m.Content) != "" {
		for _, p := range m.Parts {
			if p.Kind == PartText || p.Kind == "" {
				return m.Parts
			}
		}
		return append([]Part{{Kind: PartText, Text: m.Content}}, m.Parts...)
	}
	return m.Parts
}

func partHasContent(p Part) bool {
	switch p.Kind {
	case PartImage, PartVideo, PartAudio, PartDocument:
		return len(p.Data) > 0
	}
	return strings.TrimSpace(p.Text) != ""
}

func messageHasContent(m Message) bool {
	if len(m.ToolCalls) > 0 || len(m.ToolResults) > 0 {
		return true
	}
	for _, p := range effectiveParts(m) {
		if partHasContent(p) {
			return true
		}
	}
	return false
}

// normalizeMessages applies the shared rules: unknown roles become user,
// messages with nothing in them are dropped, and an empty list becomes one
// empty user message.
func normalizeMessages(in []Message) []Message {
	out := make([]Message, 0, len(in))
	for _, m := range in {
		m.Role = normalizeRole(m.Role)
		if !messageHasContent(m) {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return []Message{{Role: RoleUser}}
	}
	return out
}

// isPlainText reports a message that is only text.
func isPlainText(m Message) bool {
	if len(m.ToolCalls) > 0 || len(m.ToolResults) > 0 || m.CacheHint {
		return false
	}
	for _, p := range effectiveParts(m) {
		if p.Kind != PartText && p.Kind != "" || p.CacheHint {
			return false
		}
	}
	return true
}

// isTextOnly reports a message made only of text parts, whatever its hints.
func isTextOnly(m Message) bool {
	if len(m.ToolCalls) > 0 || len(m.ToolResults) > 0 {
		return false
	}
	for _, p := range effectiveParts(m) {
		if p.Kind != PartText && p.Kind != "" {
			return false
		}
	}
	return true
}

func plainText(m Message) string {
	var parts []string
	for _, p := range effectiveParts(m) {
		parts = append(parts, p.Text)
	}
	return strings.Join(parts, "")
}

// mergeLeadingSystem joins consecutive leading plain-text system messages
// into one, separated by blank lines.
func mergeLeadingSystem(in []Message) []Message {
	n := 0
	for n < len(in) && normalizeRole(in[n].Role) == RoleSystem && isPlainText(in[n]) {
		n++
	}
	if n < 2 {
		return in
	}
	var texts []string
	for _, m := range in[:n] {
		if t := plainText(m); strings.TrimSpace(t) != "" {
			texts = append(texts, t)
		}
	}
	merged := Message{Role: RoleSystem, Content: strings.Join(texts, "\n\n")}
	return append([]Message{merged}, in[n:]...)
}

func dataURL(mime string, data []byte) string {
	if mime == "" {
		mime = "application/octet-stream"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func b64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// filePartText renders extracted file text for a text-only slot.
func filePartText(p Part) string {
	name := strings.TrimSpace(p.FileName)
	if name == "" {
		return p.Text
	}
	return "File: " + name + "\n" + p.Text
}

// partAsText returns the text for text and file parts.
func partAsText(p Part) (string, bool) {
	switch p.Kind {
	case PartText, "":
		if strings.TrimSpace(p.Text) == "" {
			return "", false
		}
		return p.Text, true
	case PartFile:
		if strings.TrimSpace(p.Text) == "" {
			return "", false
		}
		return filePartText(p), true
	}
	return "", false
}

// toolResultOutput encodes a tool result as the text sent upstream. Failures
// are a JSON object with ok:false, status and error; successes pass the
// output through (empty becomes {}).
func toolResultOutput(tr ToolResult) string {
	if tr.IsError {
		b, _ := json.Marshal(map[string]any{"ok": false, "status": tr.Status, "error": tr.Error})
		return string(b)
	}
	if strings.TrimSpace(tr.Output) == "" {
		return "{}"
	}
	return tr.Output
}

func argsOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// parseArgsObject parses tool-call arguments into an object; non-object or
// invalid text becomes {"arguments": "<raw>"}.
func parseArgsObject(s string) map[string]any {
	var m map[string]any
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &m); err == nil && m != nil {
			return m
		}
	} else {
		return map[string]any{}
	}
	return map[string]any{"arguments": s}
}

// toolSchema returns a JSON Schema object for a tool, defaulting to an empty
// object schema and adding type/properties when missing.
func toolSchema(s map[string]any) map[string]any {
	if s == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := cloneJSON(s).(map[string]any)
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if t, _ := out["type"].(string); t == "object" {
		if _, ok := out["properties"]; !ok {
			out["properties"] = map[string]any{}
		}
	}
	return out
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func uniqueAppend(list []string, vals ...string) []string {
	for _, v := range vals {
		dup := false
		for _, e := range list {
			if e == v {
				dup = true
				break
			}
		}
		if !dup && v != "" {
			list = append(list, v)
		}
	}
	return list
}

func addCitation(list []Citation, c Citation) []Citation {
	c.URL = strings.TrimSpace(c.URL)
	if c.URL == "" {
		return list
	}
	for _, e := range list {
		if e.URL == c.URL {
			return list
		}
	}
	return append(list, c)
}

func mimeExt(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

// prepareOpenAIMessages applies the shared preprocessing of the OpenAI-style
// text protocols: the instructions text (when instructionsAsSystem) becomes a
// leading system message, developer messages are treated as system messages,
// cache hints are dropped unless explicit caching is active, and leading
// plain-text system messages are merged unless explicit caching is active.
func prepareOpenAIMessages(in []Message, instructions string, instructionsAsSystem, explicit, developerAsSystem bool) []Message {
	msgs := normalizeMessages(in)
	out := make([]Message, 0, len(msgs)+1)
	if instructionsAsSystem && strings.TrimSpace(instructions) != "" {
		out = append(out, Message{Role: RoleSystem, Content: instructions})
	}
	for _, m := range msgs {
		if developerAsSystem && m.Role == RoleDeveloper {
			m.Role = RoleSystem
		}
		if !explicit {
			m.CacheHint = false
			if len(m.Parts) > 0 {
				parts := make([]Part, len(m.Parts))
				copy(parts, m.Parts)
				for i := range parts {
					parts[i].CacheHint = false
				}
				m.Parts = parts
			}
		}
		out = append(out, m)
	}
	if !explicit {
		out = mergeLeadingSystem(out)
	}
	return out
}

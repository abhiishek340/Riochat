package llm

import (
	"bytes"
	"encoding/json"
)

func maxInt(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// numField reads an integer-valued field of a JSON object.
func numField(m map[string]any, keys ...string) (int64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if i, ok := toInt(v); ok {
				return i, true
			}
			if f, ok := toFloat(v); ok {
				return int64(f), true
			}
		}
	}
	return 0, false
}

func objField(m map[string]any, key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}

// openAIUsage normalizes an OpenAI-compatible usage object. xai selects the
// convention that reasoning tokens are reported separately from output.
func openAIUsage(u map[string]any, xai bool) Usage {
	var out Usage
	if u == nil {
		return out
	}
	total, _ := numField(u, "input_tokens", "prompt_tokens")
	output, _ := numField(u, "output_tokens", "completion_tokens")

	var reasoning int64
	for _, d := range []string{"output_tokens_details", "completion_tokens_details"} {
		if det := objField(u, d); det != nil {
			if r, ok := numField(det, "reasoning_tokens"); ok {
				reasoning = r
				break
			}
		}
	}
	if reasoning == 0 {
		reasoning, _ = numField(u, "reasoning_tokens")
	}

	var read int64
	for _, d := range []string{"input_tokens_details", "prompt_tokens_details"} {
		if det := objField(u, d); det != nil {
			if r, ok := numField(det, "cached_tokens", "cache_read_tokens"); ok {
				read = r
				break
			}
		}
	}
	if read == 0 {
		read, _ = numField(u, "cache_read_input_tokens", "cache_read_tokens")
	}

	var write, w5, w1 int64
	writeKeys := []string{"cache_write_tokens", "cache_creation_tokens", "cached_creation_tokens", "cache_creation_input_tokens", "cache_write_input_tokens"}
	found := false
	for _, src := range []map[string]any{
		objField(u, "input_tokens_details"), objField(u, "prompt_tokens_details"), u,
	} {
		if src == nil {
			continue
		}
		if w, ok := numField(src, writeKeys...); ok {
			write, found = w, true
			break
		}
		if cc := objField(src, "cache_creation"); cc != nil {
			a, ok1 := numField(cc, "ephemeral_5m_input_tokens", "5m", "ephemeral_5m")
			b, ok2 := numField(cc, "ephemeral_1h_input_tokens", "1h", "ephemeral_1h")
			if ok1 || ok2 {
				w5, w1, write, found = a, b, a+b, true
				break
			}
		}
	}
	_ = found

	out.CacheReadTokens = read
	out.CacheWriteTokens = write
	out.CacheWrite5mTokens = w5
	out.CacheWrite1hTokens = w1
	out.InputTokens = maxInt(0, total-read-write)
	out.ReasoningTokens = reasoning
	if xai {
		out.OutputTokens = output
	} else {
		out.OutputTokens = maxInt(0, output-reasoning)
	}
	if s, ok := u["service_tier"].(string); ok {
		out.ServiceTier = s
	}
	out.Raw = rawJSON(u)
	return out
}

// anthropicUsage normalizes an Anthropic usage object (input already excludes
// cached tokens).
func anthropicUsage(u map[string]any) Usage {
	var out Usage
	if u == nil {
		return out
	}
	out.InputTokens, _ = numField(u, "input_tokens")
	out.OutputTokens, _ = numField(u, "output_tokens")
	out.CacheReadTokens, _ = numField(u, "cache_read_input_tokens")
	if cc := objField(u, "cache_creation"); cc != nil {
		a, ok1 := numField(cc, "ephemeral_5m_input_tokens")
		b, ok2 := numField(cc, "ephemeral_1h_input_tokens")
		if ok1 || ok2 {
			out.CacheWrite5mTokens, out.CacheWrite1hTokens = a, b
			out.CacheWriteTokens = a + b
		}
	}
	if out.CacheWriteTokens == 0 {
		out.CacheWriteTokens, _ = numField(u, "cache_creation_input_tokens")
	}
	if s, ok := u["speed"].(string); ok {
		out.Speed = s
	}
	if s, ok := u["service_tier"].(string); ok {
		out.ServiceTier = s
	}
	out.Raw = rawJSON(u)
	return out
}

// geminiUsage normalizes usageMetadata.
func geminiUsage(u map[string]any) Usage {
	var out Usage
	if u == nil {
		return out
	}
	prompt, _ := numField(u, "promptTokenCount")
	cached, _ := numField(u, "cachedContentTokenCount")
	out.CacheReadTokens = cached
	out.InputTokens = maxInt(0, prompt-cached)
	out.OutputTokens, _ = numField(u, "candidatesTokenCount")
	out.ReasoningTokens, _ = numField(u, "thoughtsTokenCount")
	out.Raw = rawJSON(u)
	return out
}

// interactionsUsage normalizes Gemini Interactions usage (outputs and thoughts
// are disjoint).
func interactionsUsage(u map[string]any) Usage {
	var out Usage
	if u == nil {
		return out
	}
	total, _ := numField(u, "total_input_tokens")
	cached, _ := numField(u, "total_cached_tokens")
	out.CacheReadTokens = cached
	out.InputTokens = maxInt(0, total-cached)
	out.OutputTokens, _ = numField(u, "total_output_tokens")
	out.ReasoningTokens, _ = numField(u, "total_thought_tokens")
	out.Raw = rawJSON(u)
	return out
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// mergeRawUsage combines raw usage documents into a JSON array without
// duplicates. A single distinct document stays a plain object.
func mergeRawUsage(a, b json.RawMessage) json.RawMessage {
	var docs []json.RawMessage
	add := func(r json.RawMessage) {
		r = bytes.TrimSpace(r)
		if len(r) == 0 || string(r) == "null" {
			return
		}
		if r[0] == '[' {
			var arr []json.RawMessage
			if json.Unmarshal(r, &arr) == nil {
				for _, e := range arr {
					docs = appendUnique(docs, e)
				}
				return
			}
		}
		docs = appendUnique(docs, r)
	}
	add(a)
	add(b)
	switch len(docs) {
	case 0:
		return nil
	case 1:
		return docs[0]
	}
	out, _ := json.Marshal(docs)
	return out
}

func appendUnique(docs []json.RawMessage, r json.RawMessage) []json.RawMessage {
	var c bytes.Buffer
	if json.Compact(&c, r) != nil {
		c.Reset()
		c.Write(r)
	}
	for _, d := range docs {
		if bytes.Equal(d, c.Bytes()) {
			return docs
		}
	}
	return append(docs, json.RawMessage(append([]byte(nil), c.Bytes()...)))
}

// mergeUsage folds a later usage report into an earlier one: observed values
// replace, zero values do not erase, raw documents accumulate.
func mergeUsage(dst *Usage, src Usage) {
	if src.IsZero() {
		return
	}
	if src.InputObserved() {
		dst.InputTokens, dst.CacheReadTokens, dst.CacheWriteTokens = src.InputTokens, src.CacheReadTokens, src.CacheWriteTokens
		dst.CacheWrite5mTokens, dst.CacheWrite1hTokens = src.CacheWrite5mTokens, src.CacheWrite1hTokens
	}
	if src.OutputObserved() {
		dst.OutputTokens, dst.ReasoningTokens = src.OutputTokens, src.ReasoningTokens
	}
	if src.Speed != "" {
		dst.Speed = src.Speed
	}
	if src.ServiceTier != "" {
		dst.ServiceTier = src.ServiceTier
	}
	dst.Raw = mergeRawUsage(dst.Raw, src.Raw)
}

package usersettings

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Limits for the structured settings.
const (
	maxToolIDs         = 128
	maxPlacements      = 128
	maxToolbarEntries  = 6
	maxControlIDBytes  = 96
	placementToolbar   = "toolbar"
	placementMenu      = "menu"
	keyMCPToolIDs      = "chat.default_mcp_tool_ids"
	keyModelPlacements = "chat.model_control_placements"
)

// validator reports whether a raw value is acceptable for a key.
type validator func(value string) bool

// definition describes one supported key.
type definition struct {
	def   string
	valid validator
}

var catalog = buildCatalog()

// orderedKeys lists every supported key in a stable order.
var orderedKeys = func() []string {
	keys := make([]string, 0, len(catalog))
	for k := range catalog {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}()

func buildCatalog() map[string]definition {
	m := map[string]definition{}
	flag := func(key string, def bool) {
		m[key] = definition{def: strconv.FormatBool(def), valid: validBool}
	}
	choice := func(key, def string, allowed ...string) {
		m[key] = definition{def: def, valid: func(v string) bool {
			for _, a := range allowed {
				if v == a {
					return true
				}
			}
			return false
		}}
	}
	flag("chat.show_token_usage", true)
	flag("chat.show_model_info", true)
	flag("chat.show_latency", true)
	flag("chat.show_billing_cost", true)
	flag("chat.show_process_trace", true)
	flag("chat.auto_generate_title", true)
	flag("chat.auto_generate_labels", true)
	flag("chat.delete_conversation_files_by_default", false)
	flag("chat.context_compact_auto", true)
	flag("chat.markdown_render", true)
	flag("chat.auto_expand_thinking", true)
	flag("chat.auto_expand_tool_calls", true)
	flag("chat.restore_draft_on_failure", true)
	flag("chat.preserve_conversation_drafts", true)
	flag("chat.reuse_model_options", true)
	flag("chat.reasoning_content_passback", true)

	choice("chat.file_mode", "auto", "auto", "full_context", "rag")
	choice("chat.send_on_enter", "enter", "enter", "ctrl_enter", "meta_enter")
	choice("chat.input_height", "standard", "compact", "standard", "loose")
	choice("chat.content_width", "compact", "compact", "standard", "wide")

	m["chat.default_model"] = definition{def: "", valid: func(string) bool { return true }}
	m[keyMCPToolIDs] = definition{def: "[]", valid: validToolIDs}
	m[keyModelPlacements] = definition{def: "{}", valid: validPlacements}
	return m
}

func validBool(v string) bool { return v == "true" || v == "false" }

// decodeSingle parses text as exactly one JSON value, using json.Number for
// numeric literals so integers are not rounded through float64.
func decodeSingle(text string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return v, true
}

// validToolIDs accepts a JSON array of at most 128 positive integers; the
// literal null counts as an empty list.
func validToolIDs(text string) bool {
	v, ok := decodeSingle(text)
	if !ok {
		return false
	}
	if v == nil {
		return true
	}
	items, ok := v.([]any)
	if !ok || len(items) > maxToolIDs {
		return false
	}
	for _, it := range items {
		n, ok := it.(json.Number)
		if !ok {
			return false
		}
		id, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil || id <= 0 {
			return false
		}
	}
	return true
}

// validPlacements accepts a JSON object of at most 128 control ids, each id
// non-blank and at most 96 bytes, mapped to "toolbar" or "menu" with at most
// six toolbar entries; the literal null is accepted.
func validPlacements(text string) bool {
	v, ok := decodeSingle(text)
	if !ok {
		return false
	}
	if v == nil {
		return true
	}
	obj, ok := v.(map[string]any)
	if !ok || len(obj) > maxPlacements {
		return false
	}
	toolbar := 0
	for id, raw := range obj {
		if len(id) > maxControlIDBytes || len(bytes.TrimSpace([]byte(id))) == 0 {
			return false
		}
		switch raw {
		case placementToolbar:
			toolbar++
		case placementMenu:
		default:
			return false
		}
	}
	return toolbar <= maxToolbarEntries
}

// defaults returns a fresh map of every supported key to its default value.
func defaults() map[string]string {
	out := make(map[string]string, len(catalog))
	for k, d := range catalog {
		out[k] = d.def
	}
	return out
}

// Keys returns the supported setting keys in sorted order.
func Keys() []string { return append([]string(nil), orderedKeys...) }

// Default returns the default value of a supported key.
func Default(key string) (string, bool) {
	d, ok := catalog[key]
	return d.def, ok
}

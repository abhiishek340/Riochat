package user

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits of the structured settings.
const (
	maxMCPToolIDs        = 128
	maxPlacementEntries  = 128
	maxPlacementIDLen    = 96
	maxToolbarPlacements = 6
	placementToolbar     = "toolbar"
	placementMenu        = "menu"
)

// Setting keys that other areas read through the settings cache.
const (
	SettingContextCompactAuto      = "chat.context_compact_auto"
	SettingFileMode                = "chat.file_mode"
	SettingReasoningContentPassing = "chat.reasoning_content_passback"
	SettingDefaultMCPToolIDs       = "chat.default_mcp_tool_ids"
	SettingModelControlPlacements  = "chat.model_control_placements"
)

type settingDef struct {
	key      string
	fallback string
	check    func(string) bool
}

func boolCheck(v string) bool { return v == "true" || v == "false" }

func oneOf(options ...string) func(string) bool {
	return func(v string) bool {
		for _, o := range options {
			if v == o {
				return true
			}
		}
		return false
	}
}

// anyText accepts any string the database can hold: valid UTF-8 without NUL.
func anyText(v string) bool { return utf8.ValidString(v) && !strings.ContainsRune(v, 0) }

// decodeSingle decodes exactly one JSON value (no trailing content), keeping
// numbers verbatim so that "1.5" and "1e3" can be told apart from integers.
func decodeSingle(text string, into any) bool {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return false
	}
	var extra json.RawMessage
	return errors.Is(dec.Decode(&extra), io.EOF)
}

func validToolIDs(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" {
		return false
	}
	var items []any
	if !decodeSingle(text, &items) {
		return false
	}
	if len(items) > maxMCPToolIDs {
		return false
	}
	for _, it := range items {
		num, ok := it.(json.Number)
		if !ok {
			return false
		}
		id, err := strconv.ParseUint(num.String(), 10, 64)
		if err != nil || id == 0 {
			return false
		}
	}
	return true
}

func validPlacements(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" {
		return false
	}
	var entries map[string]any
	if !decodeSingle(text, &entries) {
		return false
	}
	if len(entries) > maxPlacementEntries {
		return false
	}
	toolbar := 0
	for id, v := range entries {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > maxPlacementIDLen {
			return false
		}
		place, ok := v.(string)
		if !ok {
			return false
		}
		switch place {
		case placementToolbar:
			toolbar++
		case placementMenu:
		default:
			return false
		}
	}
	return toolbar <= maxToolbarPlacements
}

// settingCatalog lists every supported key in a stable order.
var settingCatalog = func() []settingDef {
	defs := []settingDef{}
	for _, k := range []string{
		"chat.show_token_usage", "chat.show_model_info", "chat.show_latency",
		"chat.show_billing_cost", "chat.show_process_trace", "chat.auto_generate_title",
		"chat.auto_generate_labels",
	} {
		defs = append(defs, settingDef{k, "true", boolCheck})
	}
	defs = append(defs, settingDef{"chat.delete_conversation_files_by_default", "false", boolCheck})
	for _, k := range []string{
		SettingContextCompactAuto, "chat.markdown_render", "chat.auto_expand_thinking",
		"chat.auto_expand_tool_calls", "chat.restore_draft_on_failure",
		"chat.preserve_conversation_drafts", "chat.reuse_model_options", SettingReasoningContentPassing,
	} {
		defs = append(defs, settingDef{k, "true", boolCheck})
	}
	defs = append(defs,
		settingDef{SettingFileMode, "auto", oneOf("auto", "full_context", "rag")},
		settingDef{"chat.send_on_enter", "enter", oneOf("enter", "ctrl_enter", "meta_enter")},
		settingDef{"chat.input_height", "standard", oneOf("compact", "standard", "loose")},
		settingDef{"chat.content_width", "compact", oneOf("compact", "standard", "wide")},
		settingDef{"chat.default_model", "", anyText},
		settingDef{SettingDefaultMCPToolIDs, "[]", validToolIDs},
		settingDef{SettingModelControlPlacements, "{}", validPlacements},
	)
	return defs
}()

var settingIndex = func() map[string]settingDef {
	m := make(map[string]settingDef, len(settingCatalog))
	for _, d := range settingCatalog {
		m[d.key] = d
	}
	return m
}()

// SettingDefaults returns a fresh map of every supported key to its default.
func SettingDefaults() map[string]string {
	out := make(map[string]string, len(settingCatalog))
	for _, d := range settingCatalog {
		out[d.key] = d.fallback
	}
	return out
}

// SettingKeys returns the supported keys in sorted order.
func SettingKeys() []string {
	keys := make([]string, 0, len(settingCatalog))
	for _, d := range settingCatalog {
		keys = append(keys, d.key)
	}
	sort.Strings(keys)
	return keys
}

// ValidateSettingValue reports whether value is acceptable for key. An unknown
// key is never valid.
func ValidateSettingValue(key, value string) bool {
	d, ok := settingIndex[key]
	return ok && d.check(value)
}

// validatePatch resolves a raw PATCH payload into the entries to write, keyed by
// trimmed key. Unknown keys are reported before bad values so that the outcome
// does not depend on map order. When two raw keys collapse to the same trimmed
// key the one that sorts last wins.
func validatePatch(raw map[string]string) (map[string]string, error) {
	rawKeys := make([]string, 0, len(raw))
	for k := range raw {
		rawKeys = append(rawKeys, k)
	}
	sort.Strings(rawKeys)
	out := make(map[string]string, len(raw))
	for _, rk := range rawKeys {
		key := strings.TrimSpace(rk)
		if _, ok := settingIndex[key]; !ok {
			return nil, ErrUnknownSettingKey
		}
		out[key] = raw[rk]
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !settingIndex[key].check(out[key]) {
			return nil, ErrInvalidSettingVal.WithDiagnostic("rejected value for " + key)
		}
	}
	return out, nil
}

package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// userKey describes one personal preference.
type userKey struct {
	key   string
	def   string
	valid func(v string) bool
}

func boolValue(v string) bool { return v == "true" || v == "false" }

func enumValue(options ...string) func(string) bool {
	return func(v string) bool {
		for _, o := range options {
			if v == o {
				return true
			}
		}
		return false
	}
}

func anyValue(string) bool { return true }

const (
	maxToolIDs        = 128
	maxPlacements     = 128
	maxToolbarEntries = 6
	maxPlacementIDLen = 96
)

// validToolIDs accepts a JSON array of at most 128 positive integers.
func validToolIDs(v string) bool {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(v)))
	var items []json.RawMessage
	if err := dec.Decode(&items); err != nil || items == nil {
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return false
	}
	if len(items) > maxToolIDs {
		return false
	}
	for _, raw := range items {
		raw = bytes.TrimSpace(raw)
		for _, c := range raw {
			if c < '0' || c > '9' {
				return false
			}
		}
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || n <= 0 {
			return false
		}
	}
	return true
}

// validPlacements accepts a JSON object mapping control ids to "toolbar" or
// "menu", with at most 128 entries and at most 6 toolbar placements.
func validPlacements(v string) bool {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(v)))
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return false
	}
	if len(m) > maxPlacements {
		return false
	}
	toolbar := 0
	for id, val := range m {
		if strings.TrimSpace(id) == "" || utf8.RuneCountInString(id) > maxPlacementIDLen {
			return false
		}
		s, ok := val.(string)
		if !ok {
			return false
		}
		switch s {
		case "toolbar":
			toolbar++
		case "menu":
		default:
			return false
		}
	}
	return toolbar <= maxToolbarEntries
}

var userKeys = []userKey{
	{"chat.file_mode", "auto", enumValue("auto", "full_context", "rag")},
	{"chat.send_on_enter", "enter", enumValue("enter", "ctrl_enter", "meta_enter")},
	{"chat.show_token_usage", "true", boolValue},
	{"chat.show_model_info", "true", boolValue},
	{"chat.show_latency", "true", boolValue},
	{"chat.show_billing_cost", "true", boolValue},
	{"chat.show_process_trace", "true", boolValue},
	{"chat.default_model", "", anyValue},
	{"chat.auto_generate_title", "true", boolValue},
	{"chat.auto_generate_labels", "true", boolValue},
	{"chat.delete_conversation_files_by_default", "false", boolValue},
	{"chat.context_compact_auto", "true", boolValue},
	{"chat.markdown_render", "true", boolValue},
	{"chat.auto_expand_thinking", "true", boolValue},
	{"chat.auto_expand_tool_calls", "true", boolValue},
	{"chat.restore_draft_on_failure", "true", boolValue},
	{"chat.preserve_conversation_drafts", "true", boolValue},
	{"chat.reuse_model_options", "true", boolValue},
	{"chat.reasoning_content_passback", "true", boolValue},
	{"chat.input_height", "standard", enumValue("compact", "standard", "loose")},
	{"chat.content_width", "compact", enumValue("compact", "standard", "wide")},
	{"chat.default_mcp_tool_ids", "[]", validToolIDs},
	{"chat.model_control_placements", "{}", validPlacements},
}

var userKeyIndex = func() map[string]*userKey {
	m := make(map[string]*userKey, len(userKeys))
	for i := range userKeys {
		m[userKeys[i].key] = &userKeys[i]
	}
	return m
}()

// UserSettingKeys lists the supported preference keys.
func UserSettingKeys() []string {
	out := make([]string, len(userKeys))
	for i, k := range userKeys {
		out[i] = k.key
	}
	return out
}

// UserSettings returns every supported key for a user, with defaults filled in
// for keys without a stored row. Stored rows for keys that are no longer
// supported are ignored.
func (s *Service) UserSettings(ctx context.Context, userID uint) (map[string]string, error) {
	stored, err := s.users.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(userKeys))
	for _, k := range userKeys {
		if v, ok := stored[k.key]; ok {
			out[k.key] = v
		} else {
			out[k.key] = k.def
		}
	}
	return out, nil
}

// UserSetting returns one preference of a user. ok is false when the key is
// unsupported or has no stored value.
func (s *Service) UserSetting(ctx context.Context, userID uint, key string) (value string, ok bool, err error) {
	if userKeyIndex[key] == nil {
		return "", false, nil
	}
	stored, err := s.users.load(ctx, userID)
	if err != nil {
		return "", false, err
	}
	v, ok := stored[key]
	return v, ok, nil
}

// PatchUserSettings validates the whole map, then stores it and returns the
// full resulting map. Keys are trimmed; values are kept as given. Any unknown
// key or invalid value rejects the request without writing anything.
func (s *Service) PatchUserSettings(ctx context.Context, userID uint, in map[string]string) (map[string]string, error) {
	raw := make([]string, 0, len(in))
	for k := range in {
		raw = append(raw, k)
	}
	sort.Strings(raw)

	values := make(map[string]string, len(in))
	for _, k := range raw {
		key := strings.TrimSpace(k)
		if userKeyIndex[key] == nil {
			return nil, ErrUserUnknownKey.WithStatus(400)
		}
		values[key] = in[k]
	}
	for _, k := range raw {
		key := strings.TrimSpace(k)
		if !userKeyIndex[key].valid(in[k]) {
			return nil, ErrUserInvalidValue.WithStatus(400).WithDiagnostic("invalid value for " + key)
		}
	}
	if len(values) > 0 {
		if err := s.users.save(ctx, userID, values, s.clock.Now()); err != nil {
			return nil, err
		}
		if hook := s.port().UserSettings; hook != nil {
			keys := make([]string, 0, len(values))
			for k := range values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			hook(ctx, userID, keys)
		}
	}
	return s.UserSettings(ctx, userID)
}

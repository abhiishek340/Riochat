package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// options wraps the free-form option map with typed, alias-aware accessors.
type options map[string]any

// optionError builds the deterministic validation error naming an option.
func optionError(name, why string) *Error {
	return invalid(ErrInvalidOption, "invalid option %q: %s", name, why)
}

func (o options) first(keys ...string) (any, string, bool) {
	for _, k := range keys {
		if v, ok := o[k]; ok {
			return v, k, true
		}
	}
	return nil, "", false
}

func (o options) has(keys ...string) bool {
	_, _, ok := o.first(keys...)
	return ok
}

// toInt accepts integers and integral floats only.
func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		if x > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	case float32:
		return toInt(float64(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return 0, false
		}
		return int64(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return toInt(f)
		}
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	if i, ok := toInt(v); ok {
		return float64(i), true
	}
	return 0, false
}

func (o options) int(keys ...string) (int64, bool) {
	v, _, ok := o.first(keys...)
	if !ok {
		return 0, false
	}
	return toInt(v)
}

// positiveInt returns the value when it is a positive integer.
func (o options) positiveInt(keys ...string) (int64, bool) {
	n, ok := o.int(keys...)
	return n, ok && n > 0
}

func (o options) float(keys ...string) (float64, bool) {
	v, _, ok := o.first(keys...)
	if !ok {
		return 0, false
	}
	return toFloat(v)
}

func (o options) str(keys ...string) (string, bool) {
	v, _, ok := o.first(keys...)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && s != ""
}

func (o options) boolean(keys ...string) (bool, bool) {
	v, _, ok := o.first(keys...)
	if !ok {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		return b, err == nil
	}
	return false, false
}

func (o options) object(key string) (map[string]any, bool) {
	m, ok := o[key].(map[string]any)
	return m, ok
}

// requireObject returns the object value of key; a present non-object value is
// a validation error.
func (o options) requireObject(key string) (map[string]any, error) {
	v, ok := o[key]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, optionError(key, "must be an object")
	}
	return m, nil
}

// nativeTools validates and returns the provider-native tool objects.
func (o options) nativeTools() ([]map[string]any, error) {
	v, ok := o["tools"]
	if !ok || v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		if typed, ok2 := v.([]map[string]any); ok2 {
			return typed, nil
		}
		return nil, optionError("tools", "must be an array")
	}
	out := make([]map[string]any, 0, len(list))
	for i, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, optionError(fmt.Sprintf("tools[%d]", i), "must be an object")
		}
		out = append(out, m)
	}
	return out, nil
}

func (o options) stringList(keys ...string) []string {
	v, _, ok := o.first(keys...)
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case string:
		if s := strings.TrimSpace(x); s != "" {
			return []string{s}
		}
	case []string:
		return x
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ---- body assembly --------------------------------------------------------

// deniedKeys are never copied from user options into a request body.
var deniedKeys = map[string]bool{
	"model": true, "messages": true, "input": true, "instructions": true, "prompt": true,
	"system": true, "systemInstruction": true, "headers": true, "api_key": true, "apiKey": true,
	"base_url": true, "baseURL": true, "stream": true, "previous_response_id": true,
}

// baseConsumed are option keys every adapter interprets itself.
var baseConsumed = []string{
	"web_search", "prompt_cache", "prompt_cache_options", "cache_timeout", "enable_cache",
	"tools", "stream_options", "include", "betas", "anthropic_beta", "anthropic-beta",
}

// copyExtras copies option entries that no adapter rule consumed. An existing
// body entry wins; object values merge into an existing object key by key.
func copyExtras(body map[string]any, opts options, consumed []string, protected ...string) {
	skip := map[string]bool{}
	for _, k := range baseConsumed {
		skip[k] = true
	}
	for _, k := range consumed {
		skip[k] = true
	}
	for _, k := range protected {
		skip[k] = true
	}
	for k, v := range opts {
		if skip[k] || deniedKeys[k] {
			continue
		}
		mergeInto(body, k, v)
	}
}

func mergeInto(body map[string]any, key string, v any) {
	existing, ok := body[key]
	if !ok {
		body[key] = cloneJSON(v)
		return
	}
	em, ok1 := existing.(map[string]any)
	vm, ok2 := v.(map[string]any)
	if ok1 && ok2 {
		for k, x := range vm {
			mergeInto(em, k, x)
		}
	}
}

// cloneJSON deep-copies maps and slices so request bodies never alias caller
// data.
func cloneJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneJSON(e)
		}
		return out
	}
	return v
}

func setIf(body map[string]any, key string, v any, ok bool) {
	if ok {
		body[key] = v
	}
}

// responseFormat normalizes the response_format option. It returns the format
// type ("json_object", "json_schema", "text") and the schema object when one
// was given.
type responseFormat struct {
	Type   string
	Name   string
	Schema any
	Strict *bool
	Raw    map[string]any
}

func (o options) responseFormat() (*responseFormat, error) {
	v, ok := o["response_format"]
	if !ok || v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "json", "json_object":
			return &responseFormat{Type: "json_object"}, nil
		case "text":
			return &responseFormat{Type: "text"}, nil
		case "json_schema":
			return &responseFormat{Type: "json_schema"}, nil
		}
		return nil, optionError("response_format", "unknown format "+strconv.Quote(x))
	case map[string]any:
		t, _ := x["type"].(string)
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "json" {
			t = "json_object"
		}
		if t == "" {
			return nil, optionError("response_format", "object needs a type")
		}
		rf := &responseFormat{Type: t, Raw: x}
		src := x
		if js, ok := x["json_schema"].(map[string]any); ok {
			src = js
		}
		if n, ok := src["name"].(string); ok {
			rf.Name = n
		}
		if s, ok := src["schema"]; ok {
			rf.Schema = s
		}
		if b, ok := src["strict"].(bool); ok {
			rf.Strict = &b
		}
		return rf, nil
	}
	return nil, optionError("response_format", "must be a string or an object")
}

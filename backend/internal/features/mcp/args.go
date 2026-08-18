package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ArgumentError reports tool arguments that do not satisfy the tool's input
// schema. No upstream call is made for such a call.
type ArgumentError struct {
	Issues []string
}

func (e *ArgumentError) Error() string {
	return "argument validation failed: " + strings.Join(e.Issues, "; ")
}

// kindOf names the JSON type of a decoded value.
func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		if _, ok := wholeNumber(x); ok {
			return "integer"
		}
		return "number"
	case int64:
		return "integer"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// wholeNumber returns n as an int64 when it has no fractional part.
func wholeNumber(n json.Number) (int64, bool) {
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// declaredTypes lists the types a property schema names (nil when it names none).
func declaredTypes(prop map[string]any) []string {
	switch t := prop["type"].(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// fitExact returns v converted when it already has type t.
func fitExact(v any, t string) (any, bool) {
	switch t {
	case "string":
		_, ok := v.(string)
		return v, ok
	case "boolean":
		_, ok := v.(bool)
		return v, ok
	case "null":
		return v, v == nil
	case "array":
		_, ok := v.([]any)
		return v, ok
	case "object":
		_, ok := v.(map[string]any)
		return v, ok
	case "number":
		if n, ok := v.(json.Number); ok {
			return n, true
		}
	case "integer":
		if n, ok := v.(json.Number); ok {
			if i, ok := wholeNumber(n); ok {
				return i, true
			}
		}
	}
	return nil, false
}

// fitCoerced converts text values to the declared scalar type where that is
// unambiguous: "true"/"false" for booleans, numeric text for numbers.
func fitCoerced(v any, t string) (any, bool) {
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	s = strings.TrimSpace(s)
	switch t {
	case "boolean":
		switch strings.ToLower(s) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	case "integer":
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) && math.Abs(f) <= 1<<53 {
			return int64(f), true
		}
	case "number":
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, true
			}
			return f, true
		}
	}
	return nil, false
}

// numericValue returns the float value of a decoded or converted number.
func numericValue(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// jsonEqual compares decoded JSON values, numbers numerically.
func jsonEqual(a, b any) bool {
	if fa, ok := numericValue(a); ok {
		fb, ok := numericValue(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, e := range x {
			o, ok := y[k]
			if !ok || !jsonEqual(e, o) {
				return false
			}
		}
		return true
	}
	return false
}

// decodeObject reads exactly one JSON object from text (empty text is {}).
func decodeObject(text string) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New("arguments must be one valid JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("arguments must be one JSON object without trailing data")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be a JSON object")
	}
	return obj, nil
}

// prepareArguments validates and normalizes the model's arguments for a tool.
// It returns the canonical JSON text of the (possibly coerced) object. When the
// schema is not usable, only the object shape is enforced.
func prepareArguments(text, schemaJSON string) (string, error) {
	obj, err := decodeObject(text)
	if err != nil {
		return "", &ArgumentError{Issues: []string{err.Error()}}
	}
	var issues []string
	if schema, ok := parseSchemaObject(schemaJSON); ok {
		issues = checkAgainstSchema(obj, schema)
	}
	if len(issues) > 0 {
		return "", &ArgumentError{Issues: issues}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(convertNumbers(obj)); err != nil {
		return "", &ArgumentError{Issues: []string{"arguments cannot be encoded"}}
	}
	return strings.TrimSpace(buf.String()), nil
}

func checkAgainstSchema(obj, schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var issues []string
	names := make([]string, 0, len(obj))
	for k := range obj {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		value := obj[name]
		if types := declaredTypes(prop); len(types) > 0 {
			fitted, ok := fitAny(value, types)
			if !ok {
				issues = append(issues, fmt.Sprintf("%s: expected %s, received %s", name, strings.Join(types, " or "), kindOf(value)))
				continue
			}
			obj[name] = fitted
			value = fitted
		}
		if enum, ok := prop["enum"].([]any); ok {
			found := false
			for _, e := range enum {
				if jsonEqual(value, e) {
					found = true
					break
				}
			}
			if !found {
				issues = append(issues, fmt.Sprintf("%s: value is not one of the allowed values", name))
			}
		}
	}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			name, ok := r.(string)
			if !ok {
				continue
			}
			if _, present := obj[name]; present {
				continue
			}
			expected := "a value"
			if prop, ok := props[name].(map[string]any); ok {
				if types := declaredTypes(prop); len(types) > 0 {
					expected = strings.Join(types, " or ")
				}
			}
			issues = append(issues, fmt.Sprintf("%s: required, expected %s, received nothing", name, expected))
		}
	}
	return issues
}

// fitAny tries exact matches against every declared type first, then coercions.
func fitAny(v any, types []string) (any, bool) {
	for _, t := range types {
		if out, ok := fitExact(v, t); ok {
			return out, true
		}
	}
	for _, t := range types {
		if out, ok := fitCoerced(v, t); ok {
			return out, true
		}
	}
	return nil, false
}

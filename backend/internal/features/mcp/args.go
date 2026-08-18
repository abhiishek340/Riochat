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

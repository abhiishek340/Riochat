package mcp

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxRefDepth     = 32
	maxArgumentName = 128
)

// parseSchemaObject decodes a JSON schema document that must be an object.
func parseSchemaObject(text string) (map[string]any, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// attachmentValid reports whether an attachment configuration can bind a tool
// with the given input schema as an automatic image processor.
func attachmentValid(a attachment, schemaJSON string) bool {
	switch a.Mode {
	case AttachNone:
		return a.Argument == "" && a.Encoding == "" && a.Prompt == ""
	case AttachImage:
	default:
		return false
	}
	if a.Argument == "" || utf8.RuneCountInString(a.Argument) > maxArgumentName || utf8.RuneCountInString(a.Prompt) > maxArgumentName {
		return false
	}
	if a.Encoding != EncodingBase64 && a.Encoding != EncodingDataURL {
		return false
	}
	if a.Prompt != "" && a.Prompt == a.Argument {
		return false
	}
	root, ok := parseSchemaObject(schemaJSON)
	if !ok {
		return false
	}
	props, ok := root["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return false
	}
	img, ok := props[a.Argument]
	if !ok || !acceptsString(root, img, 0, nil) {
		return false
	}
	if a.Prompt != "" {
		p, ok := props[a.Prompt]
		if !ok || !acceptsString(root, p, 0, nil) {
			return false
		}
	}
	if req, present := root["required"]; present {
		list, ok := req.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			name, ok := item.(string)
			if !ok || (name != a.Argument && name != a.Prompt) {
				return false
			}
		}
	}
	return true
}

// acceptsString evaluates, structurally, whether a (sub)schema can be satisfied
// by a string value.
func acceptsString(root map[string]any, schema any, depth int, visiting map[string]bool) bool {
	if depth > maxRefDepth {
		return false
	}
	switch s := schema.(type) {
	case bool:
		return s
	case map[string]any:
		return mapAcceptsString(root, s, depth, visiting)
	}
	return false
}

func mapAcceptsString(root, s map[string]any, depth int, visiting map[string]bool) bool {
	if ref, present := s["$ref"]; present {
		str, ok := ref.(string)
		if !ok || str == "" {
			return false
		}
		if visiting[str] {
			return false
		}
		target, ok := resolveLocalRef(root, str)
		if !ok {
			return false
		}
		next := make(map[string]bool, len(visiting)+1)
		for k := range visiting {
			next[k] = true
		}
		next[str] = true
		if !acceptsString(root, target, depth+1, next) {
			return false
		}
	}
	if t, present := s["type"]; present {
		switch tv := t.(type) {
		case string:
			if tv != "string" {
				return false
			}
		case []any:
			found := false
			for _, e := range tv {
				if es, ok := e.(string); ok && es == "string" {
					found = true
				}
			}
			if !found {
				return false
			}
		default:
			return false
		}
	}
	if c, present := s["const"]; present {
		if _, ok := c.(string); !ok {
			return false
		}
	}
	if e, present := s["enum"]; present {
		list, ok := e.([]any)
		if !ok {
			return false
		}
		found := false
		for _, item := range list {
			if _, ok := item.(string); ok {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if v, present := s[key]; present {
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return false
			}
			any1 := false
			for _, b := range list {
				if acceptsString(root, b, depth+1, visiting) {
					any1 = true
					break
				}
			}
			if !any1 {
				return false
			}
		}
	}
	if v, present := s["allOf"]; present {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return false
		}
		for _, b := range list {
			if !acceptsString(root, b, depth+1, visiting) {
				return false
			}
		}
	}
	return true
}

// resolveLocalRef follows "#" or "#/a/b" (JSON pointer, with ~0 ~1 escapes).
func resolveLocalRef(root map[string]any, ref string) (any, bool) {
	if ref == "#" {
		return root, true
	}
	rest, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, false
	}
	var cur any = root
	for _, seg := range strings.Split(rest, "/") {
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

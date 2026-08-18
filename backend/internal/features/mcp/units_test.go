package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func schemaText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestAcceptsString(t *testing.T) {
	cases := []struct {
		name string
		prop string
		root string
		want bool
	}{
		{"no keywords", `{}`, `{}`, true},
		{"description only", `{"description":"x"}`, `{}`, true},
		{"string", `{"type":"string"}`, `{}`, true},
		{"integer", `{"type":"integer"}`, `{}`, false},
		{"type list with string", `{"type":["null","string"]}`, `{}`, true},
		{"type list without", `{"type":["null","integer"]}`, `{}`, false},
		{"const string", `{"const":"a"}`, `{}`, true},
		{"const number", `{"const":1}`, `{}`, false},
		{"enum with string", `{"enum":[1,"a"]}`, `{}`, true},
		{"enum numbers", `{"enum":[1,2]}`, `{}`, false},
		{"enum empty", `{"enum":[]}`, `{}`, false},
		{"anyOf one ok", `{"anyOf":[{"type":"integer"},{"type":"string"}]}`, `{}`, true},
		{"anyOf none", `{"anyOf":[{"type":"integer"}]}`, `{}`, false},
		{"anyOf empty", `{"anyOf":[]}`, `{}`, false},
		{"oneOf ok", `{"oneOf":[{"type":"string"}]}`, `{}`, true},
		{"allOf all", `{"allOf":[{"type":"string"},{"minLength":1}]}`, `{}`, true},
		{"allOf one bad", `{"allOf":[{"type":"string"},{"type":"integer"}]}`, `{}`, false},
		{"allOf empty", `{"allOf":[]}`, `{}`, false},
		{"ref ok", `{"$ref":"#/$defs/s"}`, `{"$defs":{"s":{"type":"string"}}}`, true},
		{"ref with bad sibling", `{"$ref":"#/$defs/s","type":"integer"}`, `{"$defs":{"s":{"type":"string"}}}`, false},
		{"ref to bad", `{"$ref":"#/$defs/s"}`, `{"$defs":{"s":{"type":"integer"}}}`, false},
		{"ref escaped", `{"$ref":"#/$defs/a~1b"}`, `{"$defs":{"a/b":{"type":"string"}}}`, true},
		{"ref escaped tilde", `{"$ref":"#/$defs/a~0b"}`, `{"$defs":{"a~b":{"type":"string"}}}`, true},
		{"remote ref", `{"$ref":"https://x/y.json"}`, `{}`, false},
		{"empty ref", `{"$ref":""}`, `{}`, false},
		{"unresolved ref", `{"$ref":"#/$defs/nope"}`, `{}`, false},
		{"root ref", `{"$ref":"#"}`, `{"type":"string"}`, true},
		{"cycle", `{"$ref":"#/$defs/a"}`, `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}}}`, false},
		{"self cycle", `{"$ref":"#/$defs/a"}`, `{"$defs":{"a":{"$ref":"#/$defs/a"}}}`, false},
		{"array pointer", `{"$ref":"#/anyOf/1"}`, `{"anyOf":[{"type":"integer"},{"type":"string"}]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prop, root any
			_ = json.Unmarshal([]byte(tc.prop), &prop)
			_ = json.Unmarshal([]byte(tc.root), &root)
			if got := acceptsString(root.(map[string]any), prop, 0, nil); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestAcceptsStringDepthCap(t *testing.T) {
	// A chain of 40 distinct references is deeper than the cap of 32.
	defs := map[string]any{}
	for i := 0; i < 40; i++ {
		next := map[string]any{"$ref": "#/$defs/d" + string(rune('a'+i%26)) + string(rune('a'+i/26))}
		defs["d"+string(rune('a'+i%26))+string(rune('a'+i/26))] = next
	}
	last := "d" + string(rune('a'+39%26)) + string(rune('a'+39/26))
	defs[last] = map[string]any{"type": "string"}
	root := map[string]any{"$defs": defs}
	if acceptsString(root, map[string]any{"$ref": "#/$defs/daa"}, 0, nil) {
		t.Fatal("reference depth beyond 32 must be rejected")
	}
	// A short chain still works.
	root = map[string]any{"$defs": map[string]any{"a": map[string]any{"$ref": "#/$defs/b"}, "b": map[string]any{"type": "string"}}}
	if !acceptsString(root, map[string]any{"$ref": "#/$defs/a"}, 0, nil) {
		t.Fatal("short chain must work")
	}
}

func TestAttachmentValid(t *testing.T) {
	good := attachment{Mode: "image", Argument: "image", Encoding: "base64"}
	s := schemaText(imageSchema)
	if !attachmentValid(good, s) {
		t.Fatal("baseline must be valid")
	}
	for name, a := range map[string]attachment{
		"none with leftovers": {Mode: "none", Argument: "image"},
		"unknown mode":        {Mode: "video", Argument: "image", Encoding: "base64"},
		"no argument":         {Mode: "image", Encoding: "base64"},
		"no encoding":         {Mode: "image", Argument: "image"},
		"prompt same":         {Mode: "image", Argument: "image", Encoding: "base64", Prompt: "image"},
	} {
		if attachmentValid(a, s) {
			t.Errorf("%s must be invalid", name)
		}
	}
	if !attachmentValid(attachment{Mode: "none"}, "not json") {
		t.Error("mode none never needs the schema")
	}
	for name, schema := range map[string]string{
		"not json":          `nope`,
		"empty":             ``,
		"array":             `[]`,
		"empty properties":  `{"properties":{}}`,
		"required not list": `{"properties":{"image":{"type":"string"}},"required":"image"}`,
		"required non str":  `{"properties":{"image":{"type":"string"}},"required":[1]}`,
	} {
		if attachmentValid(good, schema) {
			t.Errorf("schema %s must be invalid", name)
		}
	}
	if !attachmentValid(good, `{"properties":{"image":{"type":"string"}}}`) {
		t.Error("no required list is fine")
	}
	if !attachmentValid(good, `{"properties":{"image":{"type":"string"}},"required":[]}`) {
		t.Error("empty required list is fine")
	}
}

func TestPrepareArguments(t *testing.T) {
	schema := schemaText(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count":  map[string]any{"type": "integer"},
			"ratio":  map[string]any{"type": "number"},
			"flag":   map[string]any{"type": "boolean"},
			"name":   map[string]any{"type": "string"},
			"mode":   map[string]any{"enum": []any{"fast", "slow", 3}},
			"either": map[string]any{"type": []any{"integer", "string"}},
			"list":   map[string]any{"type": "array"},
			"opt":    map[string]any{"type": "object"},
		},
		"required": []any{"name"},
	})
	ok := []struct{ name, in, want string }{
		{"count text", `{"name":"a","count":"3"}`, `{"count":3,"name":"a"}`},
		{"count whole float", `{"name":"a","count":3.0}`, `{"count":3,"name":"a"}`},
		{"count float text", `{"name":"a","count":"3.0"}`, `{"count":3,"name":"a"}`},
		{"ratio text", `{"name":"a","ratio":"0.5"}`, `{"name":"a","ratio":0.5}`},
		{"ratio int", `{"name":"a","ratio":2}`, `{"name":"a","ratio":2}`},
		{"flag text", `{"name":"a","flag":" TRUE "}`, `{"flag":true,"name":"a"}`},
		{"flag false", `{"name":"a","flag":"false"}`, `{"flag":false,"name":"a"}`},
		{"enum number match", `{"name":"a","mode":3.0}`, `{"mode":3,"name":"a"}`},
		{"enum string", `{"name":"a","mode":"fast"}`, `{"mode":"fast","name":"a"}`},
		{"union keeps string", `{"name":"a","either":"abc"}`, `{"either":"abc","name":"a"}`},
		{"union exact first", `{"name":"a","either":"7"}`, `{"either":"7","name":"a"}`},
		{"unknown property kept", `{"name":"a","extra":{"k":[1,2.5]}}`, `{"extra":{"k":[1,2.5]},"name":"a"}`},
		{"html not escaped", `{"name":"<b>&"}`, `{"name":"<b>&"}`},
		{"big ints keep exactness", `{"name":"a","count":9007199254740993}`, `{"count":9007199254740993,"name":"a"}`},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := prepareArguments(tc.in, schema)
			if err != nil || got != tc.want {
				t.Fatalf("got %q err %v, want %q", got, err, tc.want)
			}
		})
	}
	bad := []struct{ name, in, mentions string }{
		{"count x", `{"name":"a","count":"x"}`, "count: expected integer, received string"},
		{"count fraction", `{"name":"a","count":1.5}`, "count: expected integer, received number"},
		{"flag maybe", `{"name":"a","flag":"maybe"}`, "flag: expected boolean"},
		{"flag number", `{"name":"a","flag":1}`, "flag: expected boolean, received integer"},
		{"name number", `{"name":5}`, "name: expected string, received integer"},
		{"missing required", `{}`, "name: required"},
		{"enum miss", `{"name":"a","mode":"medium"}`, "mode: value is not one of"},
		{"list wrong", `{"name":"a","list":"x"}`, "list: expected array, received string"},
		{"object wrong", `{"name":"a","opt":[1]}`, "opt: expected object, received array"},
		{"two problems", `{"count":"x","flag":"y"}`, "count: expected integer"},
		{"trailing data", `{"name":"a"} {"x":1}`, "trailing data"},
		{"not object", `[1]`, "must be a JSON object"},
		{"not json", `{`, "valid JSON object"},
		{"scalar", `5`, "must be a JSON object"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareArguments(tc.in, schema)
			var ae *ArgumentError
			if !errors.As(err, &ae) {
				t.Fatalf("got %v", err)
			}
			if !strings.HasPrefix(err.Error(), "argument validation failed: ") || !strings.Contains(err.Error(), tc.mentions) {
				t.Fatalf("message %q lacks %q", err.Error(), tc.mentions)
			}
		})
	}
	_, err := prepareArguments(`{"count":"x","flag":"y","name":1}`, schema)
	for _, p := range []string{"count:", "flag:", "name:"} {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("every offending parameter is named, missing %s in %q", p, err)
		}
	}
	if got, err := prepareArguments("  ", schema); err == nil {
		t.Fatalf("empty arguments still miss the required name: %q", got)
	}
	if got, err := prepareArguments("", `{"type":"object","properties":{}}`); err != nil || got != "{}" {
		t.Fatalf("empty text is {}: %q %v", got, err)
	}
	if got, err := prepareArguments(`{"b":1,"a":2}`, `not a schema`); err != nil || got != `{"a":2,"b":1}` {
		t.Fatalf("an unusable schema only enforces the shape: %q %v", got, err)
	}
	if got, err := prepareArguments(`{"a":"1"}`, `{"type":"object"}`); err != nil || got != `{"a":"1"}` {
		t.Fatalf("a schema without properties: %q %v", got, err)
	}
}

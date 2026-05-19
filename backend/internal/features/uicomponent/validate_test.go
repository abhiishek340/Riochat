package uicomponent

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	good := []string{"a", "my-chart2", "a1", "a-b-c", "a1-2", "chart", strings.Repeat("a", 64)}
	bad := []string{"", "Bad", "a--b", "-a", "a-", "2a", "a_b", "a b", "a.b", "Aa", "a-B", "é", "a\n",
		strings.Repeat("a", 65), "panel", "widget", "row", "stack", "grid", "card", "tabs", "collapse", "divider",
		"text", "stat", "badge", "progress", "callout", "formula", "scene", "slider", "select", "segmented",
		"switch", "input", "button"}
	for _, n := range good {
		if !validName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range bad {
		if validName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestPrimitivesCount(t *testing.T) {
	if len(primitives) != 21 {
		t.Fatalf("primitives: %d", len(primitives))
	}
}

func TestValidPropsSchema(t *testing.T) {
	for _, ok := range []string{"", `{}`, `{"type":"object"}`, `{"a":[1,2,{"b":null}]}`} {
		if !validPropsSchema(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"[1]", "[]", "null", `"s"`, "1", "true", "not json", `{"a":`, `{} {}`, `{"a":1}x`} {
		if validPropsSchema(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestNormalizeInput(t *testing.T) {
	ok := rawInput{Name: " n ", Description: " d ", PropsSummary: " p ", PropsSchema: " {} ", RendererSource: " s "}
	got, err := normalizeInput(ok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "n" || got.Description != "d" || got.PropsSummary != "p" || got.PropsSchema != "{}" || got.RendererSource != " s " || got.Version != 1 {
		t.Fatalf("%+v", got)
	}
	for i, mutate := range []func(*rawInput){
		func(r *rawInput) { r.Name = "A" },
		func(r *rawInput) { r.Description = " " },
		func(r *rawInput) { r.PropsSummary = "" },
		func(r *rawInput) { r.PropsSchema = "[]" },
		func(r *rawInput) { r.RendererSource = "\n " },
		func(r *rawInput) { r.Description = strings.Repeat("x", 257) },
		func(r *rawInput) { r.RendererSource = strings.Repeat("x", maxRendererSize+1) },
	} {
		in := ok
		mutate(&in)
		if _, err := normalizeInput(in); err != ErrInvalid {
			t.Errorf("case %d: err %v", i, err)
		}
	}
}

func TestNormalizePatchKeepsAbsence(t *testing.T) {
	got, err := normalizePatch(rawPatch{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != nil || got.Version != nil || got.Description != nil || got.PropsSummary != nil ||
		got.PropsSchema != nil || got.RendererSource != nil || got.Enabled != nil || got.SortOrder != nil {
		t.Fatalf("%+v", got)
	}
	empty := ""
	if got, err := normalizePatch(rawPatch{PropsSchema: &empty}); err != nil || got.PropsSchema == nil || *got.PropsSchema != "" {
		t.Fatalf("empty schema patch: %+v %v", got, err)
	}
	zero := 0
	if _, err := normalizePatch(rawPatch{Version: &zero}); err != ErrInvalid {
		t.Fatalf("version 0: %v", err)
	}
	if !(rawPatch{Name: &empty}).touchesContent() || (rawPatch{Enabled: new(bool), SortOrder: &zero}).touchesContent() {
		t.Fatal("touchesContent")
	}
}

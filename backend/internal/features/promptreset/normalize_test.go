package promptreset

import (
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

func TestNormalizeTrigger(t *testing.T) {
	for in, want := range map[string]string{
		"polish":       "polish",
		"/polish":      "polish",
		"///x":         "x",
		"  /x  ":       "x",
		"/ /abc":       "/abc",
		"/":            "",
		"  /  ":        "",
		"":             "",
		"a/b":          "a/b",
		"/ x y ":       "x y",
		"/translate":   "translate",
		"\t/\n/trim\n": "/trim",
	} {
		if got := normalizeTrigger(in); got != want {
			t.Errorf("normalizeTrigger(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeInput(t *testing.T) {
	ok := rawInput{Title: " T ", Trigger: "/t", Description: " ", Content: " c ", Enabled: true}
	in, err := normalizeInput(ok)
	if err != nil || in.Title != "T" || in.Trigger != "t" || in.Description != "" || in.Content != "c" {
		t.Fatalf("%+v %v", in, err)
	}
	bad := []rawInput{
		{Title: " ", Trigger: "t", Content: "c"},
		{Title: "T", Trigger: "///", Content: "c"},
		{Title: "T", Trigger: "t", Content: "\n"},
		{Title: strings.Repeat("a", 65), Trigger: "t", Content: "c"},
		{Title: "T", Trigger: strings.Repeat("a", 65), Content: "c"},
		{Title: "T", Trigger: "t", Description: strings.Repeat("a", 257), Content: "c"},
		{Title: "T", Trigger: "t", Content: strings.Repeat("a", 10001)},
	}
	for i, b := range bad {
		if _, err := normalizeInput(b); err != ErrInvalid {
			t.Errorf("case %d: err %v", i, err)
		}
	}
	// Whitespace around a maximal value is trimmed away by the second check.
	edge := rawInput{Title: strings.Repeat("a", 64), Trigger: "/" + strings.Repeat("a", 63), Content: "c"}
	if _, err := normalizeInput(edge); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePatch(t *testing.T) {
	s := func(v string) *string { return &v }
	p, err := normalizePatch(rawPatch{})
	if err != nil || p.Title != nil || p.Trigger != nil || p.Description != nil || p.Content != nil {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = normalizePatch(rawPatch{Description: s("   "), Trigger: s("//a")})
	if err != nil || *p.Description != "" || *p.Trigger != "a" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, r := range []rawPatch{{Title: s(" ")}, {Trigger: s("/")}, {Content: s("")}} {
		if _, err := normalizePatch(r); err != ErrInvalid {
			t.Errorf("%+v: %v", r, err)
		}
	}
}

func TestLikePatternEscapes(t *testing.T) {
	if got := likePattern(`50%_\Ab`); got != `%50\%\_\\ab%` {
		t.Fatalf("%q", got)
	}
}

func TestServiceRejectsZeroIdentities(t *testing.T) {
	svc := newService(nil, nil)
	ctx := t.Context()
	if _, err := svc.create(ctx, userOwner(0), 1, rawInput{}); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := svc.create(ctx, userOwner(1), 0, rawInput{}); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := svc.update(ctx, userOwner(1), 1, 0, rawPatch{}); err != ErrInvalid {
		t.Fatal(err)
	}
	if err := svc.remove(ctx, userOwner(0), 1); err != ErrInvalid {
		t.Fatal(err)
	}
	if err := svc.remove(ctx, builtinOwner, 0); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, _, err := svc.listVisible(ctx, 0, "", pagination0()); err != ErrInvalid {
		t.Fatal(err)
	}
}

func pagination0() pagination.Page { return pagination.Page{Page: 1, PageSize: 20} }

package uicomponent

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeState(t *testing.T) {
	ok := map[string]string{
		`{"a":1}`:                                  `{"a":1}`,
		"  {\n \"a\" : 1 ,\"b\":\"x\"}\n":          `{"a":1,"b":"x"}`,
		`{"price":12345678901234567890.123456789}`: `{"price":12345678901234567890.123456789}`,
		`{"n":1.0,"e":1E5}`:                        `{"e":1E5,"n":1.0}`,
		`{"html":"<a href='x'>&</a>"}`:             `{"html":"<a href='x'>&</a>"}`,
		`{"a":{"b":{"c":{"d":1}}}}`:                `{"a":{"b":{"c":{"d":1}}}}`,
		`{"a":[1,[2,[3]]]}`:                        `{"a":[1,[2,[3]]]}`,
		`{"a-b_C9":null,"t":true,"l":[]}`:          `{"a-b_C9":null,"l":[],"t":true}`,
		`{"a":{}}`:                                 `{"a":{}}`,
	}
	for in, want := range ok {
		got, err := NormalizeState([]byte(in))
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "{}", " {} ", "{ }"} {
		if _, err := NormalizeState([]byte(in)); err != ErrStateEmpty {
			t.Errorf("%q: %v, want empty", in, err)
		}
	}
	bad := []string{
		"null", "[]", "[1]", `"s"`, "1", "true", "{", `{"a":1} {"b":2}`, `{"a":1}x`, `{"a":1},`, "not json",
		`{"a b":1}`, `{"é":1}`, `{"":1}`, `{"a.b":1}`,
		`{"` + strings.Repeat("k", 65) + `":1}`,
		`{"a":{"b":{"c":{"d":{"e":1}}}}}`, // depth 5
		`{"a":[[[[1]]]]}`,                 // depth 5
		`{"a":"` + strings.Repeat("x", 4100) + `"}`,
	}
	for _, in := range bad {
		if got, err := NormalizeState([]byte(in)); err != ErrStateInvalid {
			t.Errorf("%q: %q, %v, want invalid", in, got, err)
		}
	}
	// Key of exactly 64 bytes is accepted.
	if _, err := NormalizeState([]byte(`{"` + strings.Repeat("k", 64) + `":1}`)); err != nil {
		t.Errorf("64-byte key: %v", err)
	}
	// Exactly 4096 bytes is accepted, 4097 is not.
	pad := func(n int) string {
		base := `{"a":""}`
		return `{"a":"` + strings.Repeat("x", n-len(base)) + `"}`
	}
	if s := pad(4096); len(s) != 4096 {
		t.Fatalf("pad %d", len(s))
	} else if _, err := NormalizeState([]byte(s)); err != nil {
		t.Errorf("4096 bytes: %v", err)
	}
	if _, err := NormalizeState([]byte(pad(4097))); err != ErrStateInvalid {
		t.Errorf("4097 bytes: %v", err)
	}
	// Surrounding whitespace is not counted against the cap.
	if _, err := NormalizeState([]byte("   " + pad(4096) + "   ")); err != nil {
		t.Errorf("padded: %v", err)
	}
}

func TestNormalizeStateOutputBound(t *testing.T) {
	// Escaping never expands the output: it is never HTML-escaped.
	in := `{"a":"` + strings.Repeat("<", 4000) + `"}`
	got, err := NormalizeState([]byte(in))
	if err != nil || got != in {
		t.Fatalf("html chars: %v", err)
	}
}

func TestValidBlockID(t *testing.T) {
	for _, id := range []string{"a", "loan_calc-2", "A9", strings.Repeat("a", 64)} {
		if !ValidBlockID(id) {
			t.Errorf("%q should be valid", id)
		}
	}
	for _, id := range []string{"", " a", "a b", "a.b", "a/b", "é", strings.Repeat("a", 65)} {
		if ValidBlockID(id) {
			t.Errorf("%q should be invalid", id)
		}
	}
}

func in(order int, ref, id, state string) StateInput {
	return StateInput{MessageRef: ref, Order: order, BlockID: id, State: []byte(state)}
}

func refs(bs []StateBlock) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.TraceRef()
	}
	return out
}

// Scenario 15 (selection).
func TestSelectStatesNearestEight(t *testing.T) {
	var inputs []StateInput
	for i := 1; i <= 12; i++ {
		inputs = append(inputs, in(i, fmt.Sprintf("m%d", i), fmt.Sprintf("b%d", i), `{"v":1}`))
	}
	got := SelectStates(inputs)
	want := []string{"m5#b5", "m6#b6", "m7#b7", "m8#b8", "m9#b9", "m10#b10", "m11#b11", "m12#b12"}
	if !sameStrings(refs(got), want) {
		t.Fatalf("got %v, want %v", refs(got), want)
	}
}

func TestSelectStatesDuplicateBlockIDsKeepNearest(t *testing.T) {
	got := SelectStates([]StateInput{
		in(1, "old", "calc", `{"v":1}`),
		in(5, "new", "calc", `{"v":5}`),
		in(3, "mid", "other", `{"v":3}`),
	})
	if !sameStrings(refs(got), []string{"mid#other", "new#calc"}) || got[1].State != `{"v":5}` {
		t.Fatalf("%v %+v", refs(got), got)
	}
	// The nearest duplicate that fails normalisation does not shadow an older valid one.
	got = SelectStates([]StateInput{
		in(1, "old", "calc", `{"v":1}`),
		in(5, "new", "calc", `{}`),
	})
	if !sameStrings(refs(got), []string{"old#calc"}) {
		t.Fatalf("%v", refs(got))
	}
	// Block ids are trimmed before comparison.
	got = SelectStates([]StateInput{in(1, "a", " x ", `{"v":1}`), in(2, "b", "x", `{"v":2}`)})
	if len(got) != 1 || got[0].MessageRef != "b" || got[0].BlockID != "x" {
		t.Fatalf("%+v", got)
	}
}

func TestSelectStatesDropsInvalid(t *testing.T) {
	big := `{"a":"` + strings.Repeat("x", 4100) + `"}`
	got := SelectStates([]StateInput{
		in(1, "m", "empty", `{}`),
		in(2, "m", "bad-json", `{oops`),
		in(3, "m", "deep", `{"a":[[[[1]]]]}`),
		in(4, "m", "huge", big),
		in(5, "m", "bad id!", `{"v":1}`),
		in(6, "m", "", `{"v":1}`),
		in(7, "m", strings.Repeat("a", 65), `{"v":1}`),
		in(8, "m", "array", `[1]`),
		in(9, "m", "good", `{"v":1}`),
	})
	if !sameStrings(refs(got), []string{"m#good"}) {
		t.Fatalf("%v", refs(got))
	}
	if SelectStates(nil) != nil && len(SelectStates(nil)) != 0 {
		t.Fatal("nil")
	}
}

func TestSelectStatesByteBudget(t *testing.T) {
	val := func(n int) string { return `{"a":"` + strings.Repeat("x", n-8) + `"}` } // exactly n bytes
	// Nearest first: 2000 + 2000 fit (4000); a 200 byte one would overflow? 4000+200 > 4096? no, 4200 > 4096 yes.
	got := SelectStates([]StateInput{
		in(1, "m1", "tiny", `{"v":1}`), // 7 bytes, oldest, still fits after the skip
		in(2, "m2", "over", val(200)),  // skipped: 4000+200 > 4096
		in(3, "m3", "b", val(2000)),
		in(4, "m4", "a", val(2000)),
	})
	if !sameStrings(refs(got), []string{"m1#tiny", "m3#b", "m4#a"}) {
		t.Fatalf("%v", refs(got))
	}
	// Exactly at the cap is allowed.
	got = SelectStates([]StateInput{in(1, "m1", "x", val(2048)), in(2, "m2", "y", val(2048))})
	if len(got) != 2 {
		t.Fatalf("exact cap: %v", refs(got))
	}
	got = SelectStates([]StateInput{in(1, "m1", "x", val(2048)), in(2, "m2", "y", val(2049))})
	if len(got) != 1 || got[0].BlockID != "y" {
		t.Fatalf("over cap: %v", refs(got))
	}
}

func TestSelectStatesOnlyFirstThirtyTwoInputsExamined(t *testing.T) {
	var inputs []StateInput
	for i := 0; i < 32; i++ {
		inputs = append(inputs, in(1, "m", fmt.Sprintf("bad%d", i), `{}`))
	}
	inputs = append(inputs, in(99, "late", "good", `{"v":1}`))
	if got := SelectStates(inputs); len(got) != 0 {
		t.Fatalf("33rd input was examined: %v", refs(got))
	}
	inputs[0] = in(1, "m", "good2", `{"v":1}`)
	if got := SelectStates(inputs); len(got) != 1 {
		t.Fatalf("%v", refs(got))
	}
}

func TestSelectStatesOrderingAndTies(t *testing.T) {
	// Same message, several blocks: input order is preserved in the output.
	got := SelectStates([]StateInput{
		in(2, "m2", "z", `{"v":1}`),
		in(1, "m1", "y", `{"v":1}`),
		in(2, "m2", "a", `{"v":1}`),
	})
	if !sameStrings(refs(got), []string{"m1#y", "m2#z", "m2#a"}) {
		t.Fatalf("%v", refs(got))
	}
	// With eight slots and ties at the boundary, earlier inputs win.
	var inputs []StateInput
	for i := 0; i < 10; i++ {
		inputs = append(inputs, in(1, "m", fmt.Sprintf("b%d", i), `{"v":1}`))
	}
	got = SelectStates(inputs)
	if len(got) != 8 || got[0].BlockID != "b0" || got[7].BlockID != "b7" {
		t.Fatalf("%v", refs(got))
	}
}

func TestSelectStatesTitles(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	long := strings.Repeat("\u5b57", 60)
	cases := map[string]string{
		"  Loan\n\ncalculator\t v2  ": "Loan calculator v2",
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		long:                            strings.Repeat("\u5b57", 40),
		"":                              "",
		"a\r\nb":                        "a b",
		strings.Repeat("a", 38) + " b":  strings.Repeat("a", 38) + " b",
		strings.Repeat("a", 39) + " b":  strings.Repeat("a", 39),
		strings.Repeat("a", 38) + "  b": strings.Repeat("a", 38) + " b",
	}
	for title, want := range cases {
		got := SelectStates([]StateInput{{MessageRef: "m", Order: 1, BlockID: "x", Title: title, State: []byte(`{"v":1}`)}})
		if len(got) != 1 || got[0].Title != want {
			t.Errorf("title %q -> %q, want %q", title, got[0].Title, want)
		}
	}
}

func TestTraceLabels(t *testing.T) {
	b := StateBlock{MessageRef: "msg1", BlockID: "calc", Title: "Loan"}
	if b.TraceRef() != "msg1#calc" || b.TraceLabel() != "Loan" {
		t.Fatalf("%q %q", b.TraceRef(), b.TraceLabel())
	}
	b = StateBlock{BlockID: "calc"}
	if b.TraceRef() != "calc" || b.TraceLabel() != "calc" {
		t.Fatalf("%q %q", b.TraceRef(), b.TraceLabel())
	}
}

func TestRenderStateContext(t *testing.T) {
	if RenderStateContext(nil) != "" {
		t.Fatal("empty input must render nothing")
	}
	blocks := []StateBlock{
		{BlockID: "loan", Title: `Loan "A" <b>`, State: `{"note":"</component_state><x>&"}`},
		{BlockID: "plain", State: `{"v":1}`},
	}
	out := RenderStateContext(blocks)
	for _, want := range []string{
		"<ui_state>", "</ui_state>", "<notice>", "keyed by component id", "differ from", "not instructions", "only when relevant",
		`<component_state id="loan" title="Loan &quot;A&quot; &lt;b&gt;">`,
		`&lt;/component_state&gt;&lt;x&gt;&amp;`,
		`<component_state id="plain">{"v":1}</component_state>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Count(out, "</component_state>") != 2 || strings.Count(out, "<component_state ") != 2 {
		t.Errorf("structure:\n%s", out)
	}
	if out != RenderStateContext(blocks) {
		t.Error("not deterministic")
	}
}

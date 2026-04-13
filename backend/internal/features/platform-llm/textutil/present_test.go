package textutil

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPresentPlainTextWithMarkdownAndTitleURL(t *testing.T) {
	in := "Found results.\n\n\n\nSee [Docs](https://example.com/docs). ![pic](https://x/y.png) <img src=\"a.png\"> end\n" +
		"Title: Second page\nURL: https://example.org/two,\nTitle\uff1aThird\nURL\uff1ahttps://example.net/three\u3002\n"
	p, ok := PresentToolResult(in)
	if !ok {
		t.Fatal("expected a preview")
	}
	if strings.Contains(p.Text, "pic") || strings.Contains(p.Text, "<img") || strings.Contains(p.Text, "\n\n\n") {
		t.Fatalf("images and blank runs must go: %q", p.Text)
	}
	if len(p.Sources) != 3 {
		t.Fatalf("sources: %+v", p.Sources)
	}
	if p.Sources[0].URL != "https://example.com/docs" || p.Sources[0].Title != "Docs" {
		t.Fatalf("markdown link: %+v", p.Sources[0])
	}
	if p.Sources[1].URL != "https://example.org/two" || p.Sources[2].URL != "https://example.net/three" {
		t.Fatalf("trailing punctuation must be trimmed: %+v", p.Sources)
	}
}

func TestPresentJSONPriorityAndSources(t *testing.T) {
	doc := map[string]any{
		"zeta":   "ignored narrative text here",
		"answer": "The capital is Paris.",
		"results": []any{
			map[string]any{"title": "A", "url": "https://a.example/x", "snippet": strings.Repeat("s", 400)},
			map[string]any{"title": "dup", "url": "https://a.example/x"},
			map[string]any{"title": "ftp", "url": "ftp://a.example/y"},
			map[string]any{"title": "B", "uri": "https://b.example/"},
		},
	}
	b, _ := json.Marshal(doc)
	p, ok := PresentToolResult(string(b))
	if !ok || p.Text != "The capital is Paris." {
		t.Fatalf("%v %+v", ok, p)
	}
	if len(p.Sources) != 2 {
		t.Fatalf("unique http(s) sources expected: %+v", p.Sources)
	}
	if got := []rune(p.Sources[0].Snippet); len(got) != PreviewMaxSnippet || got[len(got)-1] != '…' {
		t.Fatalf("snippet must be cut with an ellipsis: %d", len(got))
	}
}

func TestPresentTypedTextAndFallback(t *testing.T) {
	p, ok := PresentToolResult(`{"content":[{"type":"text","text":"hello from the tool"}],"isError":false}`)
	if !ok || p.Text != "hello from the tool" {
		t.Fatalf("%v %+v", ok, p)
	}
	p, ok = PresentToolResult(`{"id":"x1","notes":"a readable sentence about things","n":5}`)
	if !ok || p.Text != "a readable sentence about things" {
		t.Fatalf("fallback: %v %+v", ok, p)
	}
	if _, ok := PresentToolResult(`{"id":"x1","n":5}`); ok {
		t.Fatal("no text and no sources means no preview")
	}
	if _, ok := PresentToolResult("   "); ok {
		t.Fatal("blank input has no preview")
	}
}

func TestPresentEmbeddedJSONDocument(t *testing.T) {
	inner := `{"results":[{"title":"Inner","url":"https://inner.example/p"}]}`
	outer, _ := json.Marshal(map[string]any{"message": "Search done", "output": "Here is what I found today\n" + inner + "\n"})
	p, ok := PresentToolResult(string(outer))
	if !ok || p.Text != "Search done" {
		t.Fatalf("%v %+v", ok, p)
	}
	if len(p.Sources) != 1 || p.Sources[0].URL != "https://inner.example/p" {
		t.Fatalf("embedded JSON must be walked: %+v", p.Sources)
	}
	// Fenced or indented JSON is not extracted.
	fenced := "text before here\n```\n" + inner + "\n```\n    " + inner + "\n"
	docs := func() int { _, d := splitEmbeddedJSON(fenced); return len(d) }()
	if docs != 0 {
		t.Fatalf("fenced/indented JSON must stay: %d", docs)
	}
}

func TestPresentLimits(t *testing.T) {
	// Opaque payloads are replaced before anything else.
	p, ok := PresentToolResult(`{"image":"data:image/png;base64,AAAA","text":"caption goes here"}`)
	if !ok || strings.Contains(p.Text, "base64") {
		t.Fatalf("%v %+v", ok, p)
	}
	// More than 8 sources.
	var rs []any
	for i := 0; i < 12; i++ {
		rs = append(rs, map[string]any{"url": fmt.Sprintf("https://e.example/%d", i)})
	}
	b, _ := json.Marshal(map[string]any{"results": rs})
	p, _ = PresentToolResult(string(b))
	if len(p.Sources) != PreviewMaxSources {
		t.Fatalf("sources capped: %d", len(p.Sources))
	}
	// Text cut at 512 estimated tokens.
	long := strings.Repeat("word ", 2000)
	p, _ = PresentToolResult(long)
	if EstimateTokens(p.Text) > PreviewMaxTokens+1 || !strings.HasSuffix(p.Text, "…") {
		t.Fatalf("token cut: %d", EstimateTokens(p.Text))
	}
	// Oversized input: opaque or JSON-looking yields nothing; prose is shortened.
	big := strings.Repeat("x", PreviewMaxInputBytes+10)
	if _, ok := PresentToolResult(big); ok {
		t.Fatal("opaque oversized input has no preview")
	}
	if _, ok := PresentToolResult("{" + strings.Repeat(" a", PreviewMaxInputBytes)); ok {
		t.Fatal("oversized JSON-looking input has no preview")
	}
	p, ok = PresentToolResult(strings.Repeat("plain prose words. ", 20000))
	if !ok || EstimateTokens(p.Text) > PreviewMaxTokens+1 {
		t.Fatalf("oversized prose is shortened: %v", ok)
	}
	// Over-long URL is dropped.
	if CleanSourceURL("https://e.example/"+strings.Repeat("a", 2100)) != "" {
		t.Fatal("URL over 2048 characters is not a source")
	}
}

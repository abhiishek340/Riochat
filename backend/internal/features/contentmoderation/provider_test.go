package contentmoderation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// ---- hit decision -------------------------------------------------------------

func TestHitDecision(t *testing.T) {
	t.Run("flagged without selected categories is not a hit", func(t *testing.T) {
		r := hitResp("m", "violence")
		r.Results[0].Categories["violence"] = false
		r.Results[0].Categories["hate"] = false
		r.Results[0].Flagged = true
		if dec := decideHit(r, []string{"violence", "hate"}, ModalityText); dec.Hit {
			t.Fatalf("flagged flag must be ignored: %+v", dec)
		}
	})
	t.Run("unselected true category is ignored", func(t *testing.T) {
		r := hitResp("m", "hate")
		if dec := decideHit(r, []string{"violence"}, ModalityText); dec.Hit {
			t.Fatal("unselected category counted")
		}
	})
	t.Run("applied types exclude other modality", func(t *testing.T) {
		r := hitResp("m", "sexual")
		r.Results[0].AppliedInputTypes = map[string][]string{"sexual": {"text"}}
		if dec := decideHit(r, []string{"sexual"}, ModalityImage); dec.Hit {
			t.Fatal("text-only category must not hit an image check")
		}
		if dec := decideHit(r, []string{"sexual"}, ModalityText); !dec.Hit {
			t.Fatal("applies to text")
		}
	})
	t.Run("missing or empty applied types apply", func(t *testing.T) {
		r := hitResp("m", "sexual")
		if !decideHit(r, []string{"sexual"}, ModalityImage).Hit {
			t.Fatal("missing info applies")
		}
		r.Results[0].AppliedInputTypes = map[string][]string{"sexual": {}}
		if !decideHit(r, []string{"sexual"}, ModalityImage).Hit {
			t.Fatal("empty list applies")
		}
		r.Results[0].AppliedInputTypes = map[string][]string{"sexual": {"TEXT", " Image "}}
		if !decideHit(r, []string{"sexual"}, ModalityImage).Hit {
			t.Fatal("match is case-insensitive")
		}
	})
	t.Run("categories sorted, deduplicated, first score wins", func(t *testing.T) {
		r := &Response{Results: []ModerationResult{
			{Categories: map[string]bool{"violence": true, "hate": true}, CategoryScores: map[string]float64{"violence": 0.4, "hate": 0.8}},
			{Categories: map[string]bool{"violence": true, "illicit": true}, CategoryScores: map[string]float64{"violence": 0.99, "illicit": 0.5}},
		}}
		dec := decideHit(r, []string{"violence", "illicit", "hate"}, ModalityText)
		if !reflect.DeepEqual(dec.Categories, []string{"hate", "illicit", "violence"}) {
			t.Fatalf("categories %v", dec.Categories)
		}
		if dec.Scores["violence"] != 0.4 || dec.Scores["illicit"] != 0.5 {
			t.Fatalf("scores %v", dec.Scores)
		}
		if dec.FirstResult != 0 {
			t.Fatalf("first result %d", dec.FirstResult)
		}
	})
	t.Run("empty selection and nil response", func(t *testing.T) {
		if decideHit(hitResp("m", "hate"), nil, ModalityText).Hit {
			t.Fatal("empty selection hits")
		}
		if decideHit(nil, []string{"hate"}, ModalityText).Hit {
			t.Fatal("nil response hits")
		}
	})
}

// ---- chunking ---------------------------------------------------------------------

func TestSplitTextKeepsUTF8AndLimit(t *testing.T) {
	// 3-byte characters straddle every 12 KiB boundary, plus 4-byte ones.
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	unit := "\u6f22\u5b57🙂ab"
	text := strings.Repeat(unit, 30*1024/len(unit)+1)
	chunks := splitText(text, maxTextChunkBytes)
	if len(chunks) < 3 {
		t.Fatalf("chunks: %d", len(chunks))
	}
	var joined strings.Builder
	for i, c := range chunks {
		if len(c) > maxTextChunkBytes {
			t.Fatalf("chunk %d has %d bytes", i, len(c))
		}
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is not valid UTF-8", i)
		}
		joined.WriteString(c)
	}
	if joined.String() != strings.TrimSpace(text) {
		t.Fatal("chunks do not reassemble to the text")
	}
}

func TestSplitTextEdgeCases(t *testing.T) {
	if got := splitText("   \n\t ", 100); got != nil {
		t.Fatalf("blank: %v", got)
	}
	if got := splitText("  hi  ", 100); !reflect.DeepEqual(got, []string{"hi"}) {
		t.Fatalf("trim: %v", got)
	}
	exact := strings.Repeat("a", maxTextChunkBytes)
	if got := splitText(exact, maxTextChunkBytes); len(got) != 1 {
		t.Fatalf("exact fit: %d", len(got))
	}
	if got := splitText(exact+"b", maxTextChunkBytes); len(got) != 2 || got[1] != "b" {
		t.Fatalf("one over: %v", len(got))
	}
}

// ---- HTTP client --------------------------------------------------------------------

type providerRig struct {
	p      *httpProvider
	mu     sync.Mutex
	bodies []map[string]any
	hdrs   []http.Header
	sleeps []time.Duration
	hits   int
}

func newRig(handler func(n int, w http.ResponseWriter, r *http.Request)) *providerRig {
	rig := &providerRig{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		rig.mu.Lock()
		rig.hits++
		n := rig.hits
		rig.bodies = append(rig.bodies, body)
		rig.hdrs = append(rig.hdrs, r.Header.Clone())
		rig.mu.Unlock()
		handler(n, w, r)
	})
	rig.p = newHTTPProvider(handlerDoer{h}, func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	rig.p.sleep = func(ctx context.Context, d time.Duration) error {
		rig.mu.Lock()
		rig.sleeps = append(rig.sleeps, d)
		rig.mu.Unlock()
		return ctx.Err()
	}
	return rig
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func okBody(model string, hit ...string) map[string]any {
	cats := map[string]bool{"violence": false, "hate": false}
	for _, h := range hit {
		cats[h] = true
	}
	return map[string]any{"id": "modr-1", "model": model, "results": []any{map[string]any{
		"flagged": len(hit) > 0, "categories": cats, "category_scores": map[string]float64{"violence": 0.1},
	}}}
}

var testCC = ClientConfig{BaseURL: "https://h.example", APIKey: "sk-secret", Model: "m1", Timeout: 5 * time.Second}

func TestRequestShape(t *testing.T) {
	rig := newRig(func(_ int, w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "https://h.example/v1/moderations" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		writeJSON(w, okBody("m1-actual"))
	})
	resp, err := rig.p.CheckText(t.Context(), testCC, "  hello  ", Scope{Modality: ModalityText, Selected: []string{"violence"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "m1-actual" || len(resp.Results) != 1 {
		t.Fatalf("resp %+v", resp)
	}
	h := rig.hdrs[0]
	if h.Get("Authorization") != "Bearer sk-secret" || h.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", h)
	}
	body := rig.bodies[0]
	if body["model"] != "m1" {
		t.Fatalf("model %v", body["model"])
	}
	input := body["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["type"] != "text" || input[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("input %v", input)
	}
}

func TestNoAuthorizationWithoutKeyAndDefaultModel(t *testing.T) {
	rig := newRig(func(_ int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("")) })
	cc := testCC
	cc.APIKey, cc.Model = "  ", " "
	if _, err := rig.p.CheckText(t.Context(), cc, "x", Scope{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rig.hdrs[0]["Authorization"]; ok {
		t.Fatal("Authorization sent without a key")
	}
	if rig.bodies[0]["model"] != "omni-moderation-latest" {
		t.Fatalf("model %v", rig.bodies[0]["model"])
	}
}

func TestImageRequestShape(t *testing.T) {
	rig := newRig(func(_ int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m")) })
	imgs := []Image{{Data: []byte("abc"), MIME: ""}, {Data: nil, MIME: "image/png"}, {Data: []byte("xyz"), MIME: "image/jpeg"}}
	if _, err := rig.p.CheckImages(t.Context(), testCC, imgs, Scope{Modality: ModalityImage, Selected: []string{"sexual"}}); err != nil {
		t.Fatal(err)
	}
	if len(rig.bodies) != 1 {
		t.Fatalf("requests: %d", len(rig.bodies))
	}
	input := rig.bodies[0]["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("empty image must be skipped: %v", input)
	}
	first := input[0].(map[string]any)
	if first["type"] != "image_url" || first["image_url"].(map[string]any)["url"] != "data:image/png;base64,YWJj" {
		t.Fatalf("first: %v", first)
	}
	if input[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/jpeg;base64,eHl6" {
		t.Fatalf("second: %v", input[1])
	}
}

func TestEmptyInputsSendNothing(t *testing.T) {
	rig := newRig(func(_ int, w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
	resp, err := rig.p.CheckText(t.Context(), testCC, " \n ", Scope{})
	if err != nil || resp == nil || len(resp.Results) != 0 {
		t.Fatalf("text: %v %v", resp, err)
	}
	resp, err = rig.p.CheckImages(t.Context(), testCC, []Image{{}}, Scope{})
	if err != nil || resp == nil || len(resp.Results) != 0 {
		t.Fatalf("images: %v %v", resp, err)
	}
}

func TestLongTextStopsAtFirstHitChunk(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	text := strings.Repeat("\u6f22\u5b57🙂ab", 30*1024/12)
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m", "violence")) })
	resp, err := rig.p.CheckText(t.Context(), testCC, text, Scope{Modality: ModalityText, Selected: []string{"violence"}})
	if err != nil {
		t.Fatal(err)
	}
	if rig.hits != 1 || len(resp.Results) != 1 {
		t.Fatalf("hits %d results %d", rig.hits, len(resp.Results))
	}
}

func TestLongTextHitInThirdChunkMergesAll(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	text := strings.Repeat("\u6f22\u5b57🙂ab", 30*1024/12)
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 3 {
			writeJSON(w, okBody("third", "violence"))
			return
		}
		writeJSON(w, okBody("m"))
	})
	resp, err := rig.p.CheckText(t.Context(), testCC, text, Scope{Modality: ModalityText, Selected: []string{"violence"}})
	if err != nil {
		t.Fatal(err)
	}
	if rig.hits != 3 || len(resp.Results) != 3 || resp.Model != "third" {
		t.Fatalf("hits %d results %d model %q", rig.hits, len(resp.Results), resp.Model)
	}
	dec := decideHit(resp, []string{"violence"}, ModalityText)
	if !dec.Hit || dec.FirstResult != 2 {
		t.Fatalf("decision %+v", dec)
	}
	for i, b := range rig.bodies {
		txt := b["input"].([]any)[0].(map[string]any)["text"].(string)
		if len(txt) > maxTextChunkBytes || !utf8.ValidString(txt) {
			t.Fatalf("chunk %d invalid", i)
		}
	}
}

func TestLastNonEmptyModelWinsWhenMerging(t *testing.T) {
	text := strings.Repeat("a", maxTextChunkBytes*2)
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeJSON(w, okBody("first"))
		} else {
			writeJSON(w, okBody(""))
		}
	})
	resp, err := rig.p.CheckText(t.Context(), testCC, text, Scope{Modality: ModalityText, Selected: []string{"violence"}})
	if err != nil || resp.Model != "first" {
		t.Fatalf("%+v %v", resp, err)
	}
}

func TestProbeStyleCheckNeverStopsEarly(t *testing.T) {
	text := strings.Repeat("a", maxTextChunkBytes*2+5)
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m", "violence")) })
	if _, err := rig.p.CheckText(t.Context(), testCC, text, Scope{}); err != nil {
		t.Fatal(err)
	}
	if rig.hits != 3 {
		t.Fatalf("with no selected categories every chunk is sent, got %d", rig.hits)
	}
}

func TestImageBatchingByDataURLSize(t *testing.T) {
	big := make([]byte, 9<<20) // ~12 MiB of base64 each
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m")) })
	imgs := []Image{{Data: big, MIME: "image/png"}, {Data: big, MIME: "image/png"}, {Data: []byte("tiny"), MIME: "image/png"}}
	resp, err := rig.p.CheckImages(t.Context(), testCC, imgs, Scope{Modality: ModalityImage, Selected: []string{"sexual"}})
	if err != nil {
		t.Fatal(err)
	}
	if rig.hits != 3 {
		// 12 MiB + 12 MiB > 20 MiB, so each big image is alone; the tiny one rides with the second.
		sizes := []int{}
		for _, b := range rig.bodies {
			sizes = append(sizes, len(b["input"].([]any)))
		}
		if !reflect.DeepEqual(sizes, []int{1, 2}) {
			t.Fatalf("batch sizes %v", sizes)
		}
	}
	if len(resp.Results) != rig.hits {
		t.Fatalf("results %d hits %d", len(resp.Results), rig.hits)
	}
}

func TestImageBatchSingleOversizeGoesAlone(t *testing.T) {
	huge := make([]byte, 16<<20) // ~21.3 MiB of base64: over the batch limit by itself
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m")) })
	imgs := []Image{{Data: []byte("a"), MIME: "image/png"}, {Data: huge, MIME: "image/png"}, {Data: []byte("b"), MIME: "image/png"}}
	if _, err := rig.p.CheckImages(t.Context(), testCC, imgs, Scope{Modality: ModalityImage, Selected: []string{"sexual"}}); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, b := range rig.bodies {
		sizes = append(sizes, len(b["input"].([]any)))
	}
	if !reflect.DeepEqual(sizes, []int{1, 1, 1}) {
		t.Fatalf("batch sizes %v", sizes)
	}
}

func TestImageBatchStopsAtHit(t *testing.T) {
	big := make([]byte, 9<<20)
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { writeJSON(w, okBody("m", "violence")) })
	imgs := []Image{{Data: big, MIME: "image/png"}, {Data: big, MIME: "image/png"}}
	if _, err := rig.p.CheckImages(t.Context(), testCC, imgs, Scope{Modality: ModalityImage, Selected: []string{"violence"}}); err != nil {
		t.Fatal(err)
	}
	if rig.hits != 1 {
		t.Fatalf("hits %d", rig.hits)
	}
}

// ---- error handling and retries ------------------------------------------------------

func TestRetryOnServerErrorThenSuccess(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			http.Error(w, "boom", 500)
			return
		}
		writeJSON(w, okBody("m"))
	})
	if _, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{}); err != nil {
		t.Fatal(err)
	}
	if rig.hits != 2 {
		t.Fatalf("hits %d", rig.hits)
	}
}

func TestTwoServerErrorsGiveServiceError(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { http.Error(w, "secret-body sk-secret", 503) })
	_, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeServiceError || pe.Status != 503 {
		t.Fatalf("err %v", err)
	}
	if rig.hits != 2 {
		t.Fatalf("hits %d", rig.hits)
	}
	if strings.Contains(err.Error(), "secret") || errorCodeOf(err) != CodeServiceError {
		t.Fatalf("error text leaks: %q", err.Error())
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", 401) })
	_, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{})
	if errorCodeOf(err) != CodeServiceError || rig.hits != 1 {
		t.Fatalf("code %s hits %d", errorCodeOf(err), rig.hits)
	}
}

func TestRateLimitedWaitsRetryAfter(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "slow down", 429)
	})
	_, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{})
	if errorCodeOf(err) != CodeRateLimited || rig.hits != 2 {
		t.Fatalf("code %s hits %d", errorCodeOf(err), rig.hits)
	}
	if len(rig.sleeps) != 1 || rig.sleeps[0] != 2*time.Second {
		t.Fatalf("sleeps %v", rig.sleeps)
	}
}

func TestRateLimitedThenOK(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			http.Error(w, "x", 429)
			return
		}
		writeJSON(w, okBody("m"))
	})
	if _, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{}); err != nil {
		t.Fatal(err)
	}
	if len(rig.sleeps) != 1 || rig.sleeps[0] != 0 {
		t.Fatalf("no Retry-After means no wait: %v", rig.sleeps)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	p := newHTTPProvider(nil, func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	cases := map[string]time.Duration{
		"":                              0,
		"abc":                           0,
		"-5":                            0,
		"3":                             3 * time.Second,
		"500":                           30 * time.Second,
		"Thu, 01 Jan 2026 00:00:10 GMT": 10 * time.Second,
		"Wed, 31 Dec 2025 23:00:00 GMT": 0,
		"Thu, 01 Jan 2026 01:00:00 GMT": 30 * time.Second,
		"not a date":                    0,
	}
	for in, want := range cases {
		if got := p.retryAfter(in); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":       `{"results": [`,
		"empty results":   `{"id":"x","results":[]}`,
		"no results":      `{"id":"x"}`,
		"no categories":   `{"results":[{"flagged":true}]}`,
		"second no cats":  `{"results":[{"categories":{"a":true}},{"flagged":false}]}`,
		"not json":        `<html>`,
		"null categories": `{"results":[{"categories":null}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			_, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{})
			if errorCodeOf(err) != CodeInvalidResponse {
				t.Fatalf("code %s (%v)", errorCodeOf(err), err)
			}
			if rig.hits != 1 {
				t.Fatalf("invalid responses are not retried, hits %d", rig.hits)
			}
		})
	}
}

func TestResponseReadIsCapped(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[{"categories":{"a":false}}],"pad":"`)
		io.WriteString(w, strings.Repeat("x", maxResponseBytes+10))
		io.WriteString(w, `"}`)
	})
	_, err := rig.p.CheckText(t.Context(), testCC, "x", Scope{})
	if errorCodeOf(err) != CodeInvalidResponse {
		t.Fatalf("oversize body must fail to parse, got %v", err)
	}
}

type errDoer struct {
	errs  []error
	calls int
}

func (d *errDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	if d.calls <= len(d.errs) && d.errs[d.calls-1] != nil {
		return nil, d.errs[d.calls-1]
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, okBody("m"))
	return rec.Result(), nil
}

func TestNetworkErrors(t *testing.T) {
	mk := func(d Doer) *httpProvider {
		p := newHTTPProvider(d, time.Now)
		p.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
		return p
	}
	t.Run("transient failures retry once", func(t *testing.T) {
		for _, e := range []error{syscall.ECONNRESET, syscall.ECONNREFUSED, io.ErrUnexpectedEOF} {
			d := &errDoer{errs: []error{e}}
			if _, err := mk(d).CheckText(t.Context(), testCC, "x", Scope{}); err != nil || d.calls != 2 {
				t.Fatalf("%v: err %v calls %d", e, err, d.calls)
			}
		}
	})
	t.Run("persistent failure is a network error", func(t *testing.T) {
		d := &errDoer{errs: []error{syscall.ECONNRESET, syscall.ECONNRESET}}
		_, err := mk(d).CheckText(t.Context(), testCC, "x", Scope{})
		if errorCodeOf(err) != CodeNetworkError || d.calls != 2 {
			t.Fatalf("code %s calls %d", errorCodeOf(err), d.calls)
		}
	})
	t.Run("other failures do not retry", func(t *testing.T) {
		d := &errDoer{errs: []error{errors.New("tls: bad certificate")}}
		_, err := mk(d).CheckText(t.Context(), testCC, "x", Scope{})
		if errorCodeOf(err) != CodeNetworkError || d.calls != 1 {
			t.Fatalf("code %s calls %d", errorCodeOf(err), d.calls)
		}
	})
}

type slowDoer struct{}

func (slowDoer) Do(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestDeadlineIsATimeout(t *testing.T) {
	p := newHTTPProvider(slowDoer{}, time.Now)
	cc := testCC
	cc.Timeout = 30 * time.Millisecond
	start := time.Now()
	_, err := p.CheckText(t.Context(), cc, "x", Scope{})
	if errorCodeOf(err) != CodeTimeout {
		t.Fatalf("code %s (%v)", errorCodeOf(err), err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline not honoured")
	}
}

func TestCallerCancellationAbortsImmediately(t *testing.T) {
	p := newHTTPProvider(slowDoer{}, time.Now)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := p.CheckText(ctx, testCC, "x", Scope{})
	if !errors.Is(err, context.Canceled) || errorCodeOf(err) != CodeWorkerLost {
		t.Fatalf("err %v code %s", err, errorCodeOf(err))
	}
}

func TestRetryWaitBeyondDeadlineIsATimeout(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "20")
		http.Error(w, "x", 429)
	})
	rig.p.sleep = sleepContext // real sleeping, bounded by the shared deadline
	cc := testCC
	cc.Timeout = 50 * time.Millisecond
	_, err := rig.p.CheckText(t.Context(), cc, "x", Scope{})
	if errorCodeOf(err) != CodeTimeout || rig.hits != 1 {
		t.Fatalf("code %s hits %d", errorCodeOf(err), rig.hits)
	}
}

func TestInvalidBaseURLIsAServiceError(t *testing.T) {
	rig := newRig(func(n int, w http.ResponseWriter, r *http.Request) {})
	cc := testCC
	cc.BaseURL = "ftp://x"
	_, err := rig.p.CheckText(t.Context(), cc, "x", Scope{})
	if errorCodeOf(err) != CodeServiceError || rig.hits != 0 {
		t.Fatalf("%v hits %d", err, rig.hits)
	}
}

func TestErrorCodeMapping(t *testing.T) {
	cases := map[string]error{
		CodeTimeout:      context.DeadlineExceeded,
		CodeWorkerLost:   context.Canceled,
		CodeServiceError: errors.New("whatever"),
		CodeRateLimited:  &ProviderError{Code: CodeRateLimited},
	}
	for want, err := range cases {
		if got := errorCodeOf(err); got != want {
			t.Errorf("errorCodeOf(%v) = %s, want %s", err, got, want)
		}
	}
	for code, text := range publicErrorText {
		if text == "" || len(text) > 255 || strings.Contains(strings.ToLower(text), "sk-") {
			t.Errorf("bad public text for %s", code)
		}
	}
	if publicTextFor("unknown") != publicErrorText[CodeServiceError] {
		t.Error("unknown codes use the service text")
	}
}

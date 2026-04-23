package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

type rec struct {
	path   string
	header http.Header
	body   map[string]any
}

func serve(t *testing.T, status int, reply string) (*httptest.Server, *[]rec) {
	var recs []rec
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		recs = append(recs, rec{r.URL.Path, r.Header.Clone(), m})
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(s.Close)
	return s, &recs
}

func client() *Client {
	return &Client{HTTP: outbound.NewPool(outbound.PoolOptions{Proxy: outbound.NoProxy})}
}

func texts(ss ...string) []Input {
	var in []Input
	for _, s := range ss {
		in = append(in, Input{Text: s})
	}
	return in
}

func TestOpenAIBatchAndReordering(t *testing.T) {
	s, recs := serve(t, 200, `{"data":[{"embedding":[3,3],"index":2},{"embedding":[1,1],"index":0},{"embedding":[2,2],"index":1}]}`)
	vecs, err := client().Embed(context.Background(), Request{APIBase: s.URL + "/v1", APIKey: "K", Model: "m", Inputs: texts("a", "b", "c"), Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(*recs) != 1 {
		t.Fatalf("one POST for the whole batch, got %d", len(*recs))
	}
	r := (*recs)[0]
	if r.path != "/v1/embeddings" || r.header.Get("Authorization") != "Bearer K" {
		t.Fatalf("%+v", r)
	}
	if r.body["model"] != "m" || len(r.body["input"].([]any)) != 3 || r.body["dimensions"] != float64(2) {
		t.Fatalf("%v", r.body)
	}
	if vecs[0][0] != 1 || vecs[1][0] != 2 || vecs[2][0] != 3 {
		t.Fatalf("input order restored from the indexes: %v", vecs)
	}
}

func TestOpenAIValidation(t *testing.T) {
	cases := []struct {
		name, reply string
		dims        int
		wantSub     string
	}{
		{"duplicate index", `{"data":[{"embedding":[1],"index":0},{"embedding":[2],"index":0}]}`, 0, "duplicate index 0"},
		{"index out of range", `{"data":[{"embedding":[1],"index":0},{"embedding":[2],"index":5}]}`, 0, "out of range"},
		{"wrong count", `{"data":[{"embedding":[1],"index":0}]}`, 0, "1 vectors for 2 inputs"},
		{"wrong dimension", `{"data":[{"embedding":[1,2,3],"index":0},{"embedding":[1,2],"index":1}]}`, 2, "vector 0 has 3 dimensions, expected 2"},
		{"empty vector", `{"data":[{"embedding":[],"index":0},{"embedding":[1],"index":1}]}`, 0, "vector 0 is empty"},
		{"bad json", `nope`, 0, "not valid JSON"},
	}
	for _, tc := range cases {
		s, _ := serve(t, 200, tc.reply)
		_, err := client().Embed(context.Background(), Request{APIBase: s.URL, Model: "m", Inputs: texts("a", "b"), Dimensions: tc.dims})
		if err == nil || !strings.Contains(err.Error(), tc.wantSub) || !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestOmitDimensionsStillValidates(t *testing.T) {
	s, recs := serve(t, 200, `{"data":[{"embedding":[1,2,3],"index":0}]}`)
	_, err := client().Embed(context.Background(), Request{APIBase: s.URL, Model: "m", Inputs: texts("a"), Dimensions: 2, OmitDimensions: true})
	if err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("%v", err)
	}
	if _, ok := (*recs)[0].body["dimensions"]; ok {
		t.Fatal("dimensions omitted from the request")
	}
}

func TestImagesAndModality(t *testing.T) {
	s, recs := serve(t, 200, `{"data":[{"embedding":[1],"index":0}]}`)
	img := []Input{{Image: &Image{MIME: "image/png", Data: []byte("I")}}}
	_, err := client().Embed(context.Background(), Request{APIBase: s.URL, Model: "m", Inputs: img})
	if !errors.Is(err, ErrModalityUnsupported) || len(*recs) != 0 {
		t.Fatalf("openai is text only and nothing is sent: %v", err)
	}
	if SupportsImages("openai") || SupportsImages("") || !SupportsImages("gemini") || !SupportsImages("voyage") || !SupportsImages("jina") {
		t.Fatal("capability table")
	}
	// Jina.
	if _, err := client().Embed(context.Background(), Request{Protocol: "jina", APIBase: s.URL, Model: "m", Inputs: append(texts("t"), img...)[0:1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := client().Embed(context.Background(), Request{Protocol: "jina", APIBase: s.URL, Model: "m", Inputs: img, Dimensions: 1}); err != nil {
		t.Fatal(err)
	}
	b := (*recs)[1].body
	in := b["input"].([]any)[0].(map[string]any)
	if in["image"] != "data:image/png;base64,SQ==" || b["dimensions"] != float64(1) || (*recs)[1].path != "/embeddings" {
		t.Fatalf("%v", b)
	}
}

func TestVoyageWire(t *testing.T) {
	s, recs := serve(t, 200, `{"data":[{"embedding":[1,1],"index":0},{"embedding":[2,2],"index":1}]}`)
	in := []Input{{Text: "hello"}, {Image: &Image{Data: []byte("I")}}}
	if _, err := client().Embed(context.Background(), Request{Protocol: "VOYAGE", APIBase: s.URL, APIKey: "K", Model: "vm", Inputs: in, Purpose: PurposeQuery, Dimensions: 2}); err != nil {
		t.Fatal(err)
	}
	r := (*recs)[0]
	if r.path != "/multimodalembeddings" || r.header.Get("Authorization") != "Bearer K" {
		t.Fatalf("%+v", r)
	}
	if r.body["input_type"] != "query" || r.body["output_dimension"] != float64(2) {
		t.Fatalf("%v", r.body)
	}
	c0 := r.body["inputs"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	c1 := r.body["inputs"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if c0["type"] != "text" || c0["text"] != "hello" || c1["type"] != "image_base64" || c1["image_base64"] != "data:image/jpeg;base64,SQ==" {
		t.Fatalf("%v %v", c0, c1)
	}
}

func TestGeminiWireAndCountCheck(t *testing.T) {
	s, recs := serve(t, 200, `{"embeddings":[{"values":[1,2]},{"values":[3,4]}]}`)
	in := []Input{{Text: "one"}, {Image: &Image{MIME: "image/png", Data: []byte("I")}}}
	vecs, err := client().Embed(context.Background(), Request{Protocol: "gemini", APIBase: s.URL, APIKey: "GK", Model: "text-embedding-004", Inputs: in, Purpose: PurposeDocument, Dimensions: 2})
	if err != nil || len(vecs) != 2 || vecs[1][1] != 4 {
		t.Fatalf("%v %v", err, vecs)
	}
	r := (*recs)[0]
	if r.path != "/v1beta/models/text-embedding-004:batchEmbedContents" || r.header.Get("X-Goog-Api-Key") != "GK" || r.header.Get("Authorization") != "" {
		t.Fatalf("%+v", r)
	}
	reqs := r.body["requests"].([]any)
	first := reqs[0].(map[string]any)
	if first["model"] != "models/text-embedding-004" || first["taskType"] != "RETRIEVAL_DOCUMENT" || first["outputDimensionality"] != float64(2) {
		t.Fatalf("%v", first)
	}
	second := reqs[1].(map[string]any)["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if second["inlineData"].(map[string]any)["mimeType"] != "image/png" {
		t.Fatalf("%v", second)
	}
	// Query purpose, models/ prefix kept, explicit version kept.
	s2, recs2 := serve(t, 200, `{"embeddings":[{"values":[1]}]}`)
	if _, err := client().Embed(context.Background(), Request{Protocol: "gemini", APIBase: s2.URL + "/v1", Model: "models/x", Inputs: texts("q"), Purpose: PurposeQuery}); err != nil {
		t.Fatal(err)
	}
	if (*recs2)[0].path != "/v1/models/x:batchEmbedContents" || (*recs2)[0].body["requests"].([]any)[0].(map[string]any)["taskType"] != "RETRIEVAL_QUERY" {
		t.Fatalf("%+v", (*recs2)[0])
	}
	// Two inputs, one vector → error.
	s3, _ := serve(t, 200, `{"embeddings":[{"values":[1]}]}`)
	if _, err := client().Embed(context.Background(), Request{Protocol: "gemini", APIBase: s3.URL, Model: "m", Inputs: texts("a", "b")}); err == nil {
		t.Fatal("mismatched counts must fail")
	}
}

func TestErrorsAndEdgeCases(t *testing.T) {
	s, _ := serve(t, 500, strings.Repeat("x", 3000))
	_, err := client().Embed(context.Background(), Request{APIBase: s.URL, Model: "m", Inputs: texts("a")})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 500 || len(se.Body) != 1024 {
		t.Fatalf("status errors carry the status and the first 1024 bytes: %v", err)
	}
	if v, err := client().Embed(context.Background(), Request{}); v != nil || err != nil {
		t.Fatal("an empty batch returns immediately with no vectors")
	}
	if _, err := client().Embed(context.Background(), Request{Protocol: "cohere", APIBase: s.URL, Model: "m", Inputs: texts("a")}); !errors.Is(err, ErrUnknownProtocol) {
		t.Fatalf("%v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(500 * time.Millisecond) }))
	defer slow.Close()
	start := time.Now()
	if _, err := client().Embed(context.Background(), Request{APIBase: slow.URL, Model: "m", Inputs: texts("a"), Timeout: 50 * time.Millisecond}); err == nil || time.Since(start) > 400*time.Millisecond {
		t.Fatalf("per-request timeout: %v", err)
	}
	if _, err := client().Embed(context.Background(), Request{APIBase: s.URL, Inputs: texts("a")}); err == nil {
		t.Fatal("model required")
	}
	if _, err := client().Embed(context.Background(), Request{Inputs: texts("a"), Model: "m"}); err == nil {
		t.Fatal("api base required")
	}
}

func TestEmbeddingGoesThroughSSRFPolicy(t *testing.T) {
	s, recs := serve(t, 200, `{"data":[]}`)
	policy, _ := outbound.NewPolicy(true, nil, nil)
	c := &Client{HTTP: outbound.NewPool(outbound.PoolOptions{Trusted: policy, Proxy: outbound.NoProxy})}
	// The configured embedding host is trusted exactly (administrator setting).
	_, err := c.Embed(context.Background(), Request{APIBase: s.URL, Model: "m", Inputs: texts("a")})
	if errors.Is(err, outbound.ErrBlocked) || len(*recs) != 1 {
		t.Fatalf("the configured endpoint itself is trusted: %v", err)
	}
}

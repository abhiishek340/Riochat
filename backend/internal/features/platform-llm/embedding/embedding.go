// Package embedding turns a batch of inputs into vectors through one of the
// supported provider wire formats.
package embedding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
)

// Wire protocols.
const (
	ProtocolOpenAI = "openai"
	ProtocolGemini = "gemini"
	ProtocolVoyage = "voyage"
	ProtocolJina   = "jina"
)

// Limits.
const (
	DefaultTimeout   = 60 * time.Second
	MaxResponseBytes = 64 << 20
	ErrorBodyBytes   = 1024
	defaultGeminiAPI = "https://generativelanguage.googleapis.com"
)

// Errors (matched with errors.Is).
var (
	ErrModalityUnsupported = errors.New("embedding protocol does not support this input modality")
	ErrUnknownProtocol     = errors.New("unknown embedding protocol")
	ErrInvalidResponse     = errors.New("invalid embedding response")
)

// Purpose tells the provider how the text will be used.
type Purpose string

// Purposes.
const (
	PurposeDocument Purpose = "document"
	PurposeQuery    Purpose = "query"
)

// Image is an image input.
type Image struct {
	MIME string
	Data []byte
}

// Input is one item to embed: text or an image.
type Input struct {
	Text  string
	Image *Image
}

// Request is one batch.
type Request struct {
	Protocol string
	APIBase  string
	APIKey   string
	Model    string
	Inputs   []Input
	Purpose  Purpose
	// Dimensions is the expected vector width (0 means "do not check").
	Dimensions int
	// OmitDimensions leaves the dimension parameter out of the request; the
	// response width is still validated.
	OmitDimensions bool
	// Timeout per request (default 60 s).
	Timeout time.Duration
}

// Embedder is the port other areas depend on.
type Embedder interface {
	Embed(ctx context.Context, r Request) ([][]float32, error)
}

// Doer executes requests through the outbound pool.
type Doer interface {
	Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error)
}

// Client implements Embedder over HTTP.
type Client struct {
	HTTP   Doer
	Tracer *trace.Tracer
}

var _ Embedder = (*Client)(nil)

// SupportsImages reports whether a protocol can embed images.
func SupportsImages(protocol string) bool {
	switch normalizeProtocol(protocol) {
	case ProtocolGemini, ProtocolVoyage, ProtocolJina:
		return true
	}
	return false
}

func normalizeProtocol(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return ProtocolOpenAI
	}
	return p
}

var versionRe = regexp.MustCompile(`^v\d+((alpha|beta|preview)\d*)?$`)

// Embed returns one vector per input, in input order.
func (c *Client) Embed(ctx context.Context, r Request) ([][]float32, error) {
	if len(r.Inputs) == 0 {
		return nil, nil
	}
	proto := normalizeProtocol(r.Protocol)
	switch proto {
	case ProtocolOpenAI, ProtocolGemini, ProtocolVoyage, ProtocolJina:
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknownProtocol, r.Protocol)
	}
	for _, in := range r.Inputs {
		if in.Image != nil && !SupportsImages(proto) {
			return nil, ErrModalityUnsupported
		}
	}
	if strings.TrimSpace(r.Model) == "" {
		return nil, errors.New("embedding model is required")
	}
	base := strings.TrimRight(strings.TrimSpace(r.APIBase), "/")
	if base == "" && proto == ProtocolGemini {
		base = defaultGeminiAPI
	}
	if base == "" {
		return nil, errors.New("embedding api base is required")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx, span := c.Tracer.Start(ctx, "embedding.embed", trace.KindInternal,
		trace.String("embedding.protocol", proto), trace.Int("embedding.inputs", int64(len(r.Inputs))))
	defer span.End()

	var (
		endpoint string
		body     any
		header   = http.Header{}
	)
	sendDims := r.Dimensions > 0 && !r.OmitDimensions
	switch proto {
	case ProtocolOpenAI:
		endpoint = base + "/embeddings"
		texts := make([]string, len(r.Inputs))
		for i, in := range r.Inputs {
			texts[i] = in.Text
		}
		m := map[string]any{"model": r.Model, "input": texts}
		if sendDims {
			m["dimensions"] = r.Dimensions
		}
		body = m
		setBearer(header, r.APIKey)
	case ProtocolGemini:
		model := strings.TrimSpace(r.Model)
		if !strings.HasPrefix(model, "models/") {
			model = "models/" + model
		}
		g := base
		if u, err := url.Parse(base); err == nil {
			last := u.Path[strings.LastIndex(u.Path, "/")+1:]
			if !versionRe.MatchString(last) {
				g = base + "/v1beta"
			}
		}
		endpoint = g + "/" + model + ":batchEmbedContents"
		task := "RETRIEVAL_DOCUMENT"
		if r.Purpose == PurposeQuery {
			task = "RETRIEVAL_QUERY"
		}
		reqs := make([]any, len(r.Inputs))
		for i, in := range r.Inputs {
			part := map[string]any{"text": in.Text}
			if in.Image != nil {
				part = map[string]any{"inlineData": map[string]any{"mimeType": imageMIME(in.Image.MIME), "data": base64.StdEncoding.EncodeToString(in.Image.Data)}}
			}
			item := map[string]any{"model": model, "content": map[string]any{"parts": []any{part}}, "taskType": task}
			if sendDims {
				item["outputDimensionality"] = r.Dimensions
			}
			reqs[i] = item
		}
		body = map[string]any{"requests": reqs}
		if k := strings.TrimSpace(r.APIKey); k != "" {
			header.Set("x-goog-api-key", k)
		}
	case ProtocolVoyage:
		endpoint = base + "/multimodalembeddings"
		inputs := make([]any, len(r.Inputs))
		for i, in := range r.Inputs {
			var content map[string]any
			if in.Image != nil {
				content = map[string]any{"type": "image_base64", "image_base64": dataURI(in.Image)}
			} else {
				content = map[string]any{"type": "text", "text": in.Text}
			}
			inputs[i] = map[string]any{"content": []any{content}}
		}
		inputType := string(r.Purpose)
		if inputType == "" {
			inputType = string(PurposeDocument)
		}
		m := map[string]any{"model": r.Model, "inputs": inputs, "input_type": inputType}
		if sendDims {
			m["output_dimension"] = r.Dimensions
		}
		body = m
		setBearer(header, r.APIKey)
	case ProtocolJina:
		endpoint = base + "/embeddings"
		inputs := make([]any, len(r.Inputs))
		for i, in := range r.Inputs {
			if in.Image != nil {
				inputs[i] = map[string]any{"image": dataURI(in.Image)}
			} else {
				inputs[i] = map[string]any{"text": in.Text}
			}
		}
		m := map[string]any{"model": r.Model, "input": inputs}
		if sendDims {
			m["dimensions"] = r.Dimensions
		}
		body = m
		setBearer(header, r.APIKey)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid embedding endpoint")
	}
	req.Header = header
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(ctx, outbound.Endpoint{URL: base, Kind: outbound.KindTrusted, Integration: "embedding endpoint"}, req)
	if err != nil {
		span.RecordError(err)
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("embedding response read failed: %w", err)
	}
	if len(raw) > MaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, MaxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := raw
		if len(snippet) > ErrorBodyBytes {
			snippet = snippet[:ErrorBodyBytes]
		}
		err := &StatusError{Status: resp.StatusCode, Body: string(snippet)}
		span.RecordError(err)
		return nil, err
	}
	var vecs [][]float32
	if proto == ProtocolGemini {
		vecs, err = parseGemini(raw, len(r.Inputs))
	} else {
		vecs, err = parseIndexed(raw, len(r.Inputs))
	}
	if err != nil {
		return nil, err
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("%w: vector %d is empty", ErrInvalidResponse, i)
		}
		if r.Dimensions > 0 && len(v) != r.Dimensions {
			return nil, fmt.Errorf("%w: vector %d has %d dimensions, expected %d", ErrInvalidResponse, i, len(v), r.Dimensions)
		}
	}
	return vecs, nil
}

// StatusError is a non-200 answer; Body holds at most the first 1024 bytes.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("embedding request failed with status %d: %s", e.Status, strings.TrimSpace(e.Body))
}

func setBearer(h http.Header, key string) {
	if k := strings.TrimSpace(key); k != "" {
		h.Set("Authorization", "Bearer "+k)
	}
}

func imageMIME(m string) string {
	if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
		return m
	}
	return "image/jpeg"
}

func dataURI(img *Image) string {
	return "data:" + imageMIME(img.MIME) + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

func parseIndexed(raw []byte, n int) ([][]float32, error) {
	var doc struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     *int      `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: not valid JSON", ErrInvalidResponse)
	}
	if len(doc.Data) != n {
		return nil, fmt.Errorf("%w: got %d vectors for %d inputs", ErrInvalidResponse, len(doc.Data), n)
	}
	out := make([][]float32, n)
	for pos, d := range doc.Data {
		idx := pos
		if d.Index != nil {
			idx = *d.Index
		}
		if idx < 0 || idx >= n {
			return nil, fmt.Errorf("%w: index %d is out of range", ErrInvalidResponse, idx)
		}
		if out[idx] != nil {
			return nil, fmt.Errorf("%w: duplicate index %d", ErrInvalidResponse, idx)
		}
		out[idx] = toF32(d.Embedding)
	}
	return out, nil
}

func parseGemini(raw []byte, n int) ([][]float32, error) {
	var doc struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: not valid JSON", ErrInvalidResponse)
	}
	if len(doc.Embeddings) != n {
		return nil, fmt.Errorf("%w: got %d vectors for %d inputs", ErrInvalidResponse, len(doc.Embeddings), n)
	}
	out := make([][]float32, n)
	for i, e := range doc.Embeddings {
		out[i] = toF32(e.Values)
	}
	return out, nil
}

func toF32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	if len(out) == 0 {
		return []float32{}
	}
	return out
}

package extract

import (
	"context"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
)

// Config carries the settings of one engine.
type Config struct {
	URL     string
	Token   string
	Timeout time.Duration
	Model   string
	Prompt  string
	// Source selects "managed" or "external" (Tika, RapidOCR) or the MinerU
	// mode ("cloud", "self_hosted").
	Source string
	// Route is the model route for model-based OCR.
	Route llm.Route
}

// Deps are the collaborators engines need.
type Deps struct {
	HTTP     Doer
	LLM      *llm.Client
	Resolver Resolver
	Renderer PageRenderer
	Builtin  *Builtin
}

// Engine names accepted by New.
const (
	KindBuiltin  = "builtin"
	KindTika     = "tika"
	KindDocling  = "docling"
	KindMinerU   = "mineru"
	KindRapidOCR = "rapidocr"
	KindTesser   = "tesseract"
	KindPaddle   = "paddle"
	KindMistral  = "mistral"
	KindLLM      = "llm"
	KindTencent  = "tencent"
	KindAliyun   = "aliyun"
)

// New builds an engine. A missing required setting, or an engine this layer
// has no client for (tencent, aliyun), yields an unavailable error.
func New(ctx context.Context, kind string, c Config, d Deps) (Engine, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	unavailable := func(detail string) (Engine, error) {
		prefix := kind
		switch kind {
		case KindRapidOCR, KindTesser, KindPaddle, KindMistral, KindLLM, KindTencent, KindAliyun:
			prefix = "ocr"
		}
		return nil, coded(prefix+"_unavailable", detail)
	}
	switch kind {
	case "", KindBuiltin:
		if d.Builtin != nil {
			return d.Builtin, nil
		}
		return &Builtin{}, nil
	case KindTika:
		url := strings.TrimSpace(c.URL)
		if strings.EqualFold(c.Source, "managed") {
			url = ResolveManaged(ctx, d.Resolver, ManagedTikaHost, ManagedTikaURL, LocalTikaURL)
		}
		if url == "" {
			return unavailable("tika url is not configured")
		}
		return NewTika(d.HTTP, url, c.Token, c.Timeout), nil
	case KindDocling:
		if strings.TrimSpace(c.URL) == "" {
			return unavailable("docling url is not configured")
		}
		return NewDocling(d.HTTP, c.URL, c.Token, c.Timeout), nil
	case KindMinerU:
		mode := strings.ToLower(c.Source)
		if mode == MinerUSelfHosted && strings.TrimSpace(c.URL) == "" {
			return unavailable("mineru url is not configured")
		}
		return NewMinerU(d.HTTP, mode, c.URL, c.Token, c.Timeout, nil), nil
	case KindRapidOCR, KindTesser, KindPaddle:
		url := strings.TrimSpace(c.URL)
		if kind == KindRapidOCR && strings.EqualFold(c.Source, "managed") {
			url = ResolveManaged(ctx, d.Resolver, ManagedRapidOCRHost, ManagedRapidOCRURL, LocalRapidOCRURL)
		}
		if url == "" {
			return unavailable(kind + " url is not configured")
		}
		return NewOCR(d.HTTP, kind, url, c.Token, c.Timeout), nil
	case KindMistral:
		if strings.TrimSpace(c.Token) == "" {
			return unavailable("mistral api key is not configured")
		}
		return NewMistralOCR(d.HTTP, c.URL, c.Token, c.Model, c.Timeout), nil
	case KindLLM:
		route := c.Route
		if route.BaseURL == "" {
			route.BaseURL = c.URL
		}
		if route.Model == "" {
			route.Model = c.Model
		}
		if route.APIKey == "" {
			route.APIKey = c.Token
		}
		if strings.TrimSpace(route.BaseURL) == "" || strings.TrimSpace(route.Model) == "" || d.LLM == nil {
			return unavailable("model ocr is not configured")
		}
		return NewLLMOCR(d.LLM, route, c.Prompt, d.Renderer, c.Timeout), nil
	case KindTencent, KindAliyun:
		return unavailable(kind + " ocr has no client in this build")
	}
	return unavailable("unknown engine " + kind)
}

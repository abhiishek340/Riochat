package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/embedding"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/extract"
	"github.com/riochat/riochat/backend/internal/features/ragindex"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// MountRAGIndex mounts the retrieval index and hands it to the settings area
// (index administration, vector store check) and, lazily, to the conversation
// and knowledge-base adapters.
func (h *Hub) MountRAGIndex(d deps.Deps) error {
	svc, err := ragindex.MountWith(d, ragindex.Options{
		Config:   ragConfig{h: h},
		Embedder: ragEmbedder{},
		Content:  ragContent{h: h},
		Shared:   ragShared{h: h},
		Users:    ragUsers{db: d.DB},
	})
	if err != nil {
		return err
	}
	h.rag = svc
	if h.settings != nil {
		h.settings.WirePorts(settings.Ports{Embedding: ragAdmin{s: svc}, VectorStore: ragStore{s: svc}})
	}
	return nil
}

// ---- configuration ----

type ragConfig struct{ h *Hub }

func (c ragConfig) RAGConfig(context.Context) ragindex.Config {
	rt := c.h.runtime()
	f, ch := rt.File, rt.Chat
	return ragindex.Config{
		Enabled: f.EmbeddingEnabled, Host: f.EmbeddingHost, Protocol: f.EmbeddingProtocol, Key: f.EmbeddingKey,
		TimeoutSeconds: f.EmbeddingTimeoutSeconds, Dimensions: f.EmbeddingOutputDimensions,
		DimensionsPolicy: f.EmbeddingDimensionsPolicy, Normalize: f.EmbeddingNormalize,
		StoredSignature: f.EmbeddingModelSignature, Model: f.RAGModel,
		TriggerOnUpload: f.EmbedTriggerOnUpload, ChunkSizeTokens: f.EmbedChunkSizeTokens,
		ChunkOverlapTokens: f.EmbedChunkOverlapTokens, BatchSize: f.EmbedBatchSize, TopK: f.RAGTopK,
		RAGEnabled: ch.RAGEnabled, MinSimilarity: ch.RAGMinSimilarity, TokenBudget: ch.RAGTokenBudget,
		FetchMultiplier: ch.RAGFetchMultiplier, WaitReadyMS: ch.RAGWaitReadyMS, QueryHistoryTurns: ch.RAGQueryHistoryTurns,
		CacheTTLSeconds: ch.RAGRetrievalCacheTTLSeconds, Hybrid: ch.RAGHybridEnabled,
		ArtifactRetentionDays: ch.ContextArtifactRetentionDays, ImageOCREnabled: rt.Extract.ImageOCREnabled,
	}
}

// ---- provider client ----

type ragEmbedder struct{}

func (ragEmbedder) SupportsImages(protocol string) bool { return embedding.SupportsImages(protocol) }

func (ragEmbedder) Embed(ctx context.Context, r ragindex.EmbedRequest) ([][]float32, error) {
	p, ok := platformllm.Current()
	if !ok || p.Embeddings == nil {
		return nil, fmt.Errorf("%w: embedding client is not available", ragindex.ErrProviderUnavailable)
	}
	inputs := make([]embedding.Input, len(r.Inputs))
	for i, in := range r.Inputs {
		if in.IsImage() {
			inputs[i] = embedding.Input{Image: &embedding.Image{MIME: in.ImageMIME, Data: in.ImageData}}
		} else {
			inputs[i] = embedding.Input{Text: in.Text}
		}
	}
	purpose := embedding.PurposeDocument
	if r.Purpose == ragindex.PurposeQuery {
		purpose = embedding.PurposeQuery
	}
	vecs, err := p.Embeddings.Embed(ctx, embedding.Request{
		Protocol: r.Protocol, APIBase: r.Endpoint, APIKey: r.Key, Model: r.Model, Inputs: inputs, Purpose: purpose,
		Dimensions: r.Dimensions, OmitDimensions: r.OmitDimensions, Timeout: r.Timeout,
	})
	if err != nil {
		return nil, mapEmbedError(err)
	}
	return vecs, nil
}

// mapEmbedError classifies provider failures: modality errors stay their own
// kind, malformed answers keep their text (they end as the generic summary),
// and every transport or status failure means "provider unavailable". The raw
// text stays in the chain for logs only.
func mapEmbedError(err error) error {
	var se *embedding.StatusError
	switch {
	case errors.Is(err, embedding.ErrModalityUnsupported):
		return fmt.Errorf("%w: %v", ragindex.ErrModalityUnsupported, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, embedding.ErrInvalidResponse), errors.Is(err, embedding.ErrUnknownProtocol):
		return err
	case errors.As(err, &se):
		return fmt.Errorf("%w: status %d", ragindex.ErrProviderUnavailable, se.Status)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%w: %v", ragindex.ErrProviderUnavailable, err)
	}
	return fmt.Errorf("%w: %v", ragindex.ErrProviderUnavailable, err)
}

// ---- stored content ----

type ragContent struct{ h *Hub }

const extractReadLimit = 50 << 20

func (c ragContent) read(ctx context.Context, f ragindex.File, limit int64) ([]byte, error) {
	if c.h.files == nil {
		return nil, errFilesUnavailable
	}
	stored, err := c.h.files.Open(ctx, f.UserID, f.FileID)
	if err != nil {
		return nil, err
	}
	defer stored.Body.Close()
	data, err := io.ReadAll(io.LimitReader(stored.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file is larger than %d bytes", limit)
	}
	return data, nil
}

func (c ragContent) Original(ctx context.Context, f ragindex.File, limit int64) ([]byte, error) {
	return c.read(ctx, f, limit)
}

// Text extracts text with the configured document engine and falls back to the
// built-in parsers when that engine is not usable.
func (c ragContent) Text(ctx context.Context, f ragindex.File) (string, error) {
	data, err := c.read(ctx, f, extractReadLimit)
	if err != nil {
		return "", err
	}
	req := extract.Request{Name: f.FileName, MIME: f.MIME(), Data: data}
	if eng := c.engine(ctx); eng != nil {
		if res, err := eng.Extract(ctx, req); err == nil && res != nil {
			return res.Text, nil
		}
	}
	res, err := (&extract.Builtin{}).Extract(ctx, req)
	if err != nil {
		if extract.IsCode(err, "builtin_empty_content") {
			return "", nil
		}
		return "", err
	}
	return res.Text, nil
}

// engine builds the configured document engine, or nil for the built-in one.
func (c ragContent) engine(ctx context.Context) extract.Engine {
	p, ok := platformllm.Current()
	if !ok {
		return nil
	}
	x := c.h.runtime().Extract
	var cfg extract.Config
	kind := strings.ToLower(strings.TrimSpace(x.Engine))
	switch kind {
	case extract.KindTika:
		cfg = extract.Config{URL: x.TikaBaseURL, Token: x.TikaAuthToken, Source: x.TikaSource, Timeout: time.Duration(x.TikaTimeoutSeconds) * time.Second}
	case extract.KindDocling:
		cfg = extract.Config{URL: x.DoclingBaseURL, Token: x.DoclingAuthToken, Timeout: time.Duration(x.DoclingTimeoutSeconds) * time.Second}
	case extract.KindMinerU:
		cfg = extract.Config{URL: x.MinerUBaseURL, Token: x.MinerUAuthToken, Source: x.MinerUSource, Timeout: time.Duration(x.MinerUTimeoutSeconds) * time.Second}
	default:
		return nil
	}
	eng, err := extract.New(ctx, kind, cfg, extract.Deps{HTTP: p.HTTP})
	if err != nil {
		return nil
	}
	return eng
}

// ---- shared knowledge-base files and owners ----

type ragShared struct{ h *Hub }

func (s ragShared) EnabledBuiltin(ctx context.Context, ids []uint) (map[uint]bool, error) {
	out := map[uint]bool{}
	if s.h.kb == nil {
		return out, nil
	}
	for _, id := range ids {
		ok, err := s.h.kb.FileInEnabledBuiltinBase(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = true
		}
	}
	return out, nil
}

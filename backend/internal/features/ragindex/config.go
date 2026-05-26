package ragindex

import (
	"context"
	"strings"
	"time"
)

// Config is the snapshot of the administrator settings this area reads
// (namespaces file, chat and extract). Values are normalised by the accessors,
// never by the source.
type Config struct {
	// Embedding provider (namespace file).
	Enabled          bool
	Host             string
	Protocol         string
	Key              string
	TimeoutSeconds   int
	Dimensions       int
	DimensionsPolicy string // "send" or "omit"
	Normalize        bool
	// StoredSignature is file.embedding_model_signature.
	StoredSignature string
	Model           string // file.rag_model

	TriggerOnUpload    bool
	ChunkSizeTokens    int
	ChunkOverlapTokens int
	BatchSize          int
	TopK               int

	// Retrieval (namespace chat).
	RAGEnabled            bool
	MinSimilarity         float64
	TokenBudget           int
	FetchMultiplier       int
	WaitReadyMS           int
	QueryHistoryTurns     int
	CacheTTLSeconds       int
	Hybrid                bool
	ArtifactRetentionDays int

	// ImageOCREnabled is extract.image_ocr_enabled.
	ImageOCREnabled bool
}

// DefaultConfig is the documented default: everything switched off.
func DefaultConfig() Config {
	return Config{
		Protocol: "openai", TimeoutSeconds: 60, Dimensions: 1536, DimensionsPolicy: "send", Normalize: true,
		TriggerOnUpload: true, ChunkSizeTokens: 1024, ChunkOverlapTokens: 64, BatchSize: 20, TopK: 5,
		MinSimilarity: 0.45, TokenBudget: 2000, FetchMultiplier: 3, WaitReadyMS: 3000,
		CacheTTLSeconds: 120, ArtifactRetentionDays: 90,
	}
}

// ConfigSource supplies the live configuration; it is read on every use.
type ConfigSource interface {
	RAGConfig(ctx context.Context) Config
}

// ConfigFunc adapts a function to ConfigSource.
type ConfigFunc func(ctx context.Context) Config

// RAGConfig implements ConfigSource.
func (f ConfigFunc) RAGConfig(ctx context.Context) Config { return f(ctx) }

func (c Config) host() string  { return normalizeEndpoint(c.Host) }
func (c Config) model() string { return strings.TrimSpace(c.Model) }

// Signature is the effective signature of the current vector space: the stored
// one, else the legacy value from model and dimension, else empty (no model).
func (c Config) Signature() string {
	if s := strings.TrimSpace(c.StoredSignature); s != "" {
		return s
	}
	if c.model() == "" {
		return ""
	}
	return ModelSignature(c.model(), c.Dimensions)
}

func (c Config) timeout() time.Duration {
	if c.TimeoutSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func (c Config) chunkSize() int {
	if c.ChunkSizeTokens <= 0 {
		return defaultChunkTokens
	}
	return c.ChunkSizeTokens
}

func (c Config) chunkOverlap() int {
	if c.ChunkOverlapTokens < 0 {
		return defaultOverlapTokens
	}
	return c.ChunkOverlapTokens
}

func (c Config) batchSize() int {
	if c.BatchSize <= 0 {
		return 20
	}
	return c.BatchSize
}

func (c Config) topK() int {
	if c.TopK <= 0 {
		return 5
	}
	return c.TopK
}

func (c Config) minSimilarity() float64 {
	if c.MinSimilarity <= 0 {
		return 0.45
	}
	return c.MinSimilarity
}

func (c Config) tokenBudget() int {
	if c.TokenBudget <= 0 {
		return 2000
	}
	return c.TokenBudget
}

func (c Config) fetchMultiplier() int {
	if c.FetchMultiplier <= 0 {
		return 3
	}
	return c.FetchMultiplier
}

// Unavailable reasons shared by indexing and file payloads.
const (
	ReasonDisabled         = "embedding_disabled"
	ReasonModelMissing     = "embedding_model_missing"
	ReasonHostMissing      = "embedding_host_missing"
	ReasonClientMissing    = "embedding_client_missing"
	ReasonStoreUnavailable = "vector_store_unavailable"
	ReasonStoreError       = "vector_store_error"
	ReasonServiceDown      = "embedding_service_unavailable"
	ReasonRAGDisabled      = "rag_disabled"
	ReasonOutdatedIndex    = "outdated_index"
)

// configReason returns why the configuration cannot index ("" when it can).
func (c Config) configReason() string {
	switch {
	case !c.Enabled:
		return ReasonDisabled
	case c.model() == "":
		return ReasonModelMissing
	case c.host() == "":
		return ReasonHostMissing
	}
	return ""
}

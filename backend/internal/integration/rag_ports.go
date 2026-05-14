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

type ragUsers struct{ db *gorm.DB }

func (u ragUsers) Labels(ctx context.Context, ids []uint) (map[uint]string, error) {
	rows, err := accountsByIDs(ctx, u.db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(rows))
	for id, r := range rows {
		label := strings.TrimSpace(r.DisplayName)
		if label == "" {
			label = r.Username
		}
		out[id] = label
	}
	return out, nil
}

// ---- settings area: index administration ----

type ragAdmin struct{ s *ragindex.Service }

// mapAdminError translates the index's conditions into the settings area's
// port sentinels (the settings handlers pick status codes from those).
func mapAdminError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ragindex.ErrReindexRunning):
		return fmt.Errorf("%w", settings.ErrReindexRunning)
	case errors.Is(err, ragindex.ErrNotConfigured):
		return fmt.Errorf("%w: %v", settings.ErrNothingConfigured, err)
	case errors.Is(err, ragindex.ErrUnavailable):
		return fmt.Errorf("%w: %v", settings.ErrPortUnavailable, err)
	}
	return err
}

func (a ragAdmin) Status(ctx context.Context) (settings.EmbeddingStatus, error) {
	st, err := a.s.Status(ctx)
	return settings.EmbeddingStatus(st), mapAdminError(err)
}

func (a ragAdmin) Reindex(ctx context.Context, includeEmpty bool) (settings.ReindexOutcome, error) {
	res, err := a.s.Reindex(ctx, includeEmpty)
	return settings.ReindexOutcome{Submitted: res.Submitted, Reprocessed: res.Reprocessed}, mapAdminError(err)
}

func (a ragAdmin) Tasks(ctx context.Context, q settings.TaskQuery) ([]settings.EmbeddingTask, int64, error) {
	rows, total, err := a.s.Tasks(ctx, ragindex.TaskQuery{Bucket: q.Bucket, Query: q.Query, Page: q.Page, PageSize: q.PageSize})
	if err != nil {
		return nil, 0, mapAdminError(err)
	}
	out := make([]settings.EmbeddingTask, len(rows))
	for i, r := range rows {
		out[i] = settings.EmbeddingTask(r)
	}
	return out, total, nil
}

func (a ragAdmin) Retry(ctx context.Context, ids []string) (settings.RetryOutcome, error) {
	res, err := a.s.Retry(ctx, ids)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == ragindex.ErrTooManyFiles.Code {
			return settings.RetryOutcome{}, settings.ErrTooManyFiles
		}
		return settings.RetryOutcome{}, mapAdminError(err)
	}
	out := settings.RetryOutcome{Submitted: res.SubmittedFileIDs}
	for _, sk := range res.Skipped {
		out.Skipped = append(out.Skipped, settings.SkippedFile{FileID: sk.FileID, Reason: sk.Reason})
	}
	return out, nil
}

func (a ragAdmin) Reconcile(ctx context.Context) error { return a.s.Reconcile(ctx) }

func (a ragAdmin) Probe(ctx context.Context, text string) error {
	return mapAdminError(a.s.Probe(ctx, text))
}

type ragStore struct{ s *ragindex.Service }

func (r ragStore) VectorStoreAvailable(ctx context.Context) (bool, error) {
	return r.s.VectorStoreAvailable(ctx)
}

// ---- conversation area ----

// ragIndexer lets the chat-file store submit, judge, clone and forget files.
type ragIndexer struct{ h *Hub }

func (r ragIndexer) Submit(ctx context.Context, userID uint, ids []string) (*conversation.EmbeddingSubmission, error) {
	if r.h.rag == nil {
		return nil, conversation.ErrEmbedUnavailable
	}
	res, err := r.h.rag.Submit(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	out := &conversation.EmbeddingSubmission{SubmittedFileIDs: res.SubmittedFileIDs, Skipped: make([]conversation.EmbeddingSkip, 0, len(res.Skipped))}
	for _, s := range res.Skipped {
		out.Skipped = append(out.Skipped, conversation.EmbeddingSkip{FileID: s.FileID, Reason: s.Reason})
	}
	return out, nil
}

func ragFile(f conversation.FileFacts) ragindex.File {
	return ragindex.File{
		ID: f.ObjectID, FileID: f.FileID, UserID: f.UserID, FileName: f.FileName, MimeType: f.MimeType, DetectedMIME: f.DetectedMIME,
		FileCategory: f.FileCategory, StoragePath: f.StoragePath, Status: f.Status, ProcessingStatus: f.ProcessingStatus,
		ProcessingReady: f.ProcessingReady, ExtractStatus: f.ExtractStatus, EmbedStatus: f.EmbedStatus,
		EmbedSignature: f.EmbedSignature, ChunkCount: f.ChunkCount, RAGOptOut: f.RAGOptOut,
	}
}

func (r ragIndexer) Vectorization(ctx context.Context, f conversation.FileFacts) (bool, string) {
	if r.h.rag == nil {
		return false, ragindex.ReasonClientMissing
	}
	rf := ragFile(f)
	rf.UpdatedAt = f.UpdatedAt
	return r.h.rag.Vectorization(ctx, rf)
}

func (r ragIndexer) TextReady(ctx context.Context, id uint) {
	if r.h.rag != nil {
		r.h.rag.AfterProcessing(ctx, id, true)
	}
}

func (r ragIndexer) Forget(ctx context.Context, tx *gorm.DB, id uint) error {
	if r.h.rag == nil {
		return nil
	}
	return r.h.rag.Forget(ctx, tx, id)
}

func (r ragIndexer) Clone(ctx context.Context, tx *gorm.DB, from, to uint) error {
	if r.h.rag == nil {
		return ragindex.ErrCloneUnavailable
	}
	return r.h.rag.CloneIndex(ctx, tx, from, to)
}

// ragKnowledge plans retrieval for chat turns and resolves selected bases.
type ragKnowledge struct{ h *Hub }

func (k ragKnowledge) Prepare(ctx context.Context, req conversation.KnowledgeRequest) (conversation.KnowledgeResult, error) {
	if k.h.rag == nil {
		return conversation.KnowledgeResult{}, nil
	}
	in := ragindex.TurnInput{
		UserID: req.UserID, Message: req.Message, PriorUserMessages: req.PriorUserMessages, Mode: req.FileMode,
		ContextBudgetFeature: req.ContextBudgetFeature, ContextBudgetTokens: req.ContextBudgetTokens,
		KnowledgeBaseSelected: len(req.KnowledgeBaseIDs) > 0, Ephemeral: req.Ephemeral, Searching: req.Searching,
	}
	for _, f := range req.Files {
		in.Files = append(in.Files, ragindex.TurnFile{
			ObjectID: f.ObjectID, FileID: f.FileID, Name: f.Name, Category: f.Category, SHA256: f.SHA256, RAGOptOut: f.RAGOptOut,
			EmbedStatus: f.EmbedStatus, ExtractStatus: f.ExtractStatus, ChunkCount: f.ChunkCount, UpdatedAt: f.UpdatedAt,
			ProcessingReady: f.ProcessingReady, Text: f.Text, TextTokens: f.TextTokens, FullContextOK: f.FullContextOK,
			CurrentTurn: f.CurrentTurn, Generated: f.Generated, Attached: f.Attached,
		})
	}
	if len(req.KnowledgeBaseIDs) > 0 {
		if k.h.kb == nil {
			return conversation.KnowledgeResult{}, conversation.ErrKBUnavailable
		}
		res, err := k.h.kb.Resolve(ctx, req.UserID, req.KnowledgeBaseIDs)
		if err != nil {
			return conversation.KnowledgeResult{}, conversation.ErrKBUnavailable.WithCause(err)
		}
		for _, f := range res.Files {
			in.KBFiles = append(in.KBFiles, ragindex.KBFile{
				ObjectID: f.FileObjectID, FileID: f.FileID, Name: f.FileName, ChunkCount: f.ChunkCount, State: "ready",
			})
		}
	}
	out, err := k.h.rag.PrepareTurn(ctx, in)
	if err != nil {
		if errors.Is(err, ragindex.ErrKnowledgeBaseUnavailable) {
			return conversation.KnowledgeResult{}, conversation.ErrKBUnavailable.WithCause(err)
		}
		return conversation.KnowledgeResult{}, err
	}
	res := conversation.KnowledgeResult{
		Ran: out.Ran, Context: out.Context, Inline: out.Inline, Retrieved: out.Retrieved, Status: out.Status, Reason: out.Reason,
	}
	for _, a := range out.Attach {
		res.Attach = append(res.Attach, conversation.KnowledgeAttach{FileID: a.FileID, Name: a.Name})
	}
	for _, s := range out.Sources {
		res.Sources = append(res.Sources, conversation.KnowledgeSource(s))
	}
	for _, a := range out.Artifacts {
		res.Artifacts = append(res.Artifacts, conversation.ContextArtifactInput{
			Kind: a.Kind, SourceType: a.SourceType, SourceID: a.SourceID, SourceTitle: a.SourceTitle, Content: a.Content,
			ContentHash: a.ContentHash, TokenEstimate: a.TokenEstimate, Score: a.Score, MetadataJSON: a.MetadataJSON,
		})
	}
	if t := out.Trace; t != nil {
		raw, _ := json.Marshal(t.Payload)
		res.Trace = &conversation.KnowledgeTrace{Title: t.Title, Summary: t.Summary, Detail: t.Detail, Status: t.Status, PayloadJSON: string(raw)}
	}
	return res, nil
}

package ragindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// outcome is how a single indexing run ended.
type outcome int

const (
	outReady      outcome = iota // chunks published and the file is ready
	outEmpty                     // the file has no extractable text
	outStale                     // the configuration changed; the file is stale
	outDropped                   // the file is gone or inactive
	outIgnored                   // another run holds the file (or it is already ready)
	outSuperseded                // a newer task owns the file; nothing was written
)

// imageBatchBytes closes a batch before its cumulative image bytes would
// exceed this size.
const imageBatchBytes = 12 << 20

type runOpts struct {
	// takeover lets the run claim a file another consumer left processing
	// under the same signature (its lease expired).
	takeover bool
	// fromUpload moves the text-processing status through "embedding".
	fromUpload bool
}

// settleCtx returns a context for the writes that record a run's end: it
// survives cancelation and expiry of the run's own context.
func settleCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// sameSpace reports whether the live configuration still describes the task's
// vector space and endpoint.
func (s *Service) sameSpace(ctx context.Context, sig, host string) bool {
	cfg := s.config(ctx)
	return cfg.configReason() == "" && cfg.Signature() == sig && cfg.host() == normalizeEndpoint(host)
}

// runIndex claims the file and builds, publishes and marks its index. A nil
// error with a non-ready outcome is a settled case; an error means the run
// failed and the caller decides between retrying and failing the file.
func (s *Service) runIndex(ctx context.Context, objID uint, sig, host string, o runOpts) (outcome, error) {
	cfg := s.config(ctx)
	if cfg.configReason() != "" || cfg.Signature() != sig || cfg.host() != normalizeEndpoint(host) {
		sc, cancel := settleCtx(ctx)
		defer cancel()
		if err := s.markStale(sc, objID, sig, errConfigResubmit); err != nil {
			return outStale, err
		}
		return outStale, nil
	}
	if av := s.availabilityFor(ctx, cfg); !av.OK {
		return outStale, fmt.Errorf("%w: %s", ErrProviderUnavailable, av.Reason)
	}
	var f File
	if err := s.files(ctx).Where("id = ? AND status = ?", objID, fileActive).Take(&f).Error; err != nil {
		if database.IsNotFound(err) || errors.Is(err, gorm.ErrRecordNotFound) {
			return outDropped, nil
		}
		return outDropped, err
	}
	ok, err := s.claim(ctx, objID, sig, o.takeover, o.fromUpload)
	if err != nil {
		return outIgnored, err
	}
	if !ok {
		return outIgnored, nil
	}
	b, err := s.build(ctx, f, cfg, sig)
	if err != nil {
		return outReady, err
	}
	if b.empty {
		applied, err := s.publishEmpty(ctx, f, sig)
		if err != nil {
			return outEmpty, err
		}
		if !applied {
			return outSuperseded, nil
		}
		return outEmpty, nil
	}
	applied, err := s.publish(ctx, f, sig, b)
	if err != nil {
		return outReady, err
	}
	if !applied {
		return outSuperseded, nil
	}
	return s.markReady(ctx, f.ID, sig, host)
}

// markReady finishes a published index: the file becomes ready only while the
// configuration still matches, checked before and after the write.
func (s *Service) markReady(ctx context.Context, id uint, sig, host string) (outcome, error) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	if !s.sameSpace(sc, sig, host) {
		return outStale, s.markStale(sc, id, sig, errConfigDuring)
	}
	if _, err := s.transition(sc, nil, id, sig, []string{StateProcessing}, map[string]any{
		"embed_status": StateReady, "embed_error": "",
	}); err != nil {
		return outReady, err
	}
	if !s.sameSpace(sc, sig, host) {
		return outStale, s.markStale(sc, id, sig, errConfigDuring)
	}
	return outReady, nil
}

// failFile records a failed run with its user-safe summary.
func (s *Service) failFile(ctx context.Context, id uint, sig string, err error) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	if _, e := s.transition(sc, nil, id, sig, []string{StateQueued, StateProcessing}, map[string]any{
		"embed_status": StateFailed, "embed_error": truncateRunes(summarize(err), 255),
	}); e != nil {
		s.log.Warn("rag_mark_failed_error", logx.Err(e))
	}
}

// builtIndex is the result of the build phase.
type builtIndex struct {
	chunks []Chunk
	vecs   [][]float32
	empty  bool
}

// build produces the chunks and vectors of a file in the current vector space.
func (s *Service) build(ctx context.Context, f File, cfg Config, sig string) (builtIndex, error) {
	if kindOf(f.MIME(), f.FileName) == kindImage && s.imageProtocol(cfg) && pixelMIMEs[baseMIME(f.MIME())] {
		return s.buildImage(ctx, f, cfg, sig)
	}
	text, err := s.sourceText(ctx, f)
	if err != nil {
		return builtIndex{}, err
	}
	if strings.TrimSpace(text) == "" {
		return builtIndex{empty: true}, nil
	}
	pieces := Split(text, cfg.chunkSize(), cfg.chunkOverlap())
	if len(pieces) == 0 {
		return builtIndex{}, errNoContent
	}
	inputs := make([]EmbedInput, len(pieces))
	for i, p := range pieces {
		inputs[i] = EmbedInput{Text: p}
	}
	vecs, err := s.embedDocuments(ctx, cfg, inputs)
	if err != nil {
		return builtIndex{}, err
	}
	now := s.now()
	chunks := make([]Chunk, len(pieces))
	for i, p := range pieces {
		chunks[i] = Chunk{
			FileObjID: f.ID, UserID: f.UserID, ChunkIndex: i, Modality: ModalityText, Content: p,
			TokenCount: EstimateTokens(p), EmbeddingSignature: sig, CreatedAt: now,
		}
	}
	return builtIndex{chunks: chunks, vecs: vecs}, nil
}

// sourceText returns the text to index: nothing when the pipeline found the
// file empty, the stored extraction when present, else a fresh extraction.
func (s *Service) sourceText(ctx context.Context, f File) (string, error) {
	if f.ExtractStatus == extractEmpty {
		return "", nil
	}
	if strings.TrimSpace(f.ExtractText) != "" {
		return f.ExtractText, nil
	}
	if s.content == nil {
		return "", nil
	}
	return s.content.Text(ctx, f)
}

// buildImage embeds the picture itself and keeps its OCR text as the chunk
// content.
func (s *Service) buildImage(ctx context.Context, f File, cfg Config, sig string) (builtIndex, error) {
	if s.content == nil {
		return builtIndex{}, errImageFormat
	}
	raw, err := s.content.Original(ctx, f, maxOriginalSize)
	if err != nil {
		return builtIndex{}, err
	}
	data, mime, err := prepareImage(raw, f.MIME())
	if err != nil {
		return builtIndex{}, err
	}
	vecs, err := s.embedDocuments(ctx, cfg, []EmbedInput{{ImageMIME: mime, ImageData: data}})
	if err != nil {
		return builtIndex{}, err
	}
	text := strings.TrimSpace(f.ExtractText)
	return builtIndex{
		chunks: []Chunk{{
			FileObjID: f.ID, UserID: f.UserID, ChunkIndex: 0, Modality: ModalityImage, Content: text,
			TokenCount: EstimateTokens(text), EmbeddingSignature: sig, CreatedAt: s.now(),
		}},
		vecs: vecs,
	}, nil
}

// batchInputs splits inputs into batches of at most size items, closing a batch
// early when its images would pass 12 MiB (a single larger image still forms
// a batch of its own).
func batchInputs(in []EmbedInput, size int) [][]EmbedInput {
	if size <= 0 {
		size = 20
	}
	var out [][]EmbedInput
	var cur []EmbedInput
	var bytes int
	for _, it := range in {
		n := len(it.ImageData)
		if len(cur) > 0 && (len(cur) >= size || bytes+n > imageBatchBytes) {
			out = append(out, cur)
			cur, bytes = nil, 0
		}
		cur = append(cur, it)
		bytes += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// embedRequest builds the provider request shared by indexing and searching.
func (s *Service) embedRequest(cfg Config, purpose string, inputs []EmbedInput) EmbedRequest {
	return EmbedRequest{
		Protocol: cfg.Protocol, Endpoint: cfg.Host, Key: cfg.Key, Model: cfg.model(), Inputs: inputs, Purpose: purpose,
		Dimensions: cfg.Dimensions, OmitDimensions: strings.EqualFold(cfg.DimensionsPolicy, "omit"), Timeout: cfg.timeout(),
	}
}

// embedBatches embeds the inputs batch by batch and checks every batch returned
// one vector per input.
func (s *Service) embedBatches(ctx context.Context, cfg Config, purpose string, inputs []EmbedInput) ([][]float32, error) {
	if s.emb == nil {
		return nil, ErrProviderUnavailable
	}
	out := make([][]float32, 0, len(inputs))
	for _, batch := range batchInputs(inputs, cfg.batchSize()) {
		vecs, err := s.emb.Embed(ctx, s.embedRequest(cfg, purpose, batch))
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(batch) {
			return nil, errBatchMismatch
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedDocuments embeds indexing inputs and, when configured, scales every
// vector to unit length once all batches returned.
func (s *Service) embedDocuments(ctx context.Context, cfg Config, inputs []EmbedInput) ([][]float32, error) {
	vecs, err := s.embedBatches(ctx, cfg, PurposeDocument, inputs)
	if err != nil {
		return nil, err
	}
	if cfg.Normalize {
		for _, v := range vecs {
			normalizeVector(v)
		}
	}
	return vecs, nil
}

// lockFile re-reads the file row inside tx, taking the row lock where the
// database supports it, and confirms the task still owns it.
func (s *Service) lockFile(ctx context.Context, tx *gorm.DB, id uint, sig string) (bool, error) {
	var cur File
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "status", "embed_status", "embed_signature").Where("id = ?", id).Take(&cur).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if cur.Status != fileActive || cur.EmbedStatus != StateProcessing || cur.EmbedSignature != sig {
		return false, nil
	}
	return true, nil
}

// dropIndex removes the chunks and vectors of a file inside tx.
func dropIndex(tx *gorm.DB, fileObjID uint) error {
	if err := tx.Where("file_obj_id = ?", fileObjID).Delete(&ChunkVector{}).Error; err != nil {
		return err
	}
	return tx.Where("file_obj_id = ?", fileObjID).Delete(&Chunk{}).Error
}

// publish replaces the file's chunks with the new set in one transaction. It
// reports false (and writes nothing) when the file no longer belongs to the
// task: another task with a newer signature owns it.
func (s *Service) publish(ctx context.Context, f File, sig string, b builtIndex) (bool, error) {
	if len(b.chunks) != len(b.vecs) {
		return false, errBatchMismatch
	}
	applied := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owned, err := s.lockFile(ctx, tx, f.ID, sig)
		if err != nil || !owned {
			return err
		}
		for i := range b.chunks {
			c := &b.chunks[i]
			if c.EmbeddingSignature != sig {
				return errors.New("ragindex: chunk signature differs from the task")
			}
			if len(b.vecs[i]) == 0 || len(b.vecs[i]) > maxVectorDims {
				return errors.New("ragindex: unusable vector")
			}
		}
		if err := dropIndex(tx, f.ID); err != nil {
			return err
		}
		vectors := make([]ChunkVector, 0, len(b.chunks))
		for i := range b.chunks {
			c := &b.chunks[i]
			if err := tx.Create(c).Error; err != nil {
				return err
			}
			vectors = append(vectors, ChunkVector{
				ChunkID: c.ID, UserID: c.UserID, FileObjID: f.ID, EmbeddingSignature: sig,
				Dims: len(b.vecs[i]), Embedding: encodeVector(b.vecs[i]),
			})
		}
		if err := tx.CreateInBatches(vectors, 100).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&ChunkVector{}).Where("file_obj_id = ?", f.ID).Count(&n).Error; err != nil {
			return err
		}
		if int(n) != len(b.chunks) {
			return errBatchMismatch
		}
		if _, err := s.transition(ctx, tx, f.ID, sig, []string{StateProcessing}, map[string]any{"chunk_count": len(b.chunks)}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied && err == nil, err
}

// publishEmpty records a file without extractable text: previous chunks go
// away and the state becomes empty, which is not a failure.
func (s *Service) publishEmpty(ctx context.Context, f File, sig string) (bool, error) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	applied := false
	err := s.db.WithContext(sc).Transaction(func(tx *gorm.DB) error {
		owned, err := s.lockFile(sc, tx, f.ID, sig)
		if err != nil || !owned {
			return err
		}
		if err := dropIndex(tx, f.ID); err != nil {
			return err
		}
		ok, err := s.transition(sc, tx, f.ID, sig, []string{StateProcessing}, map[string]any{
			"embed_status": StateEmpty, "embed_error": errNoText, "chunk_count": 0,
		})
		applied = ok
		return err
	})
	return applied && err == nil, err
}

// Forget deletes the index data of a file inside the caller's transaction.
func (s *Service) Forget(ctx context.Context, tx *gorm.DB, fileObjID uint) error {
	if tx == nil {
		tx = s.db
	}
	return dropIndex(tx.WithContext(ctx), fileObjID)
}

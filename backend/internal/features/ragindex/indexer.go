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

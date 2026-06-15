package ragindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrKnowledgeBaseUnavailable is returned by PrepareTurn when the user
// explicitly selected a knowledge base but retrieval cannot serve it. The send
// must be aborted with the knowledge_base.unavailable error.
var ErrKnowledgeBaseUnavailable = errors.New("ragindex: knowledge base retrieval is unavailable")

// File modes of a conversation.
const (
	ModeRAG         = "rag"
	ModeFullContext = "full_context"
)

// Evidence kinds and source types stored with an answer.
const (
	KindChunk          = "file_rag_chunk"
	KindFallback       = "file_rag_fallback"
	SourceTypeChunk    = "file_chunk"
	SourceTypeFile     = "file"
	artifactContentMax = 2000
	queryTraceMax      = 240
)

// TurnFile is a file attached to the conversation, with the facts the retrieval
// plan needs. Text is the extracted text used when the full text is injected.
type TurnFile struct {
	ObjectID        uint
	FileID          string
	Name            string
	Category        string
	SHA256          string
	RAGOptOut       bool
	EmbedStatus     string
	ExtractStatus   string
	ChunkCount      int
	UpdatedAt       time.Time
	ProcessingReady bool
	Text            string
	TextTokens      int
	// FullContextOK says the file may be injected whole.
	FullContextOK bool
	// CurrentTurn marks files uploaded with the message being answered.
	CurrentTurn bool
	// Generated marks files the assistant produced; they are never reported.
	Generated bool
	// Attached says an image is already attached to the turn.
	Attached bool
}

// KBFile is a retrieval-ready file of a selected knowledge base.
type KBFile struct {
	ObjectID   uint
	FileID     string
	Name       string
	ChunkCount int
	State      string
	UpdatedAt  time.Time
}

// TurnInput describes one chat turn for retrieval planning.
type TurnInput struct {
	UserID            uint
	Message           string
	PriorUserMessages []string
	// Mode is the conversation file mode: ModeRAG, ModeFullContext or "" (auto).
	Mode                 string
	ContextBudgetFeature bool
	ContextBudgetTokens  int
	Files                []TurnFile
	// KnowledgeBaseSelected is true when the user chose at least one base;
	// KBFiles are the files those bases resolved to.
	KnowledgeBaseSelected bool
	KBFiles               []KBFile
	// Ephemeral turns (temporary chats) bypass the retrieval cache.
	Ephemeral bool
	// Searching is called before the search starts (progress event).
	Searching func()
}

// Source is a knowledge source shown with an answer.
type Source struct {
	FileName   string  `json:"fileName"`
	FileID     string  `json:"fileId"`
	ChunkIndex int     `json:"chunkIndex"`
	Score      float64 `json:"score"`
	Preview    string  `json:"preview"`
	Modality   string  `json:"modality,omitempty"`
}

// Artifact is evidence persisted for an answer.
type Artifact struct {
	Kind          string
	SourceType    string
	SourceID      string
	SourceTitle   string
	Content       string
	ContentHash   string
	TokenEstimate int
	Score         float64
	MetadataJSON  string
}

// Trace is the retrieval section of the message process trace.
type Trace struct {
	Title   string
	Summary string
	Detail  string
	Status  string
	Payload map[string]any
}

// AttachRef names an image file to attach.
type AttachRef struct {
	FileID string
	Name   string
}

// TurnOutput is the retrieval plan's result.
type TurnOutput struct {
	// Ran is false when retrieval was skipped (no events, no changes).
	Ran bool
	// Context is the dynamic context block for the current user turn.
	Context string
	// Inline lists attachments (public ids) whose full text is injected;
	// Retrieved lists attachments covered by retrieval, whose full text must
	// not be injected.
	Inline    map[string]bool
	Retrieved map[string]bool
	// Attach lists image files whose original must be attached to the turn.
	Attach    []AttachRef
	Sources   []Source
	Artifacts []Artifact
	Trace     *Trace
	Status    string
	Reason    string
}

// RAGAvailable reports whether retrieval can run for chats now and, when not,
// why (the indexing reasons plus rag_disabled when the global switch is off).
func (s *Service) RAGAvailable(ctx context.Context) (bool, string) {
	cfg := s.config(ctx)
	if !cfg.RAGEnabled {
		return false, ReasonRAGDisabled
	}
	av := s.availabilityFor(ctx, cfg)
	return av.OK, av.Reason
}

// fileDecision is how one attachment is treated this turn.
type fileDecision struct {
	retrieve bool
	inline   bool
	skip     bool
}

// planFile applies the eligibility rules of attached documents: retrieval needs
// RAG, a public id, no opt-out and a ready index; then the file mode decides.
// In "rag" mode a file that cannot be retrieved falls back to full context if
// possible, else it is skipped.
func planFile(f TurnFile, ragOn bool, in TurnInput) fileDecision {
	eligible := ragOn && f.FileID != "" && !f.RAGOptOut && normState(f.EmbedStatus) == StateReady
	switch in.Mode {
	case ModeFullContext:
		return fileDecision{inline: f.FullContextOK, skip: !f.FullContextOK}
	case ModeRAG:
		if eligible {
			return fileDecision{retrieve: true}
		}
		return fileDecision{inline: f.FullContextOK, skip: !f.FullContextOK}
	}
	if eligible && (!f.FullContextOK || (in.ContextBudgetFeature && float64(f.TextTokens) > 0.4*float64(in.ContextBudgetTokens))) {
		return fileDecision{retrieve: true}
	}
	return fileDecision{inline: f.FullContextOK, skip: !f.FullContextOK}
}

// shortError is the error label stored with fallback evidence.
func shortError(err error, status string) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded) || status == StatusTimeout:
		return "timed out"
	case status == StatusUnavailable:
		return "unavailable"
	}
	return "failed"
}

// PrepareTurn plans and runs retrieval for one chat turn and builds the
// dynamic context, evidence and trace. It returns ErrKnowledgeBaseUnavailable
// when a selected knowledge base cannot be served. Only attachment retrieval
// degrades to full text.
func (s *Service) PrepareTurn(ctx context.Context, in TurnInput) (TurnOutput, error) {
	out := TurnOutput{Inline: map[string]bool{}, Retrieved: map[string]bool{}}
	cfg := s.config(ctx)
	ragOn, _ := s.RAGAvailable(ctx)

	var retrievable []TurnFile
	for _, f := range in.Files {
		d := planFile(f, ragOn, in)
		switch {
		case d.retrieve:
			retrievable = append(retrievable, f)
		case d.inline:
			out.Inline[f.FileID] = true
		}
	}
	hasKB := in.KnowledgeBaseSelected
	if !hasKB && len(retrievable) == 0 {
		return out, nil
	}
	if len(retrievable) == 0 && len(in.KBFiles) == 0 {
		if hasKB {
			return out, ErrKnowledgeBaseUnavailable
		}
		return out, nil
	}

	out.Ran = true
	if in.Searching != nil {
		in.Searching()
	}
	query := BuildQuery(in.Message, in.PriorUserMessages, cfg.QueryHistoryTurns)

	var files []SearchFile
	kbIDs := map[string]bool{}
	names := map[uint]string{}
	seenObj := map[uint]bool{}
	for _, f := range retrievable {
		if seenObj[f.ObjectID] {
			continue
		}
		seenObj[f.ObjectID] = true
		files = append(files, SearchFile{ObjectID: f.ObjectID, FileID: f.FileID, Name: f.Name, ChunkCount: f.ChunkCount, State: normState(f.EmbedStatus), UpdatedAt: f.UpdatedAt})
		names[f.ObjectID] = f.Name
	}
	for _, f := range in.KBFiles {
		kbIDs[f.FileID] = true
		if seenObj[f.ObjectID] {
			continue
		}
		seenObj[f.ObjectID] = true
		files = append(files, SearchFile{ObjectID: f.ObjectID, FileID: f.FileID, Name: f.Name, ChunkCount: f.ChunkCount, State: normState(f.State), UpdatedAt: f.UpdatedAt})
	}

	rctx := ctx
	if cfg.WaitReadyMS > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, time.Duration(cfg.WaitReadyMS)*time.Millisecond)
		defer cancel()
	}
	res := s.Retrieve(rctx, SearchRequest{UserID: in.UserID, Query: query, Files: files, Ephemeral: in.Ephemeral})
	out.Status, out.Reason = res.Status, res.Reason

	seen := map[string]bool{}
	hits := DedupHits(seen, res.Hits)
	if res.Status == StatusHit && len(hits) > 0 {
		s.applyHit(&out, in, query, hits, retrievable, kbIDs)
		return out, nil
	}
	return s.applyMiss(out, in, query, res, retrievable, hasKB)
}

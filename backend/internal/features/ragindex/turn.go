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

// applyHit records a successful retrieval.
func (s *Service) applyHit(out *TurnOutput, in TurnInput, query string, hits []Hit, retrievable []TurnFile, kbIDs map[string]bool) {
	covered := map[string]bool{}
	fromKB := false
	var attach []AttachRef
	fileNames := map[string]bool{}
	for _, h := range hits {
		covered[h.FileID] = true
		fileNames[h.FileName] = true
		if kbIDs[h.FileID] {
			fromKB = true
		}
		out.Sources = append(out.Sources, Source{
			FileName: h.FileName, FileID: h.FileID, ChunkIndex: h.ChunkIndex, Score: h.Score,
			Preview: Preview(h.Content), Modality: imageOnly(h.Modality),
		})
		if a, ok := chunkArtifact(query, h); ok {
			out.Artifacts = append(out.Artifacts, a)
		}
		if h.Modality == ModalityImage {
			attach = append(attach, AttachRef{FileID: h.FileID, Name: h.FileName})
		}
	}
	for _, f := range retrievable {
		out.Retrieved[f.FileID] = true
	}
	for _, a := range attach {
		already := false
		for _, f := range in.Files {
			if f.FileID == a.FileID && f.Attached {
				already = true
			}
		}
		for _, o := range out.Attach {
			if o.FileID == a.FileID {
				already = true
			}
		}
		if !already {
			out.Attach = append(out.Attach, a)
		}
	}
	var unretrieved []string
	for _, f := range retrievable {
		if !covered[f.FileID] && f.CurrentTurn && !f.Generated {
			unretrieved = append(unretrieved, f.Name)
		}
	}
	notice := ""
	if in.KnowledgeBaseSelected && !fromKB {
		notice = noEvidenceNotice
	}
	out.Context = renderContext(contextParts{hits: hits, notice: notice, unretrieved: unretrieved, earlier: hasEarlier(retrievable)})
	names := sortedKeys(fileNames)
	out.Trace = &Trace{
		Title:   "Knowledge retrieval",
		Summary: fmt.Sprintf("retrieved %d relevant passages", len(hits)),
		Detail:  fmt.Sprintf("Searched %d files and used %d passages.", len(fileNames), len(hits)),
		Status:  "completed",
		Payload: map[string]any{
			"query": truncateRunes(query, queryTraceMax), "fileNames": names, "hitCount": len(hits), "citations": out.Sources,
		},
	}
}

func imageOnly(m string) string {
	if m == ModalityImage {
		return m
	}
	return ""
}

func hasEarlier(files []TurnFile) bool {
	for _, f := range files {
		if !f.CurrentTurn {
			return true
		}
	}
	return false
}

// applyMiss handles a search that returned nothing usable: an error, timeout,
// unavailable service, or no relevant passage. Attachments fall back to their
// full text where possible; a selected knowledge base turns system faults into
// ErrKnowledgeBaseUnavailable and relevance misses into a notice.
func (s *Service) applyMiss(out TurnOutput, in TurnInput, query string, res SearchResult, retrievable []TurnFile, hasKB bool) (TurnOutput, error) {
	fault := res.Status == StatusError || res.Status == StatusTimeout || res.Status == StatusUnavailable
	if hasKB && fault {
		return out, ErrKnowledgeBaseUnavailable
	}
	errLabel := shortError(res.Err, res.Status)
	var fallback []TurnFile
	var skipped []string
	var unretrieved []string
	var fileNames []string
	for _, f := range retrievable {
		fileNames = append(fileNames, f.Name)
		switch {
		case f.FullContextOK:
			out.Inline[f.FileID] = true
			fallback = append(fallback, f)
			if a, ok := fallbackArtifact(query, res, errLabel, f); ok {
				out.Artifacts = append(out.Artifacts, a)
			}
		default:
			skipped = append(skipped, f.Name)
		}
	}
	// Reporting: system faults list every attachment that got nothing; a
	// relevance miss lists only this turn's uploads. Generated files never.
	for _, f := range retrievable {
		if f.Generated || f.FullContextOK {
			continue
		}
		if fault || f.CurrentTurn {
			unretrieved = append(unretrieved, f.Name)
		}
	}
	notice := ""
	if hasKB {
		notice = noEvidenceNotice
	}
	out.Context = renderContext(contextParts{fallback: fallback, notice: notice, unretrieved: unretrieved, earlier: hasEarlier(retrievable)})
	missWord := map[string]string{
		StatusEmpty: "no relevant passage found", StatusLowScore: "no passage reached the similarity threshold",
		StatusTimeout: "retrieval timed out", StatusUnavailable: "retrieval is unavailable", StatusError: "retrieval failed",
	}[res.Status]
	if res.Status == StatusEmpty && res.Reason == ReasonBudget {
		missWord = "no passage fit the token budget"
	}
	substituted := "Full text was substituted for " + strconv.Itoa(len(fallback)) + " files."
	if len(fallback) == 0 {
		substituted = "No full text was substituted."
	}
	detail := missWord + ". " + substituted
	if len(skipped) > 0 {
		detail += " Skipped: " + strings.Join(skipped, ", ") + "."
	}
	payload := map[string]any{
		"query": truncateRunes(query, queryTraceMax), "fileNames": fileNames, "status": res.Status, "reason": res.Reason,
		"candidates": res.Candidates, "filtered": res.Filtered, "maxScore": res.MaxScore,
	}
	if errLabel != "" {
		payload["error"] = errLabel
	}
	if len(skipped) > 0 {
		payload["skippedFiles"] = skipped
	}
	out.Trace = &Trace{Title: "Knowledge retrieval", Summary: missWord, Detail: detail, Status: "completed", Payload: payload}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contentHash(kind, sourceID, content string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + sourceID + "\n" + content))
	return hex.EncodeToString(sum[:])
}

// chunkArtifact builds the evidence row of a retrieved passage; blank
// passages are not stored.
func chunkArtifact(query string, h Hit) (Artifact, bool) {
	if strings.TrimSpace(h.Content) == "" {
		return Artifact{}, false
	}
	key := h.FileID
	if key == "" {
		key = h.FileName
	}
	srcID := truncateRunes(key+":"+strconv.Itoa(h.ChunkIndex), 128)
	meta, _ := json.Marshal(map[string]any{
		"query": query, "file_id": h.FileID, "chunk_index": h.ChunkIndex, "modality": h.Modality, "score": h.Score,
	})
	return Artifact{
		Kind: KindChunk, SourceType: SourceTypeChunk, SourceID: srcID, SourceTitle: truncateRunes(h.FileName, 255),
		Content: truncateRunes(h.Content, artifactContentMax), ContentHash: contentHash(KindChunk, srcID, h.Content),
		TokenEstimate: EstimateTokens(h.Content), Score: h.Score, MetadataJSON: string(meta),
	}, true
}

// fallbackArtifact builds the evidence row of a full-text substitution; files
// without text are not stored.
func fallbackArtifact(query string, res SearchResult, errLabel string, f TurnFile) (Artifact, bool) {
	if strings.TrimSpace(f.Text) == "" {
		return Artifact{}, false
	}
	srcID := f.FileID
	switch {
	case srcID != "":
	case f.ObjectID != 0:
		srcID = strconv.FormatUint(uint64(f.ObjectID), 10)
	case f.SHA256 != "":
		srcID = f.SHA256
	default:
		srcID = f.Name
	}
	srcID = truncateRunes(srcID, 128)
	status := res.Status
	if status == "" {
		status = StatusEmpty
	}
	meta, _ := json.Marshal(map[string]any{
		"query": query, "reason": status, "error": errLabel, "file_id": f.FileID, "file_obj_id": f.ObjectID,
		"sha256": f.SHA256, "chunk_count": f.ChunkCount, "embed_status": normState(f.EmbedStatus), "extract_status": f.ExtractStatus,
	})
	return Artifact{
		Kind: KindFallback, SourceType: SourceTypeFile, SourceID: srcID, SourceTitle: truncateRunes(f.Name, 255),
		Content: truncateRunes(f.Text, artifactContentMax), ContentHash: contentHash(KindFallback, srcID, f.Text),
		TokenEstimate: EstimateTokens(f.Text), MetadataJSON: string(meta),
	}, true
}

const noEvidenceNotice = "No relevant evidence was found in the selected knowledge base. Do not claim that the knowledge base supports the answer; if you answer from general knowledge, say so."

// contextParts are the pieces of the dynamic context block.
type contextParts struct {
	hits        []Hit
	fallback    []TurnFile
	unretrieved []string
	notice      string
	earlier     bool
}

// renderContext writes the dynamic context block of the current user turn:
// a retrieval section with one element per passage, the status notice after
// it, then fallback files, unretrieved-attachment notes and the note about
// earlier documents that are searched instead of included.
func renderContext(p contextParts) string {
	var b strings.Builder
	var passages int
	for _, h := range p.hits {
		if strings.TrimSpace(h.Content) != "" {
			passages++
		}
	}
	if passages > 0 {
		b.WriteString("<retrieval>\n")
		for i, h := range p.hits {
			if strings.TrimSpace(h.Content) == "" {
				continue
			}
			pos := h.ChunkIndex
			if pos <= 0 {
				pos = i + 1
			}
			kind := ""
			if h.Modality == ModalityImage {
				kind = ` kind="image"`
			}
			fmt.Fprintf(&b, "<passage source=\"%s\" position=\"%d\"%s>%s</passage>\n",
				html.EscapeString(h.FileName), pos, kind, html.EscapeString(h.Content))
		}
		b.WriteString("</retrieval>\n")
	}
	if p.notice != "" {
		fmt.Fprintf(&b, "<retrieval_status>%s</retrieval_status>\n", html.EscapeString(p.notice))
	}
	if len(p.fallback) > 0 {
		b.WriteString("<full_text_files>\n")
		for _, f := range p.fallback {
			fmt.Fprintf(&b, "<file name=\"%s\">%s</file>\n", html.EscapeString(f.Name), html.EscapeString(f.Text))
		}
		b.WriteString("</full_text_files>\n")
	}
	if len(p.unretrieved) > 0 {
		b.WriteString("<unretrieved_files>\n")
		for _, n := range p.unretrieved {
			fmt.Fprintf(&b, "<file name=\"%s\">content not provided</file>\n", html.EscapeString(n))
		}
		b.WriteString("</unretrieved_files>\n")
	}
	if p.earlier {
		b.WriteString("<retrieval_note>Documents attached to earlier messages are searched for each question; they are not included in full.</retrieval_note>\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

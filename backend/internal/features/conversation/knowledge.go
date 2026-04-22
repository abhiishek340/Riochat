package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Event types and ids of the retrieval step.
const (
	evRAGSearch      = "rag_search"
	retrievalEventID = "rag_retrieval"
)

// fullContextOK says whether a file may be injected whole: it has extracted
// text within the full-context byte and token limits.
func fullContextOK(rs RuntimeSettings, f FileObject) bool {
	if f.ExtractStatus != "ready" || strings.TrimSpace(f.ExtractText) == "" {
		return false
	}
	if rs.FullContextMaxBytes > 0 && int64(len(f.ExtractText)) > rs.FullContextMaxBytes {
		return false
	}
	return rs.FullContextMaxTokens <= 0 || estimateTokens(f.ExtractText) <= rs.FullContextMaxTokens
}

func knowledgeFileOf(rs RuntimeSettings, f FileObject, current, generated bool) KnowledgeFile {
	return KnowledgeFile{
		ObjectID: f.ID, FileID: f.FileID, Name: f.FileName, Category: f.FileCategory, SHA256: f.SHA256, RAGOptOut: f.RAGOptOut,
		EmbedStatus: f.EmbedStatus, ExtractStatus: f.ExtractStatus, ChunkCount: f.ChunkCount, UpdatedAt: f.UpdatedAt,
		ProcessingReady: f.ProcessingReady, Text: f.ExtractText, TextTokens: estimateTokens(f.ExtractText),
		FullContextOK: fullContextOK(rs, f), CurrentTurn: current, Generated: generated,
	}
}

// conversationFiles collects the attachments of the earlier messages on the
// branch (never the images, which are attached natively) together with the
// user messages before the current one.
func (s *service) branchContext(ctx context.Context, ps *pendingSend, rs RuntimeSettings) (earlier []KnowledgeFile, prior []string, err error) {
	chain, err := s.ancestorChain(ctx, ps.conv.ID, &ps.userMsg, 10000)
	if err != nil {
		return nil, nil, err
	}
	role := map[uint]string{}
	var ids []uint
	for _, m := range chain {
		if m.ID == ps.userMsg.ID {
			continue
		}
		role[m.ID] = m.Role
		ids = append(ids, m.ID)
		if m.Role == roleUser && strings.TrimSpace(m.Content) != "" {
			prior = append(prior, m.Content)
		}
	}
	if len(ids) == 0 {
		return nil, prior, nil
	}
	var atts []Attachment
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ? AND message_id IN ? AND status <> ?", ps.conv.ID, ps.userID, ids, fileStatusDeleted).
		Order("id").Find(&atts).Error; err != nil {
		return nil, nil, err
	}
	if len(atts) == 0 {
		return nil, prior, nil
	}
	fileIDs := make([]string, 0, len(atts))
	for _, a := range atts {
		fileIDs = append(fileIDs, a.FileID)
	}
	var objs []FileObject
	if err := s.db.WithContext(ctx).Where("file_id IN ? AND user_id = ? AND status = ?", fileIDs, ps.userID, fileStatusActive).Find(&objs).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[string]FileObject, len(objs))
	for _, o := range objs {
		byID[o.FileID] = o
	}
	current := map[string]bool{}
	for _, f := range ps.files {
		current[f.FileID] = true
	}
	seen := map[string]bool{}
	for _, a := range atts {
		o, ok := byID[a.FileID]
		if !ok || seen[a.FileID] || current[a.FileID] || o.FileCategory == "image" {
			continue
		}
		seen[a.FileID] = true
		earlier = append(earlier, knowledgeFileOf(rs, o, false, role[a.MessageID] == roleAssistant))
	}
	return earlier, prior, nil
}

// knowledgeFor runs retrieval for a persisted turn. It returns the zero result
// when no retrieval port is attached.
func (s *service) knowledgeFor(ctx context.Context, ps *pendingSend, emit func(string, map[string]any)) (KnowledgeResult, error) {
	if s.ports.Knowledge == nil {
		return KnowledgeResult{}, nil
	}
	rs := s.settings(ctx)
	earlier, prior, err := s.branchContext(ctx, ps, rs)
	if err != nil {
		return KnowledgeResult{}, err
	}
	var files []KnowledgeFile
	for _, f := range ps.files {
		if f.FileCategory == "image" {
			continue
		}
		files = append(files, knowledgeFileOf(rs, f, true, false))
	}
	files = append(files, earlier...)
	mode := ""
	if v, ok, err := s.ports.Settings.UserSetting(ctx, ps.userID, "chat.file_mode"); err == nil && ok {
		mode = strings.ToLower(strings.TrimSpace(v))
		if mode == "auto" {
			mode = ""
		}
	}
	kbs := ps.req.KBIDs
	if !rs.KnowledgeBaseEnabled {
		kbs = nil
	}
	return s.ports.Knowledge.Prepare(ctx, KnowledgeRequest{
		UserID: ps.userID, Message: ps.req.Content, PriorUserMessages: prior, FileMode: mode,
		ContextBudgetFeature: rs.ContextTokenBudgetEnabled, ContextBudgetTokens: rs.ContextWindowFallbackTokens,
		Files: files, KnowledgeBaseIDs: kbs,
		Searching: func() { emit(evRAGSearch, map[string]any{"status": "searching"}) },
	})
}

// applyKnowledge adjusts the current user turn to the retrieval result:
// attachments covered by retrieval lose their full text, files nobody provides
// lose it too (the dynamic context says so), and originals of retrieved images
// join the turn.
func applyKnowledge(history []PromptMessage, kr KnowledgeResult) []PromptMessage {
	if !kr.Ran || len(history) == 0 {
		return history
	}
	last := &history[len(history)-1]
	files := make([]PromptFile, 0, len(last.Files)+len(kr.Attach))
	have := map[string]bool{}
	for _, f := range last.Files {
		have[f.FileID] = true
		if f.Kind != "image" && !kr.Inline[f.FileID] {
			f.Text, f.Retrieved = "", true
		}
		files = append(files, f)
	}
	for _, a := range kr.Attach {
		if !have[a.FileID] {
			files = append(files, PromptFile{FileID: a.FileID, FileName: a.Name, Kind: "image"})
		}
	}
	last.Files = files
	return history
}

// mergeKnowledge adds the retrieval evidence and trace to a generation result.
func mergeKnowledge(res GenerateResult, kr KnowledgeResult, now time.Time) GenerateResult {
	if !kr.Ran {
		return res
	}
	if len(kr.Sources) > 0 {
		res.KnowledgeSources = append(append([]KnowledgeSource(nil), kr.Sources...), res.KnowledgeSources...)
	}
	if len(kr.Artifacts) == 0 && kr.Trace == nil {
		return res
	}
	td := TraceData{}
	if res.Trace != nil {
		td = *res.Trace
	}
	td.Artifacts = append(append([]ContextArtifactInput(nil), kr.Artifacts...), td.Artifacts...)
	if t := kr.Trace; t != nil {
		ev := TraceEvent{
			EventID: retrievalEventID, EventType: "retrieval", Phase: "retrieval", Stage: "retrieval", Title: t.Title,
			Summary: t.Summary, ContentMarkdown: t.Detail, Status: t.Status, Seq: 1, StartedAt: now, PayloadJSON: t.PayloadJSON,
		}
		td.Events = append([]TraceEvent{ev}, td.Events...)
		for i := 1; i < len(td.Events); i++ {
			if td.Events[i].Seq == 0 {
				td.Events[i].Seq = i + 1
			}
		}
	}
	res.Trace = &td
	return res
}

// artifactHash is the content hash stored with evidence: SHA-256 over the
// kind, the source id and the full content.
func artifactHash(kind, sourceID, content string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + sourceID + "\n" + content))
	return hex.EncodeToString(sum[:])
}

// persistArtifacts stores the evidence of an answer, best effort: a failure is
// logged and the answer proceeds. Rows need a conversation, message, user and
// run; anything else is rejected as invalid input.
func (s *service) persistArtifacts(ctx context.Context, ps *pendingSend, td *TraceData) {
	if td == nil || len(td.Artifacts) == 0 {
		return
	}
	rs := s.settings(ctx)
	now := s.now()
	var expires *time.Time
	if rs.ContextArtifactRetentionDays > 0 {
		t := now.AddDate(0, 0, rs.ContextArtifactRetentionDays)
		expires = &t
	}
	for _, a := range td.Artifacts {
		hash := a.ContentHash
		if hash == "" {
			hash = artifactHash(a.Kind, a.SourceID, a.Content)
		}
		rec := ContextRecord{
			UserID: ps.userID, ConversationID: ps.conv.ID, MessageID: ps.asstMsg.ID, RunID: ps.req.RunID,
			RecordType: recordArtifact, Kind: a.Kind, SourceType: a.SourceType, SourceID: truncateRunes(a.SourceID, 128),
			SourceTitle: truncateRunes(a.SourceTitle, 255), Content: a.Content, ContentHash: hash,
			TokenEstimate: a.TokenEstimate, Score: a.Score, MetadataJSON: a.MetadataJSON, ExpiresAt: expires,
		}
		if rec.UserID == 0 || rec.ConversationID == 0 || rec.MessageID == 0 || rec.RunID == "" {
			s.log.Warn("conversation_artifact_invalid", logx.F("reason", "missing owner ids"))
			continue
		}
		if err := s.db.WithContext(ctx).Create(&rec).Error; err != nil {
			s.log.Warn("conversation_artifact_store_failed", logErr(err))
		}
	}
}

// Reuse of earlier evidence (the "historical evidence" block): a few recent
// artifacts of the active branch are offered again, compactly.
const (
	historyEvidenceItems  = 4
	historyEvidenceTokens = 600
	historySnippetRunes   = 500
	historyCandidateScan  = 24
)

// historyKinds are the artifact kinds that may be offered again. Summaries only
// count while no compaction snapshot is current.
var historyKinds = []string{"file_rag_chunk", "file_rag_fallback", "tool_result", "native_tool_result", "image_analysis"}

const kindSummary = "summary"

// historicalEvidence renders recent evidence from the active branch as compact
// elements, skipping what the current turn already carries. Evidence of other
// branches, of other users and expired rows is never used.
func (s *service) historicalEvidence(ctx context.Context, ps *pendingSend, current []ContextArtifactInput) string {
	chain, err := s.ancestorChain(ctx, ps.conv.ID, &ps.userMsg, 10000)
	if err != nil {
		return ""
	}
	var ids []uint
	for _, m := range chain {
		if m.ID != ps.userMsg.ID && m.Role == roleAssistant {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	kinds := append([]string(nil), historyKinds...)
	var snaps int64
	if err := s.db.WithContext(ctx).Model(&ContextRecord{}).
		Where("conversation_id = ? AND record_type = ?", ps.conv.ID, recordSnapshot).Count(&snaps).Error; err == nil && snaps == 0 {
		kinds = append(kinds, kindSummary)
	}
	var rows []ContextRecord
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ? AND record_type = ? AND message_id IN ? AND kind IN ?",
			ps.conv.ID, ps.userID, recordArtifact, ids, kinds).
		Where("expires_at IS NULL OR expires_at > ?", s.now()).
		Order("id DESC").Limit(historyCandidateScan).Find(&rows).Error; err != nil {
		return ""
	}
	have := map[string]bool{}
	for _, a := range current {
		have[a.Kind+"\x00"+a.SourceID] = true
	}
	var b strings.Builder
	used, n := 0, 0
	for _, r := range rows {
		if n >= historyEvidenceItems {
			break
		}
		key := r.Kind + "\x00" + r.SourceID
		if have[key] || strings.TrimSpace(r.Content) == "" {
			continue
		}
		snippet := truncateRunes(strings.Join(strings.Fields(r.Content), " "), historySnippetRunes)
		cost := estimateTokens(snippet)
		if used+cost > historyEvidenceTokens {
			continue
		}
		have[key] = true
		used += cost
		n++
		if n == 1 {
			b.WriteString("<historical_evidence>\n")
		}
		fmt.Fprintf(&b, "<item kind=\"%s\" source=\"%s\">%s</item>\n", html.EscapeString(r.Kind), html.EscapeString(r.SourceTitle), html.EscapeString(snippet))
	}
	if n == 0 {
		return ""
	}
	b.WriteString("</historical_evidence>")
	return b.String()
}

// joinContext puts the non-empty blocks of the dynamic context together.
func joinContext(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

package conversation

import (
	"context"
	"encoding/json"
	"strings"
)

// attachmentSnap is one entry of the attachments snapshot string.
type attachmentSnap struct {
	FileID           string  `json:"file_id"`
	FileName         string  `json:"file_name"`
	MimeType         string  `json:"mime_type"`
	DetectedMime     string  `json:"detected_mime"`
	FileCategory     string  `json:"file_category"`
	FileSize         int64   `json:"file_size"`
	DurationSeconds  float64 `json:"duration_seconds,omitempty"`
	Kind             string  `json:"kind"`
	ProcessingStatus string  `json:"processing_status"`
	ProcessingReady  bool    `json:"processing_ready"`
	ErrorCode        string  `json:"processing_error_code"`
	ErrorMessage     string  `json:"processing_error_message"`
}

// attachmentKind is "image" for pictures and "file" for everything else.
func attachmentKind(category string) string {
	if category == "image" {
		return "image"
	}
	return "file"
}

type enrichOpts struct {
	userID       uint
	conv         *Conversation
	summaryTrace bool
	public       bool
	// send marks the messages of a send result, which carry no moderation
	// object unless a verdict exists.
	send bool
}

// buildAttachmentSnapshots groups the non-deleted attachments of messages.
func (s *service) buildAttachmentSnapshots(ctx context.Context, messageIDs []uint) (map[uint][]attachmentSnap, error) {
	out := map[uint][]attachmentSnap{}
	if len(messageIDs) == 0 {
		return out, nil
	}
	var rows []Attachment
	if err := s.db.WithContext(ctx).Where("message_id IN ? AND status <> ?", messageIDs, fileStatusDeleted).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	fileIDs := make([]string, 0, len(rows))
	for _, a := range rows {
		fileIDs = append(fileIDs, a.FileID)
	}
	files := map[string]FileObject{}
	if len(fileIDs) > 0 {
		var fs []FileObject
		if err := s.db.WithContext(ctx).Unscoped().Where("file_id IN ?", dedupeStrings(fileIDs)).Find(&fs).Error; err != nil {
			return nil, err
		}
		for _, f := range fs {
			files[f.FileID] = f
		}
	}
	for _, a := range rows {
		snap := attachmentSnap{
			FileID: a.FileID, Kind: attachmentKind(a.Kind), FileName: a.FileName, MimeType: a.MimeType, FileSize: a.FileSize,
		}
		if f, ok := files[a.FileID]; ok {
			snap.DetectedMime, snap.FileCategory = f.DetectedMIME, f.FileCategory
			snap.ProcessingStatus, snap.ProcessingReady = f.ProcessingStatus, f.ProcessingReady
			snap.ErrorCode, snap.ErrorMessage = f.ProcessingErrCode, f.ProcessingErrMsg
		}
		if a.MetaJSON != "" {
			var meta struct {
				Duration float64 `json:"duration_seconds"`
			}
			if json.Unmarshal([]byte(a.MetaJSON), &meta) == nil && meta.Duration > 0 {
				snap.DurationSeconds = meta.Duration
			}
		}
		out[a.MessageID] = append(out[a.MessageID], snap)
	}
	return out, nil
}

func snapString(list []attachmentSnap) string {
	if len(list) == 0 {
		return "[]"
	}
	return marshalNoEscape(list)
}

func traceKey(m Message) string {
	if m.RunID != "" {
		return m.RunID
	}
	return "msg_" + m.PublicID
}

// billingCostOf derives the billingCost object from a pricing snapshot.
func billingCostOf(m Message) *BillingCost {
	snap := strings.TrimSpace(m.PricingSnapshot)
	if snap == "" {
		return nil
	}
	var probe struct {
		BillingMode string `json:"billingMode"`
	}
	if json.Unmarshal([]byte(snap), &probe) != nil {
		return nil
	}
	if probe.BillingMode == "" || strings.EqualFold(probe.BillingMode, "self") {
		return nil
	}
	cur := m.BilledCurrency
	if cur == "" {
		cur = "USD"
	}
	return &BillingCost{
		BillingMode: probe.BillingMode, BilledCurrency: cur, BilledNanousd: m.BilledNanoUSD,
		BilledUSD: float64(m.BilledNanoUSD) / 1e9, PricingSnapshotJSON: snap,
	}
}

func moderationOf(m Message, run *Run) *ModerationInfo {
	eventID := m.ModerationEventID
	state := ""
	cats := m.ModerationCategoriesJSON
	if run != nil {
		if eventID == "" {
			eventID = run.ModerationEventID
		}
		if run.ModerationState != "" && run.ModerationState != "not_required" {
			state = run.ModerationState
		}
		if strings.TrimSpace(cats) == "" || cats == "[]" {
			cats = run.ModerationCategories
		}
	}
	if eventID == "" && state == "" {
		return nil
	}
	if state == "" && m.Status == msgBlocked {
		state = "blocked"
	}
	info := &ModerationInfo{State: state, EventID: eventID, Categories: parseStringList(cats)}
	if m.Status == msgBlocked {
		if m.Role == roleUser {
			info.Direction = dirInput
		} else {
			info.Direction = dirOutput
		}
	}
	return info
}

// enrich turns message rows into response objects.
func (s *service) enrich(ctx context.Context, msgs []Message, o enrichOpts) ([]MessageDTO, error) {
	if len(msgs) == 0 {
		return []MessageDTO{}, nil
	}
	ids := make([]uint, 0, len(msgs))
	runIDs := []string{}
	keys := []string{}
	linked := map[uint]bool{}
	for _, m := range msgs {
		ids = append(ids, m.ID)
		if m.RunID != "" {
			runIDs = append(runIDs, m.RunID)
		}
		keys = append(keys, traceKey(m))
		if m.ParentMessageID != nil {
			linked[*m.ParentMessageID] = true
		}
		if m.SourceMessageID != nil {
			linked[*m.SourceMessageID] = true
		}
	}
	db := s.db.WithContext(ctx)

	have := map[uint]string{}
	for _, m := range msgs {
		have[m.ID] = m.PublicID
	}
	var missing []uint
	for id := range linked {
		if _, ok := have[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		var rows []Message
		if err := db.Unscoped().Select("id", "public_id").Where("id IN ?", missing).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			have[r.ID] = r.PublicID
		}
	}

	runs := map[string]Run{}
	if len(runIDs) > 0 {
		var rs []Run
		if err := db.Where("run_id IN ?", dedupeStrings(runIDs)).Find(&rs).Error; err != nil {
			return nil, err
		}
		for _, r := range rs {
			runs[r.RunID] = r
		}
	}
	atts, err := s.buildAttachmentSnapshots(ctx, ids)
	if err != nil {
		return nil, err
	}

	type fbRow struct {
		MessageID uint
		Feedback  string
		N         int64
	}
	up, down := map[uint]int64{}, map[uint]int64{}
	mine := map[uint]string{}
	if !o.public {
		var fbs []fbRow
		if err := db.Model(&Feedback{}).Select("message_id, feedback, COUNT(*) AS n").Where("message_id IN ?", ids).Group("message_id, feedback").Scan(&fbs).Error; err != nil {
			return nil, err
		}
		for _, f := range fbs {
			switch f.Feedback {
			case "up":
				up[f.MessageID] = f.N
			case "down":
				down[f.MessageID] = f.N
			}
		}
		var own []Feedback
		if err := db.Where("message_id IN ? AND user_id = ?", ids, o.userID).Find(&own).Error; err != nil {
			return nil, err
		}
		for _, f := range own {
			mine[f.MessageID] = f.Feedback
		}
	}

	uiStates := map[uint]map[string]json.RawMessage{}
	if !o.public {
		var assistantIDs []uint
		for _, m := range msgs {
			if m.Role == roleAssistant {
				assistantIDs = append(assistantIDs, m.ID)
			}
		}
		if len(assistantIDs) > 0 {
			var rows []UIState
			if err := db.Where("message_id IN ?", assistantIDs).Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				c, ok := compactJSON([]byte(r.StateJSON))
				if !ok || c == "" || c == "{}" || c == "null" {
					continue
				}
				if uiStates[r.MessageID] == nil {
					uiStates[r.MessageID] = map[string]json.RawMessage{}
				}
				uiStates[r.MessageID][r.BlockID] = json.RawMessage(c)
			}
		}
	}

	traceRowsByKey := map[string][]RunEvent{}
	{
		var rows []RunEvent
		if err := db.Where("run_id IN ? AND event_scope IN ?", dedupeStrings(keys), []string{scopeTraceBlock, scopeTraceEvent}).
			Order("seq ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			traceRowsByKey[r.RunID] = append(traceRowsByKey[r.RunID], r)
		}
	}

	out := make([]MessageDTO, 0, len(msgs))
	for _, m := range msgs {
		var run *Run
		if r, ok := runs[m.RunID]; ok {
			run = &r
		}
		d := MessageDTO{
			ID: m.ID, ConversationID: m.ConversationID, UserID: m.UserID, PublicID: m.PublicID,
			ParentMessageID: m.ParentMessageID, RunID: m.RunID, Role: m.Role, ContentType: m.ContentType,
			Content: m.Content, BranchReason: m.BranchReason, SourceMessageID: m.SourceMessageID,
			TokenUsage: m.TokenUsage, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadTokens, CacheWriteTokens: m.CacheWriteTokens, ReasoningTokens: m.ReasoningTokens,
			ReasoningEffort: m.ReasoningEffort, LatencyMS: m.LatencyMS, Status: m.Status, ErrorCode: m.ErrorCode,
			ErrorMessage: m.ErrorMessage, Attachments: snapString(atts[m.ID]),
			MyFeedback: mine[m.ID], ThumbsUpCount: up[m.ID], ThumbsDownCount: down[m.ID],
			EditedAt: m.EditedAt, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		}
		if d.BranchReason == "" {
			d.BranchReason = branchDefault
		}
		if m.ParentMessageID != nil {
			d.ParentPublicID = have[*m.ParentMessageID]
		}
		if m.SourceMessageID != nil {
			d.SourcePublicID = have[*m.SourceMessageID]
		}
		// Model identity comes from the run, then the conversation.
		if run != nil {
			d.PlatformModelName = run.PlatformModelName
			if d.PlatformModelName == "" {
				d.PlatformModelName = run.RequestedModelName
			}
			d.UpstreamModelName, d.ModelVendor, d.ModelIcon = run.UpstreamModelName, run.ModelVendor, run.ModelIcon
		}
		if d.PlatformModelName == "" && o.conv != nil {
			d.PlatformModelName = o.conv.ModelName
		}
		if run != nil && (m.Status == msgError || m.Status == msgInterrupted) && m.Role == roleAssistant {
			d.ErrorRequestID = run.RequestID
		}
		if !o.public {
			d.BillingCost = billingCostOf(m)
			d.Moderation = moderationOf(m, run)
			if ui := uiStates[m.ID]; len(ui) > 0 {
				d.UIState = ui
			}
		}
		if d.Moderation == nil && !o.send {
			d.Moderation = &ModerationInfo{State: "not_required", Categories: []string{}}
		}
		if ks := parseKnowledgeSources(m.KnowledgeSourcesJSON); len(ks) > 0 {
			d.KnowledgeSources = ks
		}
		if rows := traceRowsByKey[traceKey(m)]; len(rows) > 0 {
			t := buildTrace(rows, m.Status)
			t = sanitizeTrace(t, o.public)
			if o.summaryTrace && m.Role == roleAssistant && m.Status == msgSuccess {
				t = summarizeTrace(t)
			}
			d.ProcessTrace = t
		}
		if m.Status == msgBlocked && m.Role == roleAssistant {
			d.Content = ""
			d.Attachments = "[]"
			d.KnowledgeSources = nil
			d.ProcessTrace = nil
			d.UIState = nil
		}
		out = append(out, d)
	}
	return out, nil
}

func parseKnowledgeSources(raw string) []KnowledgeSource {
	if strings.TrimSpace(raw) == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var ks []KnowledgeSource
	if json.Unmarshal([]byte(raw), &ks) != nil {
		return nil
	}
	return ks
}

// enrichOne enriches a single message.
func (s *service) enrichOne(ctx context.Context, m Message, o enrichOpts) (*MessageDTO, error) {
	list, err := s.enrich(ctx, []Message{m}, o)
	if err != nil {
		return nil, err
	}
	return &list[0], nil
}

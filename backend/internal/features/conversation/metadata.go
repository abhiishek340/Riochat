package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const metadataTimeout = 90 * time.Second

const metadataPromptHead = "From the first user message below, produce JSON only. " +
	"Use the user's language. Fields: \"title\" (at most 15 Chinese characters or 8 English words)" +
	" and \"labels\" (one to three short topic labels; use [\"general\"] when the topic is unclear).\n\nUser message:\n"

// scheduleMetadata starts the background title and label generation.
func (s *service) scheduleMetadata(ctx context.Context, ps *pendingSend) {
	if ps.hint != "pending" {
		return
	}
	s.lc.Detach(ctx, "conversation-metadata", metadataTimeout, func(c context.Context) {
		s.generateMetadata(c, ps)
	})
}

func cleanLabelList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		l := trimQuotes(collapseSpaces(raw))
		l = strings.TrimSpace(strings.TrimLeft(l, "#"))
		n := len([]rune(l))
		if n == 0 || n > maxLabelLen {
			continue
		}
		k := strings.ToLower(l)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, l)
		if len(out) >= maxLabels {
			break
		}
	}
	return out
}

func parseMetadataReply(reply string) (string, []string) {
	text := strings.TrimSpace(reply)
	if m := fenceRe.FindStringSubmatch(text); m != nil {
		text = strings.TrimSpace(m[1])
	}
	var obj struct {
		Title  string   `json:"title"`
		Labels []string `json:"labels"`
	}
	if json.Unmarshal([]byte(text), &obj) == nil {
		return cleanGeneratedTitle(obj.Title), cleanLabelList(obj.Labels)
	}
	return parseGeneratedTitle(text), nil
}

func (s *service) generateMetadata(ctx context.Context, ps *pendingSend) {
	first := s.firstUserContent(ctx, ps)
	if strings.TrimSpace(first) == "" {
		return
	}
	conv, err := s.conversationByID(ctx, ps.conv.ID)
	if err != nil {
		return
	}
	wantTitle := s.userFlag(ctx, ps.userID, "chat.auto_generate_title") && s.isReplaceableTitle(conv.Title, first)
	wantLabels := s.userFlag(ctx, ps.userID, "chat.auto_generate_labels") && !conv.LabelsManuallyManaged && len(parseStringList(conv.LabelsJSON)) == 0
	if !wantTitle && !wantLabels {
		return
	}
	runID := newRunID()
	res, gerr := s.ports.Generator.Generate(ctx, GenerateRequest{
		Task: taskLabels, UserID: ps.userID, ConversationPublicID: conv.PublicID, RunID: runID, Model: s.taskModel(ctx, conv),
		Messages: []PromptMessage{{Role: roleUser, Content: metadataPromptHead + truncateRunes(first, 4000)}},
	}, func(GenerateEvent) {})
	if res.Usage.Total() > 0 || res.Billable {
		if err := s.ports.Billing.ChargeBasicService(ctx, BasicServiceCharge{UserID: ps.userID, ConversationID: conv.ID, Service: "title_labels", Usage: res.Usage, Route: res.Route, RunID: runID, LatencyMS: res.TotalLatencyMS}); err != nil {
			s.log.Warn("conversation_metadata_billing_failed", logErr(err))
			return
		}
	}
	if gerr != nil {
		return
	}
	title, labels := parseMetadataReply(res.Content)
	// Re-read so user edits made meanwhile are never overwritten.
	fresh, err := s.conversationByID(ctx, ps.conv.ID)
	if err != nil {
		return
	}
	upd := map[string]any{}
	if wantTitle && title != "" && s.isReplaceableTitle(fresh.Title, first) {
		upd["title"] = title
	}
	if wantLabels && len(labels) > 0 && !fresh.LabelsManuallyManaged && len(parseStringList(fresh.LabelsJSON)) == 0 {
		upd["labels_json"] = marshalNoEscape(labels)
	}
	if len(upd) == 0 {
		return
	}
	if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", fresh.ID).UpdateColumns(upd).Error; err != nil {
		s.log.Warn("conversation_metadata_update_failed", logErr(err))
	}
}

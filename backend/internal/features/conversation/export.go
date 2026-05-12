package conversation

import (
	"context"
	"encoding/json"
	"time"
)

// ExportDoc is the full backup of one conversation.
type ExportDoc struct {
	Version                 int             `json:"version"`
	ExportScope             string          `json:"exportScope"`
	ExportedAt              time.Time       `json:"exportedAt"`
	Conversation            ConversationDTO `json:"conversation"`
	Messages                []MessageDTO    `json:"messages"`
	Runs                    []RunDTO        `json:"runs"`
	TotalMessages           int             `json:"totalMessages"`
	TotalRuns               int             `json:"totalRuns"`
	DefaultMessagePublicIDs []string        `json:"defaultMessagePublicIDs"`
	Compatibility           ExportCompat    `json:"compatibility"`
}

// ExportCompat documents the export format.
type ExportCompat struct {
	Format string `json:"format"`
	Notes  string `json:"notes"`
}

// latestVisibleBranch returns the public ids of the newest branch: start at the
// newest root and repeatedly follow the newest (highest id) child.
func latestVisibleBranch(msgs []Message) []string {
	if len(msgs) == 0 {
		return []string{}
	}
	present := map[uint]bool{}
	for _, m := range msgs {
		present[m.ID] = true
	}
	children := map[uint][]Message{}
	var roots []Message
	for _, m := range msgs {
		if m.ParentMessageID == nil || !present[*m.ParentMessageID] {
			roots = append(roots, m)
			continue
		}
		children[*m.ParentMessageID] = append(children[*m.ParentMessageID], m)
	}
	newest := func(list []Message) (Message, bool) {
		if len(list) == 0 {
			return Message{}, false
		}
		best := list[0]
		for _, m := range list[1:] {
			if m.ID > best.ID {
				best = m
			}
		}
		return best, true
	}
	cur, ok := newest(roots)
	out := []string{}
	seen := map[uint]bool{}
	for ok && !seen[cur.ID] {
		seen[cur.ID] = true
		out = append(out, cur.PublicID)
		cur, ok = newest(children[cur.ID])
	}
	return out
}

func blockedPlaceholder(m *MessageDTO) {
	if m.Status != msgBlocked {
		return
	}
	if m.Role == roleUser {
		m.Content = "[blocked by content moderation; event " + eventOf(m) + "]"
		return
	}
	m.Content = m.ErrorMessage
}

func eventOf(m *MessageDTO) string {
	if m.Moderation != nil {
		return m.Moderation.EventID
	}
	return ""
}

// buildExport assembles the export document of a conversation. With adminView,
// blocked content is replaced by placeholders.
func (s *service) buildExport(ctx context.Context, userID uint, conv *Conversation, adminView bool) (*ExportDoc, error) {
	msgs, err := s.loadConversationMessages(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	dtos, err := s.enrich(ctx, msgs, enrichOpts{userID: userID, conv: conv})
	if err != nil {
		return nil, err
	}
	if adminView {
		for i := range dtos {
			blockedPlaceholder(&dtos[i])
		}
	}
	var runs []Run
	if err := s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID).Order("id ASC").Find(&runs).Error; err != nil {
		return nil, err
	}
	runDTOs := make([]RunDTO, 0, len(runs))
	for _, r := range runs {
		runDTOs = append(runDTOs, toRunDTO(r))
	}
	cd, err := s.conversationDTO(ctx, conv)
	if err != nil {
		return nil, err
	}
	return &ExportDoc{
		Version: 1, ExportScope: "full", ExportedAt: s.now().UTC(), Conversation: *cd, Messages: dtos, Runs: runDTOs,
		TotalMessages: len(dtos), TotalRuns: len(runDTOs), DefaultMessagePublicIDs: latestVisibleBranch(msgs),
		Compatibility: ExportCompat{Format: "riochat.conversation.export", Notes: "Full backup export. Import compatibility is not guaranteed."},
	}, nil
}

func (s *service) exportConversation(ctx context.Context, userID uint, publicID string) (*ExportDoc, error) {
	conv, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return s.buildExport(ctx, userID, conv, false)
}

// exportManifest is the last line of a bulk export.
type exportManifest struct {
	Type      string `json:"_type"`
	Complete  bool   `json:"complete"`
	Exported  int    `json:"exported"`
	Failed    int    `json:"failed"`
	FailedIDs []uint `json:"failedIDs,omitempty"`
	Error     string `json:"error,omitempty"`
}

const exportBatch = 50

// streamExportAll writes one JSON line per conversation and a final manifest.
// write returns false when the client is gone, which stops the export silently.
func (s *service) streamExportAll(ctx context.Context, userID uint, write func(line []byte) bool) {
	man := exportManifest{Type: "export_manifest", Complete: true}
	var lastID uint
	for {
		if ctx.Err() != nil {
			return
		}
		var batch []Conversation
		if err := s.db.WithContext(ctx).Where("user_id = ? AND id > ?", userID, lastID).Order("id ASC").Limit(exportBatch).Find(&batch).Error; err != nil {
			if ctx.Err() != nil {
				return
			}
			man.Complete, man.Error = false, "failed to list conversations"
			break
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			c := batch[i]
			lastID = c.ID
			doc, err := s.buildExport(ctx, userID, &c, false)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				man.Failed++
				man.FailedIDs = append(man.FailedIDs, c.ID)
				s.log.Warn("conversation_export_item_failed", logErr(err))
				continue
			}
			raw, err := json.Marshal(doc)
			if err != nil {
				man.Failed++
				man.FailedIDs = append(man.FailedIDs, c.ID)
				continue
			}
			if !write(append(raw, '\n')) {
				return
			}
			man.Exported++
		}
	}
	raw, _ := json.Marshal(man)
	write(append(raw, '\n'))
}

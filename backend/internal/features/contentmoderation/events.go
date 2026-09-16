package contentmoderation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Content summary labels. They describe what kind of content a row once held,
// never the content itself.
const (
	SummaryInputTextPass  = "input_text_pass"
	SummaryOutputTextPass = "output_text_pass"
	SummaryImagePass      = "image_pass"
	SummaryInputTextHit   = "input_text_hit"
	SummaryOutputTextHit  = "output_text_hit"
	SummaryImageHit       = "image_hit"
)

// Fields of contentLocation.Field.
const (
	FieldUserMessage     = "user_message"
	FieldUserAttachments = "user_attachments"
	FieldAssistantText   = "assistant_message"
	FieldAssistantImages = "assistant_images"
)

func summaryFor(direction, modality, result string) string {
	switch result {
	case ResultPassed:
		switch {
		case modality == ModalityImage:
			return SummaryImagePass
		case direction == DirectionInput:
			return SummaryInputTextPass
		default:
			return SummaryOutputTextPass
		}
	case ResultHit:
		switch {
		case modality == ModalityImage:
			return SummaryImageHit
		case direction == DirectionInput:
			return SummaryInputTextHit
		default:
			return SummaryOutputTextHit
		}
	}
	return ""
}

func fieldFor(direction, modality string) string {
	switch {
	case direction == DirectionInput && modality == ModalityText:
		return FieldUserMessage
	case direction == DirectionInput:
		return FieldUserAttachments
	case modality == ModalityText:
		return FieldAssistantText
	}
	return FieldAssistantImages
}

// contentLocation says where in the turn the checked content sat.
type contentLocation struct {
	Field      string `json:"field,omitempty"`
	FileID     uint   `json:"fileID,omitempty"`
	Attachment int    `json:"attachment,omitempty"`
	ChunkIndex int    `json:"chunkIndex,omitempty"`
	ChunkCount int    `json:"chunkCount,omitempty"`
}

// imageMeta describes one isolated image. StoragePath is internal.
type imageMeta struct {
	Index        int    `json:"index"`
	SHA256       string `json:"sha256"`
	MIMEType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
	StoragePath  string `json:"storagePath"`
	SourceFileID string `json:"sourceFileID,omitempty"`
}

func newPublicID() string {
	return "cme_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
}

func mustJSON(v any, fallback string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

func categoriesToJSON(c []string) string {
	if c == nil {
		c = []string{}
	}
	return mustJSON(c, "[]")
}

func categoriesFromJSON(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func scoresFromJSON(raw string) map[string]float64 {
	out := map[string]float64{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]float64{}
	}
	return out
}

func imagesFromJSON(raw string) []imageMeta {
	var out []imageMeta
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func textBinding(publicID string) string { return "content-moderation:text:" + publicID }

func imageBinding(publicID string, index int) string {
	return "content-moderation:image:" + publicID + ":" + strconv.Itoa(index)
}

// EventItem is an event as listed and shown in detail. It never carries
// ciphertext, scores, image metadata or storage paths.
type EventItem struct {
	PublicID        string    `json:"publicID"`
	UserID          uint      `json:"userID"`
	UserLabel       string    `json:"userLabel,omitempty"`
	Username        string    `json:"username,omitempty"`
	ConversationID  uint      `json:"conversationID"`
	RunID           string    `json:"runID"`
	MessagePublicID string    `json:"messagePublicID"`
	Direction       string    `json:"direction"`
	Modality        string    `json:"modality"`
	Model           string    `json:"model"`
	PolicyVersion   int64     `json:"policyVersion"`
	Result          string    `json:"result"`
	Categories      []string  `json:"categories"`
	LatencyMS       int64     `json:"latencyMS"`
	ErrorCode       string    `json:"errorCode"`
	ErrorMessage    string    `json:"errorMessage"`
	ContentSummary  string    `json:"contentSummary"`
	CreatedAt       time.Time `json:"createdAt"`
}

func eventItem(e Event) EventItem {
	return EventItem{
		PublicID: e.PublicID, UserID: e.UserID, ConversationID: e.ConversationID, RunID: e.RunID,
		MessagePublicID: e.MessagePublicID, Direction: e.Direction, Modality: e.Modality, Model: e.ModelName,
		PolicyVersion: e.PolicyVersion, Result: e.Result, Categories: categoriesFromJSON(e.CategoriesJSON),
		LatencyMS: e.LatencyMS, ErrorCode: e.ErrorCode, ErrorMessage: e.ErrorMessage,
		ContentSummary: e.ContentSummary, CreatedAt: e.CreatedAt,
	}
}

// EventFilter holds the validated list filters.
type EventFilter struct {
	Query     string
	Result    string
	Direction string
	Modality  string
	Category  string
	UserID    uint
	RunID     string
	From, To  *time.Time
}

func (s *Service) createEvent(ctx context.Context, e *Event) error {
	if e.PublicID == "" {
		e.PublicID = newPublicID()
	}
	if err := s.db.WithContext(ctx).Create(e).Error; err != nil {
		return database.Translate(err)
	}
	return nil
}

func (s *Service) eventByPublicID(ctx context.Context, publicID string) (*Event, error) {
	var e Event
	err := s.db.WithContext(ctx).Where("public_id = ?", publicID).First(&e).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrEventNotFound
		}
		return nil, err
	}
	return &e, nil
}

// applyEventFilter adds the list conditions. userIDs are users whose name or
// public id matched the free-text query.
func applyEventFilter(q *gorm.DB, f EventFilter, userIDs []uint) *gorm.DB {
	if f.Result != "" {
		q = q.Where("result = ?", f.Result)
	}
	if f.Direction != "" {
		q = q.Where("direction = ?", f.Direction)
	}
	if f.Modality != "" {
		q = q.Where("modality = ?", f.Modality)
	}
	if f.Category != "" {
		q = q.Where("categories_json LIKE ?", `%"`+f.Category+`"%`)
	}
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.RunID != "" {
		q = q.Where("run_id = ?", f.RunID)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}
	if f.Query != "" {
		raw := []string{f.Query}
		if lower := strings.ToLower(f.Query); lower != f.Query {
			raw = append(raw, lower)
		}
		var conds []string
		var args []any
		for _, col := range []string{"public_id", "run_id", "message_public_id", "result", "direction",
			"modality", "model", "error_code", "content_summary"} {
			conds = append(conds, col+" IN ?")
			args = append(args, raw)
		}
		ids := append([]uint(nil), userIDs...)
		if n, err := strconv.ParseUint(f.Query, 10, 64); err == nil && n > 0 {
			ids = append(ids, uint(n))
		}
		if len(ids) > 0 {
			conds = append(conds, "user_id IN ?")
			args = append(args, ids)
		}
		q = q.Where("("+strings.Join(conds, " OR ")+")", args...)
	}
	return q
}

// EventPage is a page of listed events.
type EventPage struct {
	Items    []EventItem `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

func (s *Service) listEvents(ctx context.Context, f EventFilter, page, pageSize int) (EventPage, error) {
	var userIDs []uint
	if f.Query != "" && s.users != nil {
		ids, err := s.users.Search(ctx, f.Query)
		if err != nil {
			s.log.Warn("content_moderation_user_search_failed", errField(err))
		} else {
			userIDs = ids
		}
	}
	q := applyEventFilter(s.db.WithContext(ctx).Model(&Event{}), f, userIDs)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return EventPage{}, database.Translate(err)
	}
	var rows []Event
	offset := (page - 1) * pageSize
	err := applyEventFilter(s.db.WithContext(ctx).Model(&Event{}), f, userIDs).
		Order("id DESC").Offset(offset).Limit(pageSize).Find(&rows).Error
	if err != nil {
		return EventPage{}, database.Translate(err)
	}
	items := make([]EventItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, eventItem(r))
	}
	s.attachLabels(ctx, items)
	return EventPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// attachLabels fills user labels in one batch; unresolved users stay unlabeled.
func (s *Service) attachLabels(ctx context.Context, items []EventItem) {
	if s.users == nil || len(items) == 0 {
		return
	}
	seen := map[uint]bool{}
	var ids []uint
	for _, it := range items {
		if it.UserID != 0 && !seen[it.UserID] {
			seen[it.UserID] = true
			ids = append(ids, it.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	labels, err := s.users.Labels(ctx, ids)
	if err != nil {
		s.log.Warn("content_moderation_user_labels_failed", errField(err))
		return
	}
	for i := range items {
		if l, ok := labels[items[i].UserID]; ok {
			items[i].UserLabel, items[i].Username = l.Label, l.Username
		}
	}
}

// ImageInfo is an isolated image as shown to administrators.
type ImageInfo struct {
	Index        int    `json:"index"`
	SHA256       string `json:"sha256"`
	MIMEType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
	SourceFileID string `json:"sourceFileID,omitempty"`
}

// EventDetail is the detail view of one event.
type EventDetail struct {
	Event           EventItem          `json:"event"`
	CategoryScores  map[string]float64 `json:"categoryScores"`
	DecryptedText   string             `json:"decryptedText,omitempty"`
	TextAvailable   bool               `json:"textAvailable"`
	ImagesAvailable bool               `json:"imagesAvailable"`
	Images          []ImageInfo        `json:"images"`
}

func (s *Service) contentRetained(e *Event) bool {
	return s.clock.Now().Before(e.ContentExpiresAt)
}

func (s *Service) buildDetail(ctx context.Context, e *Event) EventDetail {
	d := EventDetail{
		Event:          eventItem(*e),
		CategoryScores: scoresFromJSON(e.CategoryScoresJSON),
		Images:         []ImageInfo{},
	}
	retained := s.contentRetained(e)
	if retained && e.Result == ResultHit && e.Modality == ModalityText && e.EncryptedText != "" {
		plain, _, err := s.secrets.DecryptBound(e.EncryptedText, textBinding(e.PublicID))
		if err == nil {
			d.DecryptedText, d.TextAvailable = plain, true
		} else {
			s.log.Warn("content_moderation_text_decrypt_failed", errField(err))
		}
	}
	if e.Modality == ModalityImage {
		metas := imagesFromJSON(e.ImageMetaJSON)
		if retained && len(metas) > 0 {
			d.ImagesAvailable = true
			for _, m := range metas {
				d.Images = append(d.Images, ImageInfo{
					Index: m.Index, SHA256: m.SHA256, MIMEType: m.MIMEType,
					SizeBytes: m.SizeBytes, SourceFileID: m.SourceFileID,
				})
			}
		}
	}
	return d
}

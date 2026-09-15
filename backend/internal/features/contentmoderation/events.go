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

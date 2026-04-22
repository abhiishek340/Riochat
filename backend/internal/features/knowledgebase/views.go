package knowledgebase

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// KnowledgeBaseView is the wire shape of a knowledge base. Numeric database
// ids are never exposed.
type KnowledgeBaseView struct {
	PublicID            string    `json:"publicID"`
	Scope               string    `json:"scope"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	Enabled             bool      `json:"enabled"`
	SortOrder           int       `json:"sortOrder"`
	Revision            uint64    `json:"revision"`
	FileCount           int64     `json:"fileCount"`
	ReadyFileCount      int64     `json:"readyFileCount"`
	ProcessingFileCount int64     `json:"processingFileCount"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// FileView is the wire shape of a file in a list or upload response.
type FileView struct {
	FileID              string    `json:"fileID"`
	FileName            string    `json:"fileName"`
	MimeType            string    `json:"mimeType"`
	DetectedMIME        string    `json:"detectedMIME"`
	FileCategory        string    `json:"fileCategory"`
	SizeBytes           int64     `json:"sizeBytes"`
	ProcessingStatus    string    `json:"processingStatus"`
	Processing          bool      `json:"processing"`
	ProcessingReady     bool      `json:"processingReady"`
	ExtractStatus       string    `json:"extractStatus"`
	EmbedStatus         string    `json:"embedStatus"`
	EmbedError          string    `json:"embedError"`
	ChunkCount          int       `json:"chunkCount"`
	RagOptOut           bool      `json:"ragOptOut"`
	CanVectorize        bool      `json:"canVectorize"`
	VectorizationReason string    `json:"vectorizationReason"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// StatusView is the wire shape of one processing status record.
type StatusView struct {
	FileID              string    `json:"fileID"`
	DetectedMIME        string    `json:"detectedMIME"`
	FileCategory        string    `json:"fileCategory"`
	ProcessingStatus    string    `json:"processingStatus"`
	Processing          bool      `json:"processing"`
	ProcessingReady     bool      `json:"processingReady"`
	ExtractStatus       string    `json:"extractStatus"`
	EmbedStatus         string    `json:"embedStatus"`
	EmbedError          string    `json:"embedError"`
	ChunkCount          int       `json:"chunkCount"`
	RagOptOut           bool      `json:"ragOptOut"`
	CanVectorize        bool      `json:"canVectorize"`
	VectorizationReason string    `json:"vectorizationReason"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// counts are the live aggregates of a knowledge base.
type counts struct {
	files      int64
	ready      int64
	processing int64
}

func toView(kb *KnowledgeBase, c counts) KnowledgeBaseView {
	return KnowledgeBaseView{
		PublicID: kb.PublicID, Scope: kb.Scope, Name: kb.Name, Description: kb.Description,
		Enabled: kb.Enabled, SortOrder: kb.SortOrder, Revision: kb.Revision,
		FileCount: c.files, ReadyFileCount: c.ready, ProcessingFileCount: c.processing,
		CreatedAt: kb.CreatedAt.Local(), UpdatedAt: kb.UpdatedAt.Local(),
	}
}

// newPublicID returns 32 lower-case hex characters.
func newPublicID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// cleanName trims a name and enforces 1..80 characters.
func cleanName(s string) (string, error) {
	t := strings.TrimSpace(s)
	if n := utf8.RuneCountInString(t); n == 0 || n > maxNameChars {
		return "", ErrInvalid
	}
	return t, nil
}

// cleanDescription trims a description and enforces at most 255 characters.
func cleanDescription(s string) (string, error) {
	t := strings.TrimSpace(s)
	if utf8.RuneCountInString(t) > maxDescriptionChars {
		return "", ErrInvalid
	}
	return t, nil
}

// normalizeIDs trims, drops blanks and removes duplicates, keeping the first
// occurrence order.
func normalizeIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		t := strings.TrimSpace(r)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// escapeLike escapes the wildcard characters of a LIKE pattern (used with
// ESCAPE '\').
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

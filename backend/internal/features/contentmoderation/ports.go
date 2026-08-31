package contentmoderation

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// This file lists everything the moderation feature needs from neighbouring
// areas. Each interface is small and implemented by the owner of the data. Any
// port left nil degrades gracefully, as documented on the field.

// RunStore reaches the conversation run and message tables.
type RunStore interface {
	// SetRunState records the moderation state of a run (pending, moderating,
	// passed, failed_open). eventID and categoriesJSON overwrite the stored
	// values only when non-empty. Setting the state "blocked" also marks the run
	// status blocked.
	SetRunState(ctx context.Context, runID, state, eventID, categoriesJSON string) error

	// ApplyBlock withdraws a blocked turn in one transaction:
	//   1. attachments of the run's assistant messages are marked deleted and
	//      their file objects get status moderation_blocked with owner 0;
	//   2. the run's messages (assistant only, plus user messages when
	//      spec.Direction is input) get status blocked, the event id and
	//      categories, error code BlockedErrorCode and error message
	//      BlockedErrorMessage;
	//   3. assistant messages lose content and reasoning content and become
	//      content type text;
	//   4. the run's process-trace events (scopes trace_block, trace_event) are
	//      deleted;
	//   5. the run gets moderation_state blocked, the event id, the categories
	//      and status blocked. A missing run row fails the whole transaction.
	// It returns the ids of file objects whose physical objects must now be
	// deleted.
	ApplyBlock(ctx context.Context, spec BlockSpec) (fileIDs []uint, err error)

	// StaleRuns lists runs in state moderating or pending last updated before
	// cutoff.
	StaleRuns(ctx context.Context, cutoff time.Time) ([]RunRef, error)
}

// BlockSpec describes a block to apply.
type BlockSpec struct {
	RunID      string
	EventID    string
	Direction  string
	Categories []string
}

// RunRef identifies a run and the records attached to it.
type RunRef struct {
	RunID           string
	UserID          uint
	ConversationID  uint
	MessageID       uint
	MessagePublicID string
}

// Errors a FileStore reports from Load.
var (
	ErrFileNotFound = errors.New("file not found")
	ErrFileEmpty    = errors.New("file is empty")
	// ErrNotImage means the file is not an image; the check skips it silently.
	ErrNotImage = errors.New("file is not an image")
)

// FileStore reaches uploaded and generated files.
type FileStore interface {
	// Load returns the stored bytes and MIME type of a file. A blank MIME is
	// detected from the content. A file whose type is not an image answers
	// ErrNotImage without its bytes being read.
	Load(ctx context.Context, fileID uint) (data []byte, mime string, err error)
	// Revoke withdraws a generated file: status moderation_blocked, owner
	// cleared, and the attachments referencing it marked deleted.
	Revoke(ctx context.Context, fileID uint) error
	// DeleteObjects physically removes the stored objects of files. The stored
	// path is cleared only after the object store delete succeeded.
	DeleteObjects(ctx context.Context, fileIDs []uint) error
	// RetryBlockedDeletes retries, in bounded batches, the physical delete of
	// files with status moderation_blocked that still have a storage path.
	RetryBlockedDeletes(ctx context.Context, limit int) error
}

// ObjectStore holds the encrypted copies of isolated images.
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// AuditEntry is one administrator action to record.
type AuditEntry struct {
	ActorID      uint
	RequestID    string
	ClientIP     string
	UserAgent    string
	Action       string
	ResourceType string
	ResourceID   string
	Detail       map[string]any
}

// Audit actions and resource type written by the detail views.
const (
	AuditActionEventView      = "content_moderation.event.view"
	AuditActionEventImageView = "content_moderation.event_image.view"
	AuditResourceEvent        = "content_moderation_event"
)

// AuditWriter records administrator actions. Nil disables auditing; a failing
// writer never fails the request.
type AuditWriter interface {
	Record(ctx context.Context, e AuditEntry) error
}

// UserLabel is the display information of a user.
type UserLabel struct {
	Label    string
	Username string
}

// UserDirectory resolves users for the event list. Nil leaves events without
// labels and disables user matching in the free-text query.
type UserDirectory interface {
	// Labels resolves ids in one batch; unknown ids are simply absent.
	Labels(ctx context.Context, ids []uint) (map[uint]UserLabel, error)
	// Search returns ids of non-deleted users whose username, display name or
	// public id equals query, ignoring case.
	Search(ctx context.Context, query string) ([]uint, error)
}

// Stream event types written to the chat stream and the resumable stream.
const (
	EventTypeChecking = "moderation_checking"
	EventTypeBlocked  = "moderation_blocked"
)

// BilledReasonUpstreamUsage labels a blocked turn whose upstream usage is still
// billed.
const BilledReasonUpstreamUsage = "moderation_blocked_upstream_usage"

// StreamEvent is a moderation event for the chat stream.
type StreamEvent struct {
	Type         string   `json:"type"`
	EventID      string   `json:"eventID,omitempty"`
	Direction    string   `json:"direction,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	BilledReason string   `json:"billedReason,omitempty"`
}

// MarshalJSON keeps "categories" and "eventID" present on a blocked event,
// where clients always expect them, and absent on a checking event.
func (e StreamEvent) MarshalJSON() ([]byte, error) {
	if e.Type != EventTypeBlocked {
		type plain StreamEvent
		return json.Marshal(plain(e))
	}
	return json.Marshal(struct {
		Type         string   `json:"type"`
		EventID      string   `json:"eventID"`
		Direction    string   `json:"direction"`
		Categories   []string `json:"categories"`
		BilledReason string   `json:"billedReason,omitempty"`
	}{e.Type, e.EventID, e.Direction, nonNilStrings(e.Categories), e.BilledReason})
}

// Notifier delivers moderation events to the live and resumable streams of a
// run.
type Notifier interface {
	Emit(ctx context.Context, runID string, ev StreamEvent)
	// DiscardRetained drops stream deltas and media retained for replay so a
	// reconnecting client cannot see withdrawn content. The caller then writes a
	// fresh terminal event.
	DiscardRetained(ctx context.Context, runID string)
}

// Ports bundles the optional collaborators handed to MountWith.
type Ports struct {
	Runs     RunStore
	Files    FileStore
	Objects  ObjectStore
	Audit    AuditWriter
	Users    UserDirectory
	Notifier Notifier
	// Provider replaces the HTTP moderation client.
	Provider Provider
	// HTTP replaces the HTTP transport of the default Provider.
	HTTP Doer
	// ImageMaxDimension bounds the longest side of images sent for moderation
	// (default 1024).
	ImageMaxDimension int
}

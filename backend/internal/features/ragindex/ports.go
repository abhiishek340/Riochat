package ragindex

import (
	"context"
	"time"
)

// The area talks to other subsystems only through the interfaces below. Every
// one is optional: a missing member makes the dependent feature report its
// documented "unavailable" outcome.

// Embedding purposes.
const (
	PurposeDocument = "document"
	PurposeQuery    = "query"
)

// EmbedInput is one item to embed: text, or an image when ImageData is set.
type EmbedInput struct {
	Text      string
	ImageMIME string
	ImageData []byte
}

// IsImage reports whether the input is a picture.
func (i EmbedInput) IsImage() bool { return len(i.ImageData) > 0 }

// EmbedRequest is one batch for the provider.
type EmbedRequest struct {
	Protocol       string
	Endpoint       string
	Key            string
	Model          string
	Inputs         []EmbedInput
	Purpose        string
	Dimensions     int
	OmitDimensions bool
	Timeout        time.Duration
}

// Embedder is the external embedding provider client. Embed returns one vector
// per input, aligned by position, each of the configured width.
type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) ([][]float32, error)
	// SupportsImages reports whether the protocol can embed images.
	SupportsImages(protocol string) bool
}

// Content reads what is stored for a file.
type Content interface {
	// Text extracts the text of the stored file with the configured engines.
	Text(ctx context.Context, f File) (string, error)
	// Original returns the stored bytes. It fails when the file is larger than
	// limit bytes.
	Original(ctx context.Context, f File, limit int64) ([]byte, error)
}

// SharedFiles tells which files belong to an enabled platform-scope knowledge
// base (searchable by every user).
type SharedFiles interface {
	EnabledBuiltin(ctx context.Context, fileObjIDs []uint) (map[uint]bool, error)
}

// UserLabels resolves display labels of file owners in bulk.
type UserLabels interface {
	Labels(ctx context.Context, userIDs []uint) (map[uint]string, error)
}

// Options are the collaborators of the area.
type Options struct {
	Config   ConfigSource
	Embedder Embedder
	Content  Content
	Shared   SharedFiles
	Users    UserLabels
	// Workers is the number of queue workers (default 4). DisableWorkers
	// starts none; tests then drive the queue with Service.DrainQueue.
	Workers        int
	DisableWorkers bool
}

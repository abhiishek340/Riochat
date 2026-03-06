package conversation

import (
	"strings"

	"github.com/google/uuid"
)

const maxRunIDLen = 64

// newPublicID returns a 32 character identifier (a UUID without hyphens).
func newPublicID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// newRunID returns "run_" followed by 32 hex characters.
func newRunID() string { return "run_" + newPublicID() }

// newFileID returns "file_" followed by 32 hex characters.
func newFileID() string { return "file_" + newPublicID() }

// normalizeRunID trims a client supplied run id, removes hyphens and makes sure
// it carries the "run_" prefix. An empty value, or one that ends up longer than
// 64 characters, is replaced by a fresh id.
func normalizeRunID(raw string) string {
	id := strings.ReplaceAll(strings.TrimSpace(raw), "-", "")
	if id == "" {
		return newRunID()
	}
	if !strings.HasPrefix(id, "run_") {
		id = "run_" + id
	}
	if len(id) > maxRunIDLen {
		return newRunID()
	}
	return id
}

// validRunParam checks a run id taken from a URL path.
func validRunParam(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if id == "" || len(id) > maxRunIDLen {
		return "", false
	}
	return id, true
}

package memory

import (
	"errors"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes consumed by the web client's translation tables. Do not change
// the codes or the messages.
var (
	ErrKeyRequired   = apperr.Define("memory.key_required", "memory_key is required")
	ErrLimitExceeded = apperr.Define("memory.limit_exceeded", "user memory limit exceeded")
)

// ErrInvalidInput is returned by Service methods for input that can never be
// stored (an empty or over-long key). It is not an API error: the HTTP layer
// validates first and maps it to the invalid-body error as a safety net.
var ErrInvalidInput = errors.New("memory: invalid input")

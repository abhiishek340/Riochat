package promptreset

import "github.com/riochat/riochat/backend/internal/platform/apperr"

// The code strings are consumed by the web client's message catalog and must
// not change.
var (
	ErrInvalidID     = apperr.Define("prompt_preset.invalid_id", "invalid prompt preset id")
	ErrNotFound      = apperr.Define("prompt_preset.not_found", "prompt preset not found")
	ErrInvalid       = apperr.Define("request.invalid_prompt_preset", "invalid prompt preset")
	ErrTriggerExists = apperr.Define("prompt_preset_trigger.already_exists", "prompt preset trigger already exists")
)

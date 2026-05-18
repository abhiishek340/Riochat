package uicomponent

import "github.com/riochat/riochat/backend/internal/platform/apperr"

// The code strings are consumed by the web client's message catalog and must
// not change.
var (
	ErrNotFound         = apperr.Define("ui_component.not_found", "ui component not found")
	ErrInvalid          = apperr.Define("request.invalid_ui_component", "invalid ui component")
	ErrAlreadyExists    = apperr.Define("ui_component.already_exists", "ui component name already exists")
	ErrBuiltinProtected = apperr.Define("ui_component.builtin_protected", "builtin ui component is protected")
	ErrInvalidID        = apperr.Define("ui_component.invalid_id", "invalid ui component id")
)

package skill

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes are part of the client contract; the status hints let handlers
// answer with httpx.FailErr.
var (
	ErrInvalidID     = apperr.Define("skill.invalid_id", "invalid skill id").WithStatus(http.StatusBadRequest)
	ErrTooManyIDs    = apperr.Define("skill.too_many_ids", "too many skill ids").WithStatus(http.StatusBadRequest)
	ErrInvalidSkill  = apperr.Define("request.invalid_skill", "invalid skill").WithStatus(http.StatusBadRequest)
	ErrNotFound      = apperr.Define("skill.not_found", "skill not found").WithStatus(http.StatusNotFound)
	ErrTriggerExists = apperr.Define("skill_trigger.already_exists", "skill trigger already exists").WithStatus(http.StatusConflict)

	// ErrTooManySelected is returned when a message selects more skills than
	// the shared per-message selection limit allows.
	ErrTooManySelected = apperr.Define("message.too_many_selected_skills", "too many selected skills").WithStatus(http.StatusBadRequest)
	// ErrInvalidSkillUse is returned for malformed skill selections at send time.
	ErrInvalidSkillUse = apperr.Define("request.invalid_skill_use", "invalid skill use").WithStatus(http.StatusBadRequest)
	// ErrInvalidProjectDefaults mirrors the project area's invalid-project code;
	// it is what project default validation reports.
	ErrInvalidProjectDefaults = apperr.Define("request.invalid_conversation_project", "invalid conversation project").WithStatus(http.StatusBadRequest)
)

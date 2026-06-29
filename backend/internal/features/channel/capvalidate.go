package channel

import (
	"encoding/json"
	"strings"
)

// validateCapabilitiesText is the save-time validation of a capabilities
// document: JSON well-formedness, window/output limits, the reasoning
// declaration, input modalities and controls, in that order. The empty string
// is allowed.
func validateCapabilitiesText(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return ErrInvalidJSON
	}
	caps, err := parseCaps(raw)
	if err != nil {
		return ErrInvalidCapabilities
	}
	if err := caps.validateLimits(); err != nil {
		return err
	}
	if err := validateReasoningDeclaration(caps); err != nil {
		return err
	}
	if _, declared, valid := caps.explicitInputModalities(); declared && !valid {
		return ErrInvalidCapabilities
	}
	return validateControlsDeclaration(caps)
}

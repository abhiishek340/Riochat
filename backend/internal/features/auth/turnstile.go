package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// checkTurnstile verifies a registration challenge token. The check is skipped
// when the feature is off or no site key is configured.
func (s *Service) checkTurnstile(ctx context.Context, tokenValue string) error {
	st := s.settings(ctx)
	if !st.TurnstileRegistrationEnabled || strings.TrimSpace(st.TurnstileSiteKey) == "" {
		return nil
	}
	if strings.TrimSpace(st.TurnstileSecretKey) == "" {
		return ErrTurnstileNotConfigured
	}
	tokenValue = strings.TrimSpace(tokenValue)
	if tokenValue == "" {
		return ErrTurnstileRequired
	}
	if len(tokenValue) > 2048 {
		return ErrTurnstileInvalid.WithMessage("turnstile token is invalid")
	}
	endpoint := s.cfg().Security.TurnstileSiteverifyURL
	form := url.Values{"secret": {st.TurnstileSecretKey}, "response": {tokenValue}}
	if ip := clientFrom(ctx).ClientIP; ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrTurnstileInvalid.WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	status, body, err := doCapped(s.httpClient(endpoint), req)
	if err != nil {
		return ErrTurnstileInvalid.WithCause(err)
	}
	var out struct {
		Success bool `json:"success"`
	}
	if status < 200 || status > 299 || json.Unmarshal(body, &out) != nil || !out.Success {
		return ErrTurnstileInvalid
	}
	return nil
}

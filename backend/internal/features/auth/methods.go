package auth

import (
	"context"
	"time"
)

// Extra-verification methods.
const (
	methodTwoFactor = "two_factor"
	methodEmail     = "email"
	methodNone      = "none"
)

// startResult is the answer of every "start verification" endpoint.
type startResult struct {
	Sent               bool      `json:"sent"`
	ExpiresAt          time.Time `json:"expiresAt"`
	VerificationMethod string    `json:"verificationMethod"`
	AvailableMethods   []string  `json:"availableMethods"`
}

// emailMethodAvailable reports whether mailed codes can prove control of u's
// identity: verification is on and the address is verified.
func (s *Service) emailMethodAvailable(ctx context.Context, u *User) bool {
	return s.settings(ctx).EmailVerificationEnabled && u.Email != "" && isSet(u.EmailVerifiedAt)
}

// availableMethods lists the ways u can pass an extra verification, in order of
// preference. "none" appears only when nothing else does.
func (s *Service) availableMethods(ctx context.Context, u *User) ([]string, error) {
	mfa, err := s.mfaOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	var out []string
	if totpActive(mfa) {
		out = append(out, methodTwoFactor)
	}
	if s.emailMethodAvailable(ctx, u) {
		out = append(out, methodEmail)
	}
	if len(out) == 0 {
		out = []string{methodNone}
	}
	return out, nil
}

// pickMethod resolves a requested method against the available ones. An empty
// request selects the first.
func pickMethod(requested string, available []string) (string, error) {
	if requested == "" {
		return available[0], nil
	}
	if !containsString(available, requested) {
		return "", ErrVerificationMethodUnavailable
	}
	return requested, nil
}

// startVerification resolves the method and, for email, sends the code.
func (s *Service) startVerification(ctx context.Context, u *User, purpose, requested string) (startResult, error) {
	avail, err := s.availableMethods(ctx, u)
	if err != nil {
		return startResult{}, err
	}
	method, err := pickMethod(requested, avail)
	if err != nil {
		return startResult{}, err
	}
	res := startResult{VerificationMethod: method, AvailableMethods: avail}
	if method != methodEmail {
		return res, nil
	}
	target, err := normalizeEmail(u.Email)
	if err != nil {
		return startResult{}, err
	}
	exp, err := s.sendCode(ctx, u.ID, purpose, target)
	if err != nil {
		return startResult{}, err
	}
	res.Sent, res.ExpiresAt = true, exp
	res.AvailableMethods = []string{methodEmail}
	return res, nil
}

// checkMethod verifies code with the given method for u. Every failure is
// auth.verification_code_invalid except operational errors.
func (s *Service) checkMethod(ctx context.Context, u *User, purpose, method, code string) error {
	switch method {
	case methodNone:
		return nil
	case methodTwoFactor:
		ok, err := s.VerifyUserSecondFactor(ctx, u.ID, code)
		if err != nil {
			return err
		}
		if !ok {
			return ErrVerificationCodeInvalid
		}
		return nil
	case methodEmail:
		target, err := normalizeEmail(u.Email)
		if err != nil {
			return err
		}
		return s.verifyCode(ctx, u.ID, purpose, target, code)
	}
	return ErrVerificationMethodUnavailable
}

package auth

import (
	"context"
	"time"
)

func noneStart() startResult {
	return startResult{Sent: false, VerificationMethod: methodNone, AvailableMethods: []string{methodNone}}
}

func emailStart(exp time.Time) startResult {
	return startResult{Sent: true, ExpiresAt: exp, VerificationMethod: methodEmail, AvailableMethods: []string{methodEmail}}
}

// bootstrapAllowed: the account has no verified email, never used the bootstrap,
// and either has no email or one the provider did not verify.
func bootstrapAllowed(u *User) bool {
	if isSet(u.EmailVerifiedAt) || isSet(u.EmailBootstrapUsedAt) {
		return false
	}
	return u.Email == "" || u.EmailSource == EmailSourceProviderUnverified
}

// checkNewEmail normalizes a candidate address and applies policy and
// uniqueness.
func (s *Service) checkNewEmail(ctx context.Context, st Settings, raw string, owner uint) (string, error) {
	email, err := normalizeEmail(raw)
	if err != nil {
		return "", err
	}
	if err := checkEmailPolicy(email, st); err != nil {
		return "", err
	}
	taken, err := s.emailTaken(ctx, email, owner)
	if err != nil {
		return "", err
	}
	if taken {
		return "", ErrEmailAlreadyExists
	}
	return email, nil
}

// EmailBootstrapStart mails a code to the address an account wants to set first.
func (s *Service) EmailBootstrapStart(ctx context.Context, u *User, raw string) (startResult, error) {
	st := s.settings(ctx)
	if !bootstrapAllowed(u) {
		return startResult{}, ErrEmailBootstrapNotAllowed
	}
	email, err := s.checkNewEmail(ctx, st, raw, u.ID)
	if err != nil {
		return startResult{}, err
	}
	if !st.EmailVerificationEnabled {
		return noneStart(), nil
	}
	exp, err := s.sendCode(ctx, u.ID, PurposeEmailBootstrapNew, email)
	if err != nil {
		s.event(ctx, eventInput{Type: "email_bootstrap_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	s.event(ctx, eventInput{Type: "email_bootstrap_code", Result: resultSuccess, UserID: u.ID})
	return emailStart(exp), nil
}

// EmailBootstrapComplete stores the first email of an account.
func (s *Service) EmailBootstrapComplete(ctx context.Context, u *User, raw, code string) (*User, error) {
	st := s.settings(ctx)
	if !bootstrapAllowed(u) {
		return nil, ErrEmailBootstrapNotAllowed
	}
	email, err := s.checkNewEmail(ctx, st, raw, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	upd := map[string]any{"email": email, "email_source": EmailSourceUserSet, "email_bootstrap_used_at": now}
	if st.EmailVerificationEnabled {
		if err := s.verifyCode(ctx, u.ID, PurposeEmailBootstrapNew, email, code); err != nil {
			s.event(ctx, eventInput{Type: "email_bootstrap", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
			return nil, err
		}
		upd["email_verified_at"] = now
	}
	if err := s.conn(ctx).Model(&User{}).Where("id = ?", u.ID).Updates(upd).Error; err != nil {
		return nil, err
	}
	s.event(ctx, eventInput{Type: "email_bootstrap", Result: resultSuccess, UserID: u.ID})
	return s.userByID(ctx, u.ID)
}

// EmailVerifyCurrentStart mails a code to the account's unverified address.
func (s *Service) EmailVerifyCurrentStart(ctx context.Context, u *User) (startResult, error) {
	if !s.settings(ctx).EmailVerificationEnabled {
		return noneStart(), nil
	}
	if u.Email == "" || isSet(u.EmailVerifiedAt) {
		return startResult{}, ErrEmailNotVerified
	}
	email, err := normalizeEmail(u.Email)
	if err != nil {
		return startResult{}, err
	}
	exp, err := s.sendCode(ctx, u.ID, PurposeEmailVerifyCurrent, email)
	if err != nil {
		s.event(ctx, eventInput{Type: "email_verify_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	s.event(ctx, eventInput{Type: "email_verify_code", Result: resultSuccess, UserID: u.ID})
	return emailStart(exp), nil
}

// EmailVerifyCurrentComplete marks the account's address verified.
func (s *Service) EmailVerifyCurrentComplete(ctx context.Context, u *User, code string) (*User, error) {
	if !s.settings(ctx).EmailVerificationEnabled {
		return nil, ErrEmailVerificationDisabled
	}
	if u.Email == "" || isSet(u.EmailVerifiedAt) {
		return nil, ErrEmailNotVerified
	}
	email, err := normalizeEmail(u.Email)
	if err != nil {
		return nil, err
	}
	if err := s.verifyCode(ctx, u.ID, PurposeEmailVerifyCurrent, email, code); err != nil {
		s.event(ctx, eventInput{Type: "email_verify", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return nil, err
	}
	if err := s.conn(ctx).Model(&User{}).Where("id = ?", u.ID).Update("email_verified_at", s.now()).Error; err != nil {
		return nil, err
	}
	s.event(ctx, eventInput{Type: "email_verify", Result: resultSuccess, UserID: u.ID})
	return s.userByID(ctx, u.ID)
}

// EmailChangeStartCurrent starts proof of control of the current identity.
func (s *Service) EmailChangeStartCurrent(ctx context.Context, u *User, method string) (startResult, error) {
	res, err := s.startVerification(ctx, u, PurposeEmailChangeCurrent, method)
	if err != nil {
		s.event(ctx, eventInput{Type: "email_change_current_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	if res.Sent {
		s.event(ctx, eventInput{Type: "email_change_current_code", Result: resultSuccess, UserID: u.ID})
	}
	return res, nil
}

// EmailChangeStartNew mails a code to the new address.
func (s *Service) EmailChangeStartNew(ctx context.Context, u *User, raw string) (startResult, error) {
	st := s.settings(ctx)
	email, err := s.checkNewEmail(ctx, st, raw, u.ID)
	if err != nil {
		return startResult{}, err
	}
	if !st.EmailVerificationEnabled {
		return noneStart(), nil
	}
	exp, err := s.sendCode(ctx, u.ID, PurposeEmailChangeNew, email)
	if err != nil {
		s.event(ctx, eventInput{Type: "email_change_new_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	s.event(ctx, eventInput{Type: "email_change_new_code", Result: resultSuccess, UserID: u.ID})
	return emailStart(exp), nil
}

type emailChangeInput struct {
	Email         string
	CurrentMethod string
	CurrentCode   string
	NewCode       string
}

// EmailChangeComplete replaces the account's address.
func (s *Service) EmailChangeComplete(ctx context.Context, u *User, in emailChangeInput) (*User, error) {
	st := s.settings(ctx)
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if email == u.Email {
		return nil, ErrEmailUnchanged
	}
	if email, err = s.checkNewEmail(ctx, st, in.Email, u.ID); err != nil {
		return nil, err
	}
	avail, err := s.availableMethods(ctx, u)
	if err != nil {
		return nil, err
	}
	method, err := pickMethod(in.CurrentMethod, avail)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*User, error) {
		s.event(ctx, eventInput{Type: "email_change", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return nil, err
	}
	if err := s.checkMethod(ctx, u, PurposeEmailChangeCurrent, method, in.CurrentCode); err != nil {
		return fail(err)
	}
	now := s.now()
	upd := map[string]any{"email": email, "email_source": EmailSourceUserSet, "email_verified_at": nil}
	if st.EmailVerificationEnabled {
		if err := s.verifyCode(ctx, u.ID, PurposeEmailChangeNew, email, in.NewCode); err != nil {
			return fail(err)
		}
		upd["email_verified_at"] = now
	}
	if err := s.conn(ctx).Model(&User{}).Where("id = ?", u.ID).Updates(upd).Error; err != nil {
		return nil, err
	}
	s.event(ctx, eventInput{Type: "email_change", Result: resultSuccess, UserID: u.ID})
	return s.userByID(ctx, u.ID)
}

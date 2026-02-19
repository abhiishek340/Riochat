package auth

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

// lockedError is auth.account_locked carrying the seconds to wait; the HTTP
// layer turns it into a Retry-After header. Zero means the unlock time is not
// known (an administrator lock).
type lockedError struct{ retryAfter int }

// Reasons recorded on login failure events. A wrong password, an unknown
// user, a disabled login method and an inactive account share one reason so
// the event never tells them apart.
const (
	reasonInvalidCredentials = "invalid_credentials_or_inactive"
	reasonAccountLocked      = "account_locked"
)

func (e *lockedError) Error() string { return ErrAccountLocked.Error() }
func (e *lockedError) Unwrap() error { return ErrAccountLocked }

func retryAfterSeconds(until *time.Time, now time.Time) int {
	if until == nil || !until.After(now) {
		return 0
	}
	return int(math.Ceil(until.Sub(now).Seconds()))
}

// lockoutEnabled reports whether failed attempts can lock accounts.
func lockoutEnabled(st Settings) bool { return st.LoginMaxFailures > 0 && st.LoginLockMinutes > 0 }

// resetFailures clears the failure counter and any lock, reactivating a user
// whose status was locked.
func (s *Service) resetFailures(ctx context.Context, tx *gorm.DB, u *User, c *Credential) error {
	if err := tx.Model(&Credential{}).Where("id = ?", c.ID).
		Updates(map[string]any{"failed_login_count": 0, "locked_until": nil}).Error; err != nil {
		return err
	}
	c.FailedLoginCount, c.LockedUntil = 0, nil
	if u.Status == StatusLocked {
		if err := tx.Model(&User{}).Where("id = ?", u.ID).Update("status", StatusActive).Error; err != nil {
			return err
		}
		u.Status = StatusActive
	}
	return nil
}

// registerFailure counts a wrong credential. It reports whether this very
// failure put the account under lock.
func (s *Service) registerFailure(ctx context.Context, u *User, c *Credential, st Settings) (newlyLocked bool, until *time.Time, err error) {
	err = s.conn(ctx).Model(&Credential{}).Where("id = ?", c.ID).
		UpdateColumn("failed_login_count", gorm.Expr("failed_login_count + 1")).Error
	if err != nil {
		return false, nil, err
	}
	var fresh Credential
	if err = s.conn(ctx).First(&fresh, c.ID).Error; err != nil {
		return false, nil, err
	}
	c.FailedLoginCount = fresh.FailedLoginCount
	if !lockoutEnabled(st) || fresh.FailedLoginCount < st.LoginMaxFailures {
		return false, nil, nil
	}
	t := s.now().Add(time.Duration(st.LoginLockMinutes) * time.Minute)
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Credential{}).Where("id = ?", c.ID).Update("locked_until", t).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", u.ID).Update("status", StatusLocked).Error
	})
	if err != nil {
		return false, nil, err
	}
	c.LockedUntil, u.Status = &t, StatusLocked
	return true, &t, nil
}

// Login performs the first factor of a credential sign-in. It returns either a
// session or a second-factor challenge.
func (s *Service) Login(ctx context.Context, identifier, password string) (signIn, error) {
	st := s.settings(ctx)
	ident := strings.TrimSpace(identifier)
	// Failed attempts are never attributed to an account: the attempted name is
	// kept in the detail only, so filtering events by user shows successes alone.
	failInvalid := func(_ uint, reason string) (signIn, error) {
		s.event(ctx, eventInput{Type: "login", Result: resultFailure, Reason: reason,
			Detail: map[string]any{"username": identifier}})
		return signIn{}, ErrInvalidCredentials
	}

	byEmail := strings.Contains(ident, "@")
	if (byEmail && !st.EmailLoginEnabled) || (!byEmail && !st.UsernameLoginEnabled) {
		burnPasswordCheck(password)
		return failInvalid(0, reasonInvalidCredentials)
	}
	var u User
	col := "username"
	if byEmail {
		col = "email"
	}
	if err := s.conn(ctx).Where(col+" = ?", ident).First(&u).Error; err != nil {
		if database.IsNotFound(err) {
			burnPasswordCheck(password)
			return failInvalid(0, reasonInvalidCredentials)
		}
		return signIn{}, err
	}
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return signIn{}, err
	}
	if cred == nil || !cred.PasswordEnabled {
		burnPasswordCheck(password)
		return failInvalid(u.ID, reasonInvalidCredentials)
	}
	now := s.now()
	if cred.LockedUntil != nil && !cred.LockedUntil.After(now) {
		if err := s.resetFailures(ctx, s.conn(ctx), &u, cred); err != nil {
			return signIn{}, err
		}
	}
	locked := u.Status == StatusLocked || (cred.LockedUntil != nil && cred.LockedUntil.After(now))
	if locked {
		if u.Status != StatusLocked {
			if err := s.conn(ctx).Model(&User{}).Where("id = ?", u.ID).Update("status", StatusLocked).Error; err != nil {
				return signIn{}, err
			}
			u.Status = StatusLocked
		}
		if checkPassword(cred.PasswordHash, password) {
			s.event(ctx, eventInput{Type: "login", Result: resultFailure, Reason: reasonAccountLocked,
				Detail: map[string]any{"username": identifier}})
			return signIn{}, &lockedError{retryAfter: retryAfterSeconds(cred.LockedUntil, now)}
		}
		return failInvalid(u.ID, reasonInvalidCredentials)
	}
	if u.Status != StatusActive {
		burnPasswordCheck(password)
		return failInvalid(u.ID, reasonInvalidCredentials)
	}
	if !checkPassword(cred.PasswordHash, password) {
		if _, _, err := s.registerFailure(ctx, &u, cred, st); err != nil {
			return signIn{}, err
		}
		return failInvalid(u.ID, reasonInvalidCredentials)
	}
	if cred.FailedLoginCount > 0 || cred.LockedUntil != nil {
		if err := s.resetFailures(ctx, s.conn(ctx), &u, cred); err != nil {
			return signIn{}, err
		}
	}
	mfa, err := s.mfaOf(ctx, u.ID)
	if err != nil {
		return signIn{}, err
	}
	if totpActive(mfa) {
		out, err := s.buildChallenge(ctx, &u)
		if err != nil {
			return signIn{}, err
		}
		s.event(ctx, eventInput{Type: "login", Result: resultChallenge, Reason: "second factor required", UserID: u.ID,
			Detail: map[string]any{"username": identifier}})
		return out, nil
	}
	out, sess, err := s.newSession(ctx, &u)
	if err != nil {
		return signIn{}, err
	}
	detail := sessionDetail(sess)
	detail["username"] = identifier
	s.event(ctx, eventInput{Type: "login", Result: resultSuccess, UserID: u.ID, Detail: detail})
	return out, nil
}

// ---- second-factor challenge --------------------------------------------------------

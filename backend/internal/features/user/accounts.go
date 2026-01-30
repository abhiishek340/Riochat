package user

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// bcryptCost is the work factor of stored password hashes. It is a variable so
// that tests can lower it; production always uses 12.
var bcryptCost = 12

// maxRevokeReason is the stored length of a session revoke reason.
const maxRevokeReason = 128

// Accounts is the storage-level API for user accounts. The admin and auth
// areas call it; it enforces storage guarantees (atomicity, uniqueness, the
// last-superadmin guard) and the field rules of validate.go where the spec
// assigns validation to this layer.
type Accounts struct {
	db     *gorm.DB
	clock  clock.Clock
	images ImageChecker
}

// NewAccounts builds the service. images may be nil, which makes every
// "file:" avatar update fail with ErrInvalidAvatarURL.
func NewAccounts(db *gorm.DB, clk clock.Clock, images ImageChecker) *Accounts {
	return &Accounts{db: db, clock: clk, images: images}
}

func (a *Accounts) now() time.Time { return a.clock.Now().UTC() }

func (a *Accounts) conn(ctx context.Context) *gorm.DB { return a.db.WithContext(ctx) }

// newPublicID returns a fresh 32-character lowercase hex id.
func newPublicID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// hashPassword hashes an already normalized password with bcrypt. bcrypt reads
// only 72 bytes, so longer inputs are first folded through SHA-256; this is the
// same scheme the auth area verifies logins with.
func hashPassword(plain string) (string, error) {
	in := []byte(plain)
	if len(in) > 72 {
		sum := sha256.Sum256(in)
		in = []byte(base64.StdEncoding.EncodeToString(sum[:]))
	}
	h, err := bcrypt.GenerateFromPassword(in, bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// translateWrite maps a unique-constraint violation to the matching outcome.
// Drivers do not always say which constraint failed, so the driver text is
// consulted first and fallback (the only unique column the statement can break)
// decides otherwise; nil fallback means the generic duplicate outcome.
func translateWrite(err error, fallback error) error {
	if err == nil {
		return nil
	}
	err = database.Translate(err)
	if !database.IsDuplicate(err) {
		return err
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "username"):
		return ErrUsernameExists.WithCause(err)
	case strings.Contains(msg, "provider_subject"), strings.Contains(msg, "identity_user_links"):
		return ErrDuplicateIdentity
	}
	if fallback != nil {
		if te, ok := fallback.(interface{ WithCause(error) *apperr.Error }); ok {
			return te.WithCause(err)
		}
		return fallback
	}
	return ErrDuplicate
}

// ---- lookups ----------------------------------------------------------------

// GetByID returns the live account with the internal id.
func (a *Accounts) GetByID(ctx context.Context, id uint) (*Account, error) {
	return a.getBy(a.conn(ctx), "id = ?", id)
}

// GetByPublicID returns the live account with the public id. A blank id is not
// found.
func (a *Accounts) GetByPublicID(ctx context.Context, publicID string) (*Account, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return nil, ErrNotFound
	}
	return a.getBy(a.conn(ctx), "public_id = ?", publicID)
}

func (a *Accounts) getBy(db *gorm.DB, query string, arg any) (*Account, error) {
	var acct Account
	if err := db.Where(query, arg).Take(&acct).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &acct, nil
}

// CountSuperadmins counts live superadmin accounts.
func (a *Accounts) CountSuperadmins(ctx context.Context) (int64, error) {
	return countSuperadmins(a.conn(ctx))
}

func countSuperadmins(db *gorm.DB) (int64, error) {
	var n int64
	err := db.Model(&Account{}).Where("role = ?", RoleSuperAdmin).Count(&n).Error
	return n, err
}

// UsersByEmails looks accounts up by email, ignoring case. The input is
// trimmed and lowercased, blanks dropped and duplicates collapsed; the result
// is keyed by the lowercase trimmed email. When several accounts share an
// email one of them is returned.
func (a *Accounts) UsersByEmails(ctx context.Context, emails []string) (map[string]Account, error) {
	seen := map[string]struct{}{}
	wanted := make([]string, 0, len(emails))
	for _, e := range emails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		wanted = append(wanted, e)
	}
	out := map[string]Account{}
	if len(wanted) == 0 {
		return out, nil
	}
	var rows []Account
	if err := a.conn(ctx).Where("LOWER(email) IN ?", wanted).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		k := strings.ToLower(strings.TrimSpace(r.Email))
		if _, taken := out[k]; !taken {
			out[k] = r
		}
	}
	return out, nil
}

// AllUsernames returns every username in use, soft-deleted accounts included,
// because the storage uniqueness rule covers them too.
func (a *Accounts) AllUsernames(ctx context.Context) (map[string]struct{}, error) {
	var names []string
	if err := a.conn(ctx).Unscoped().Model(&Account{}).Pluck("username", &names).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out, nil
}

// LatestSessionActivity returns, per user, the time of the most recent session:
// last-seen falling back to issued-at. Revoked and expired sessions count;
// users without sessions are absent.
func (a *Accounts) LatestSessionActivity(ctx context.Context, userIDs []uint) (map[uint]time.Time, error) {
	out := map[uint]time.Time{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []sessionRow
	err := a.conn(ctx).Select("id", "user_id", "issued_at", "last_seen_at", "created_at").
		Where("user_id IN ?", userIDs).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		at := r.IssuedAt
		if r.LastSeenAt != nil {
			at = *r.LastSeenAt
		}
		if cur, ok := out[r.UserID]; !ok || at.After(cur) {
			out[r.UserID] = at
		}
	}
	return out, nil
}

// ---- simple mutations ------------------------------------------------------

// SetStatus writes a status. There is no validation and no existence check: an
// unknown id is a silent no-op.
func (a *Accounts) SetStatus(ctx context.Context, id uint, status string) error {
	return a.conn(ctx).Model(&Account{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": a.now()}).Error
}

// Unlock clears the failed-login counter and the lock of a user's credential.
// It does nothing when the user has no credential.
func (a *Accounts) Unlock(ctx context.Context, userID uint) error {
	return a.conn(ctx).Model(&Credential{}).Where("user_id = ?", userID).
		Updates(map[string]any{"failed_login_count": 0, "locked_until": nil, "updated_at": a.now()}).Error
}

// ResetPassword sets a new password on behalf of an administrator. Sessions are
// not revoked here; callers combine it with RevokeAllSessions when needed.
func (a *Accounts) ResetPassword(ctx context.Context, userID uint, newPassword string, mustReset bool) error {
	pw, err := NormalizePassword(newPassword)
	if err != nil {
		return ErrInvalidPassword.WithCause(err)
	}
	hash, err := hashPassword(pw)
	if err != nil {
		return err
	}
	now := a.now()
	res := a.conn(ctx).Model(&Credential{}).Where("user_id = ?", userID).Updates(map[string]any{
		"password_hash":       hash,
		"password_algo":       "bcrypt",
		"password_enabled":    true,
		"password_updated_at": now,
		"password_set_at":     now,
		"password_origin":     PasswordOriginAdminReset,
		"must_reset_password": mustReset,
		"failed_login_count":  0,
		"locked_until":        nil,
		"updated_at":          now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAllSessions revokes every session of the user that is not revoked yet.
// Sessions that were already revoked keep their original stamp and reason.
func (a *Accounts) RevokeAllSessions(ctx context.Context, userID uint, reason string) error {
	if r := []rune(reason); len(r) > maxRevokeReason {
		reason = string(r[:maxRevokeReason])
	}
	now := a.now()
	return a.conn(ctx).Model(&sessionRow{}).Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{"revoked_at": now, "revoke_reason": reason, "updated_at": now}).Error
}

// ---- auth events -------------------------------------------------------------

// RecordAuthEvent stores an event with occurred-at set to now.
func (a *Accounts) RecordAuthEvent(ctx context.Context, ev AuthEvent) error {
	ev.ID = 0
	ev.OccurredAt = a.now()
	return a.conn(ctx).Create(&ev).Error
}

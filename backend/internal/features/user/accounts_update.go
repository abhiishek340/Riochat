package user

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Opt is an optional field of a partial update. The zero value means "not
// provided"; Some(v) provides v, which may be the empty string or nil.
type Opt[T any] struct {
	Set bool
	Val T
}

// Some marks v as provided.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Val: v} }

// UpdateFields lists the fields Update can change. Only provided fields are
// written.
type UpdateFields struct {
	AvatarURL             Opt[string]
	DisplayName           Opt[string]
	Email                 Opt[string]
	EmailVerifiedAt       Opt[*time.Time]
	EmailSource           Opt[string]
	EmailBootstrapUsedAt  Opt[*time.Time]
	Phone                 Opt[string]
	PhoneVerifiedAt       Opt[*time.Time]
	Role                  Opt[string]
	Timezone              Opt[string]
	Locale                Opt[string]
	ProfilePreferences    Opt[string]
	AppearancePreferences Opt[string]
	OnboardingCompletedAt Opt[*time.Time]
}

func (f UpdateFields) columns(avatar string) map[string]any {
	m := map[string]any{}
	put := func(on bool, col string, v any) {
		if on {
			m[col] = v
		}
	}
	put(f.AvatarURL.Set, "avatar_url", avatar)
	put(f.DisplayName.Set, "display_name", f.DisplayName.Val)
	put(f.Email.Set, "email", f.Email.Val)
	put(f.EmailVerifiedAt.Set, "email_verified_at", f.EmailVerifiedAt.Val)
	put(f.EmailSource.Set, "email_source", f.EmailSource.Val)
	put(f.EmailBootstrapUsedAt.Set, "email_bootstrap_used_at", f.EmailBootstrapUsedAt.Val)
	put(f.Phone.Set, "phone", f.Phone.Val)
	put(f.PhoneVerifiedAt.Set, "phone_verified_at", f.PhoneVerifiedAt.Val)
	put(f.Role.Set, "role", f.Role.Val)
	put(f.Timezone.Set, "timezone", f.Timezone.Val)
	put(f.Locale.Set, "locale", f.Locale.Val)
	put(f.ProfilePreferences.Set, "profile_preferences", f.ProfilePreferences.Val)
	put(f.AppearancePreferences.Set, "appearance_preferences", f.AppearancePreferences.Val)
	put(f.OnboardingCompletedAt.Set, "onboarding_completed_at", f.OnboardingCompletedAt.Val)
	return m
}

// Update writes the provided fields of one account and returns the refreshed
// row. Only the avatar is validated here; every other value is stored as given.
//
// Setting a role other than superadmin on the only superadmin is refused with
// ErrLastSuperadmin, atomically with respect to concurrent updates.
func (a *Accounts) Update(ctx context.Context, id uint, f UpdateFields) (*Account, error) {
	cols := f.columns(strings.TrimSpace(f.AvatarURL.Val))
	if len(cols) == 0 {
		return a.GetByID(ctx, id)
	}

	var avatarUpload string
	if f.AvatarURL.Set {
		avatar := strings.TrimSpace(f.AvatarURL.Val)
		if !ValidAvatarRef(avatar) {
			return nil, ErrInvalidAvatarURL
		}
		if strings.HasPrefix(avatar, AvatarFilePrefix) {
			if a.images == nil {
				return nil, ErrInvalidAvatarURL
			}
			avatarUpload, _ = UploadIDFromAvatarRef(avatar)
		}
	}
	cols["updated_at"] = a.now()

	var out *Account
	err := a.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if f.Role.Set && f.Role.Val != RoleSuperAdmin {
			if err := lockSuperadmins(tx); err != nil {
				return err
			}
		}
		var cur Account
		if err := tx.Where("id = ?", id).Take(&cur).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		if f.Role.Set && f.Role.Val != RoleSuperAdmin && cur.Role == RoleSuperAdmin {
			n, err := countSuperadmins(tx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastSuperadmin
			}
		}
		if avatarUpload != "" {
			ok, err := a.images.IsActiveImage(ctx, tx, id, avatarUpload)
			if err != nil {
				return err
			}
			if !ok {
				return ErrInvalidAvatarURL
			}
		}
		if err := tx.Model(&Account{}).Where("id = ?", id).Updates(cols).Error; err != nil {
			return translateWrite(err, nil)
		}
		var fresh Account
		if err := tx.Where("id = ?", id).Take(&fresh).Error; err != nil {
			return err
		}
		out = &fresh
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// lockSuperadmins takes a write lock on every superadmin row so that two
// demotions cannot both observe "two superadmins". The statement rewrites
// updated_at with its own value, which holds the row (PostgreSQL) or the
// database write lock (SQLite) until the transaction ends.
func lockSuperadmins(tx *gorm.DB) error {
	return tx.Exec("UPDATE identity_users SET updated_at = updated_at WHERE role = ? AND deleted_at IS NULL", RoleSuperAdmin).Error
}

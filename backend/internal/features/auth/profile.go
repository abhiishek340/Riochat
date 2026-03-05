package auth

import (
	"context"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

type profileInput struct {
	AvatarURL             *string
	DisplayName           *string
	Timezone              *string
	Locale                *string
	ProfilePreferences    *string
	AppearancePreferences *string
}

// UpdateProfile applies the supplied profile fields and returns the names of the
// fields it was asked to change.
func (s *Service) UpdateProfile(ctx context.Context, userID uint, in profileInput) (*User, []string, error) {
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	upd := map[string]any{}
	var fields []string

	if v, ok := trimmedPtr(in.AvatarURL); ok {
		shape, fileID := avatarShape(v)
		if !shape {
			return nil, nil, ErrInvalidAvatarURL
		}
		if fileID != "" && (s.h.AvatarFiles == nil || !s.h.AvatarFiles.IsOwnedImage(ctx, userID, fileID)) {
			return nil, nil, ErrInvalidAvatarURL
		}
		upd["avatar_url"] = v
		fields = append(fields, "avatar_url")
	}
	if v, ok := trimmedPtr(in.DisplayName); ok {
		if n := utf8.RuneCountInString(v); n < 3 || n > 16 {
			return nil, nil, ErrInvalidDisplayName
		}
		upd["display_name"] = v
		fields = append(fields, "display_name")
	}
	if v, ok := trimmedPtr(in.Timezone); ok {
		if v == "" {
			v = "Etc/UTC"
		}
		if !validTimeZone(v) {
			return nil, nil, ErrInvalidTimeZone
		}
		upd["timezone"] = v
		fields = append(fields, "timezone")
	}
	if v, ok := trimmedPtr(in.Locale); ok {
		loc, err := normalizeLocale(v)
		if err != nil {
			return nil, nil, err
		}
		upd["locale"] = loc
		fields = append(fields, "locale")
	}
	if v, ok := trimmedPtr(in.ProfilePreferences); ok {
		upd["profile_preferences"] = v
		fields = append(fields, "profile_preferences")
	}
	if v, ok := trimmedPtr(in.AppearancePreferences); ok {
		norm, err := normalizeAppearance(v)
		if err != nil {
			return nil, nil, err
		}
		upd["appearance_preferences"] = norm
		fields = append(fields, "appearance_preferences")
	}
	if len(upd) > 0 {
		if err := s.conn(ctx).Model(&User{}).Where("id = ?", userID).Updates(upd).Error; err != nil {
			return nil, nil, err
		}
		if u, err = s.userByID(ctx, userID); err != nil {
			return nil, nil, err
		}
	}
	return u, fields, nil
}

// UpdateUsername performs the one self-service username change.
func (s *Service) UpdateUsername(ctx context.Context, userID uint, raw string) (*User, error) {
	name, err := normalizeUsername(raw)
	if err != nil {
		return nil, err
	}
	current, err := s.userByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.initialUsernameRequired(current) && name == current.Username {
		return nil, ErrUsernameChangeRequired
	}
	now := s.now()
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return database.Translate(err)
		}
		if u.UsernameChangedAt != nil {
			return ErrUsernameChangeUsed
		}
		if name == u.Username {
			return tx.Model(&User{}).Where("id = ?", u.ID).Update("username_changed_at", now).Error
		}
		var n int64
		if err := tx.Model(&User{}).Where("LOWER(username) = ? AND id <> ?", strings.ToLower(name), u.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrUsernameExists
		}
		err := tx.Model(&User{}).Where("id = ?", u.ID).
			Updates(map[string]any{"username": name, "username_changed_at": now}).Error
		if database.IsDuplicate(err) {
			return ErrUsernameExists
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.userByID(ctx, userID)
}

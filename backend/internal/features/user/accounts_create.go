package user

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	billingModePeriod = "period"
	planCodeFree      = "free"
	intervalLifetime  = "lifetime"
)

// CreateInput is what an administrator supplies to create a normal user.
type CreateInput struct {
	Username              string
	Password              string
	AvatarURL             string
	DisplayName           string
	Email                 string
	Phone                 string
	Timezone              string
	Locale                string
	BillingMode           string
	SubscriptionTier      string
	SubscriptionExpiresAt *time.Time
}

// plannedSubscription is a subscription resolved before the transaction opens.
type plannedSubscription struct {
	planID, priceID uint
	end             *time.Time
	autoRenew       bool
}

// Create makes a normal, active user together with its credential and, when
// requested, a subscription, all in one transaction. Validation runs in the
// documented order and the first failure wins.
func (a *Accounts) Create(ctx context.Context, in CreateInput) (*Account, error) {
	username, err := NormalizeUsername(in.Username)
	if err != nil {
		return nil, err
	}
	var taken int64
	if err := a.conn(ctx).Model(&Account{}).Where("username = ?", username).Count(&taken).Error; err != nil {
		return nil, err
	}
	if taken > 0 {
		return nil, ErrUsernameExists
	}
	password, err := NormalizePassword(in.Password)
	if err != nil {
		return nil, err
	}
	avatar := strings.TrimSpace(in.AvatarURL)
	if !ValidAvatarRef(avatar) || strings.HasPrefix(avatar, AvatarFilePrefix) {
		return nil, ErrInvalidAvatarURL
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	if display, err = NormalizeDisplayName(display); err != nil {
		return nil, err
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return nil, err
	}
	timezone, err := NormalizeTimezone(in.Timezone)
	if err != nil {
		return nil, err
	}
	locale, err := NormalizeLocale(in.Locale)
	if err != nil {
		return nil, err
	}

	now := a.now()
	sub, err := a.planSubscription(ctx, in, now)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	acct := Account{
		PublicID: newPublicID(), Username: username, DisplayName: display, AvatarURL: avatar,
		Email: email, Phone: phone, Role: RoleUser, Status: StatusActive,
		Timezone: timezone, Locale: locale, EmailSource: EmailSourceAdminSet,
	}
	err = a.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&acct).Error; err != nil {
			return translateWrite(err, ErrUsernameExists)
		}
		cred := Credential{
			UserID: acct.ID, PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: true,
			PasswordUpdatedAt: &now, PasswordSetAt: &now, PasswordOrigin: PasswordOriginAdminCreated,
		}
		if err := tx.Create(&cred).Error; err != nil {
			return err
		}
		if sub != nil {
			row := subscriptionRow{
				UserID: acct.ID, PlanID: sub.planID, PriceID: sub.priceID, Status: "active",
				StartAt: now, CurrentPeriodStartAt: now, CurrentPeriodEndAt: sub.end, AutoRenew: sub.autoRenew,
			}
			return tx.Create(&row).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &acct, nil
}

// planSubscription resolves the subscription to create, or nil for none.
func (a *Accounts) planSubscription(ctx context.Context, in CreateInput, now time.Time) (*plannedSubscription, error) {
	if strings.ToLower(strings.TrimSpace(in.BillingMode)) != billingModePeriod {
		return nil, nil
	}
	tier := strings.ToLower(strings.TrimSpace(in.SubscriptionTier))
	if tier == "" {
		tier = planCodeFree
	}
	var plan planRow
	if err := a.conn(ctx).Where("code = ? AND is_active = ?", tier, true).Take(&plan).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrInvalidSubscriptionTier
		}
		return nil, err
	}
	var price priceRow
	if err := a.conn(ctx).Where("plan_id = ? AND is_active = ? AND is_default = ?", plan.ID, true, true).
		Order("id ASC").Take(&price).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrInvalidSubscriptionTier
		}
		return nil, err
	}
	planned := &plannedSubscription{planID: plan.ID, priceID: price.ID}
	if tier == planCodeFree {
		planned.autoRenew = price.BillingInterval != intervalLifetime
		return planned, nil
	}
	if in.SubscriptionExpiresAt == nil || in.SubscriptionExpiresAt.IsZero() {
		return nil, ErrSubscriptionExpiryMissing
	}
	end := in.SubscriptionExpiresAt.UTC()
	if !end.After(now) {
		return nil, ErrInvalidSubscriptionExpiry
	}
	planned.end = &end
	return planned, nil
}

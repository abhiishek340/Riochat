package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// normalizeRedemptionCode trims and upper-cases a code.
func normalizeRedemptionCode(code string) string { return upper(strings.TrimSpace(code)) }

func validRedemptionCode(code string) bool {
	if len(code) < 3 || len(code) > 64 {
		return false
	}
	for _, r := range code {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func codeHint(code string) string {
	if len(code) <= 8 {
		return "****"
	}
	return code[:4] + "***" + code[len(code)-4:]
}

// randomRedemptionCode returns a UUID-v4-shaped upper-case code.
func randomRedemptionCode() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := upper(hex.EncodeToString(b[:]))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// CodeView is the API form of a redemption code.
type CodeView struct {
	ID                   uint       `json:"id"`
	Code                 string     `json:"code,omitempty"`
	CodeHint             string     `json:"codeHint"`
	Mode                 string     `json:"mode"`
	RewardType           string     `json:"rewardType"`
	CreditUSD            float64    `json:"creditUSD"`
	CreditNanousd        int64      `json:"creditNanousd"`
	PlanID               uint       `json:"planID"`
	DurationDays         int        `json:"durationDays"`
	MaxRedemptions       *int       `json:"maxRedemptions"`
	PerUserLimit         int        `json:"perUserLimit"`
	RedeemedCount        int        `json:"redeemedCount"`
	RemainingRedemptions *int       `json:"remainingRedemptions"`
	Status               string     `json:"status"`
	ExpiresAt            *time.Time `json:"expiresAt"`
	Description          string     `json:"description"`
	CreatedByUserID      uint       `json:"createdByUserID"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

func codeView(c RedemptionCode, plaintext string) CodeView {
	v := CodeView{ID: c.ID, Code: plaintext, CodeHint: c.CodeHint, Mode: c.Mode, RewardType: c.RewardType, CreditUSD: NanoToUSD(c.CreditNanousd),
		CreditNanousd: c.CreditNanousd, PlanID: c.PlanID, DurationDays: c.DurationDays, MaxRedemptions: c.MaxRedemptions,
		PerUserLimit: c.PerUserLimit, RedeemedCount: c.RedeemedCount, Status: c.Status, ExpiresAt: c.ExpiresAt, Description: c.Description,
		CreatedByUserID: c.CreatedByUserID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.MaxRedemptions != nil {
		rem := *c.MaxRedemptions - c.RedeemedCount
		if rem < 0 {
			rem = 0
		}
		v.RemainingRedemptions = &rem
	}
	return v
}

// CreateCodesInput is the administrator's batch creation request.
type CreateCodesInput struct {
	Code           string     `json:"code" binding:"omitempty,min=3,max=64"`
	Quantity       int        `json:"quantity" binding:"max=100"`
	Mode           string     `json:"mode" binding:"oneof=usage period"`
	CreditUSD      float64    `json:"creditUSD" binding:"gte=0"`
	PlanID         uint       `json:"planID"`
	DurationDays   int        `json:"durationDays" binding:"max=3660"`
	MaxRedemptions *int       `json:"maxRedemptions" binding:"omitempty,min=1"`
	PerUserLimit   int        `json:"perUserLimit" binding:"max=100"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	Description    string     `json:"description" binding:"max=255"`
}

// CreateCodes validates and creates redemption codes. The plaintext is only
// returned here.
func (s *Service) CreateCodes(ctx context.Context, actor uint, in CreateCodesInput) ([]CodeView, error) {
	if s.secrets == nil {
		return nil, ErrRedemptionSecret
	}
	manual := normalizeRedemptionCode(in.Code)
	if strings.TrimSpace(in.Code) != "" && !validRedemptionCode(manual) {
		return nil, invalidRedemption("code", "code_format")
	}
	qty := in.Quantity
	if qty == 0 {
		qty = 1
	}
	if qty < 1 || qty > 100 {
		return nil, invalidRedemption("quantity", "quantity")
	}
	if manual != "" && qty != 1 {
		return nil, invalidRedemption("quantity", "manual_quantity")
	}
	if in.MaxRedemptions != nil && *in.MaxRedemptions <= 0 {
		return nil, invalidRedemption("maxRedemptions", "max_redemptions")
	}
	perUser := in.PerUserLimit
	if perUser == 0 {
		perUser = 1
	}
	if perUser < 1 || perUser > 100 {
		return nil, invalidRedemption("perUserLimit", "per_user_limit")
	}
	var max *int
	if in.MaxRedemptions != nil {
		v := *in.MaxRedemptions
		max = &v
	} else if manual == "" {
		one := 1
		max = &one
	}
	if max != nil && perUser > *max {
		return nil, invalidRedemption("perUserLimit", "limit_relationship")
	}
	now := s.now()
	var expires *time.Time
	if in.ExpiresAt != nil {
		if !in.ExpiresAt.After(now) {
			return nil, invalidRedemption("expiresAt", "expires_at")
		}
		expires = utcPtr(*in.ExpiresAt)
	}
	row := RedemptionCode{Mode: in.Mode, PerUserLimit: perUser, Status: CodeActive, ExpiresAt: expires,
		Description: strings.TrimSpace(in.Description), CreatedByUserID: actor}
	if len(row.Description) > 255 {
		return nil, invalidRedemption("description", "description")
	}
	switch in.Mode {
	case ModeUsage:
		if in.CreditUSD <= 0 || math.IsNaN(in.CreditUSD) || math.IsInf(in.CreditUSD, 0) || USDToNano(in.CreditUSD) <= 0 {
			return nil, invalidRedemption("creditUSD", "credit")
		}
		row.RewardType, row.CreditNanousd = RewardBalance, USDToNano(in.CreditUSD)
	case ModePeriod:
		var plan Plan
		if in.PlanID == 0 || s.db.WithContext(ctx).First(&plan, in.PlanID).Error != nil || !plan.IsActive || plan.Code == PlanFree {
			return nil, invalidRedemption("planID", "plan")
		}
		if in.DurationDays <= 0 || in.DurationDays > 3660 {
			return nil, invalidRedemption("durationDays", "duration")
		}
		row.RewardType, row.PlanID, row.DurationDays = RewardSubscription, plan.ID, in.DurationDays
	default:
		return nil, invalidRedemption("mode", "mode")
	}

	var created []CodeView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := 0; i < qty; i++ {
			var plaintext string
			for attempt := 0; ; attempt++ {
				plaintext = manual
				if plaintext == "" {
					p, err := randomRedemptionCode()
					if err != nil {
						return err
					}
					plaintext = p
				}
				enc, err := s.secrets.Encrypt(plaintext)
				if err != nil {
					return ErrRedemptionSecret.WithCause(err)
				}
				c := row
				c.CodeHash, c.CodeEncrypted, c.CodeHint, c.MaxRedemptions = s.secrets.LookupHash(plaintext), enc, codeHint(plaintext), max
				if c.CodeHash == "" {
					return ErrRedemptionSecret
				}
				var n int64
				if err := tx.Model(&RedemptionCode{}).Where("code_hash IN ?", s.secrets.LookupHashes(plaintext)).Count(&n).Error; err != nil {
					return err
				}
				if n == 0 {
					if err := tx.Create(&c).Error; err != nil {
						if database.IsDuplicate(err) {
							n = 1
						} else {
							return err
						}
					}
				}
				if n > 0 {
					if manual != "" || attempt >= 5 {
						return ErrRedemptionConflict
					}
					continue
				}
				created = append(created, codeView(c, plaintext))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

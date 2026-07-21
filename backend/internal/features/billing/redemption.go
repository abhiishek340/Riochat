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

// CodeListQuery filters the administrator's code list.
type CodeListQuery struct {
	Mode         string
	Status       string
	Availability string
	Query        string
	Page         int
	PageSize     int
}

func likePattern(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}

// ListCodes lists codes (never with plaintext).
func (s *Service) ListCodes(ctx context.Context, q CodeListQuery, offset, limit int) ([]CodeView, int64, error) {
	now := s.now()
	db := s.db.WithContext(ctx).Model(&RedemptionCode{})
	if q.Mode != "" {
		db = db.Where("mode = ?", q.Mode)
	}
	if q.Status != "" {
		if q.Status != CodeActive && q.Status != CodeInactive {
			// An unknown status matches nothing (deleted codes stay hidden).
			return []CodeView{}, 0, nil
		}
		db = db.Where("status = ?", q.Status)
	} else {
		db = db.Where("status <> ?", CodeDeleted)
	}
	switch q.Availability {
	case "expired":
		db = db.Where("expires_at IS NOT NULL AND expires_at <= ?", now)
	case "exhausted":
		db = db.Where("max_redemptions IS NOT NULL AND redeemed_count >= max_redemptions")
	case "available":
		mode := s.currentMode(ctx)
		var modes []string
		switch mode {
		case ModeUsage:
			modes = []string{ModeUsage}
		case ModePeriod:
			modes = []string{ModeUsage, ModePeriod}
		default:
			return []CodeView{}, 0, nil
		}
		db = db.Where("mode IN ? AND status = ? AND (expires_at IS NULL OR expires_at > ?) AND (max_redemptions IS NULL OR redeemed_count < max_redemptions)",
			modes, CodeActive, now)
	}
	if strings.TrimSpace(q.Query) != "" {
		p := likePattern(q.Query)
		db = db.Where(`LOWER(description) LIKE ? ESCAPE '\' OR LOWER(code_hint) LIKE ? ESCAPE '\'`, p, p)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []RedemptionCode
	if err := db.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]CodeView, 0, len(rows))
	for _, r := range rows {
		out = append(out, codeView(r, ""))
	}
	return out, total, nil
}

func (s *Service) loadCode(ctx context.Context, db *gorm.DB, id uint) (*RedemptionCode, error) {
	var c RedemptionCode
	if err := db.WithContext(ctx).First(&c, id).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrRedemptionUnavailable.WithStatus(404)
		}
		return nil, err
	}
	if c.Status == CodeDeleted {
		return nil, ErrRedemptionUnavailable.WithStatus(404)
	}
	return &c, nil
}

// RevealCode decrypts the plaintext of one code and returns the full code
// object with the plaintext in its Code field.
func (s *Service) RevealCode(ctx context.Context, id uint) (*CodeView, error) {
	c, err := s.loadCode(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if c.CodeEncrypted == "" {
		return nil, ErrRedemptionNoPlaintext
	}
	if s.secrets == nil {
		return nil, ErrRedemptionSecret
	}
	plain, _, err := s.secrets.Decrypt(c.CodeEncrypted)
	if err != nil {
		return nil, ErrRedemptionSecret.WithCause(err)
	}
	v := codeView(*c, plain)
	return &v, nil
}

// optional distinguishes an absent JSON key from an explicit null.
type optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UpdateCodeInput is the decoded PATCH body of a code.
type UpdateCodeInput struct {
	Status         *string
	MaxRedemptions optional[int]
	PerUserLimit   *int
	ExpiresAt      optional[time.Time]
	Description    *string
}

// ParseUpdateCode decodes a PATCH body, keeping absent keys distinct from nulls.
func ParseUpdateCode(body []byte) (UpdateCodeInput, error) {
	var in UpdateCodeInput
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return in, apperr.ErrInvalidBody.WithStatus(400).WithMessage(fmt.Sprintf("invalid JSON body at offset %d", syn.Offset)).
				WithDetails(map[string]any{"reason": "json_syntax", "offset": syn.Offset})
		}
		return in, apperr.ErrInvalidBody.WithStatus(400).WithDetails(map[string]any{"reason": "invalid"})
	}
	isNull := func(m json.RawMessage) bool { return strings.TrimSpace(string(m)) == "null" }
	if m, ok := raw["status"]; ok && !isNull(m) {
		var v string
		if json.Unmarshal(m, &v) != nil || (v != CodeActive && v != CodeInactive) {
			return in, invalidBodyField("status", "oneof", CodeActive+" "+CodeInactive, "status must be one of: "+CodeActive+" "+CodeInactive)
		}
		in.Status = &v
	}
	if m, ok := raw["maxRedemptions"]; ok {
		in.MaxRedemptions.Set = true
		if isNull(m) {
			in.MaxRedemptions.Null = true
		} else if json.Unmarshal(m, &in.MaxRedemptions.Value) != nil {
			return in, invalidRedemption("maxRedemptions", "max_redemptions")
		}
	}
	if m, ok := raw["perUserLimit"]; ok && !isNull(m) {
		var v int
		if json.Unmarshal(m, &v) != nil {
			return in, invalidBodyField("perUserLimit", "type", "", "perUserLimit has invalid type")
		}
		if v < 1 {
			return in, invalidBodyField("perUserLimit", "min", "1", "perUserLimit must be at least 1")
		}
		if v > 100 {
			return in, invalidBodyField("perUserLimit", "max", "100", "perUserLimit must be at most 100")
		}
		in.PerUserLimit = &v
	}
	if m, ok := raw["expiresAt"]; ok {
		in.ExpiresAt.Set = true
		if isNull(m) {
			in.ExpiresAt.Null = true
		} else {
			var str string
			if json.Unmarshal(m, &str) != nil {
				return in, invalidBodyField("expiresAt", "datetime", "", "expiresAt must be an RFC 3339 time")
			}
			t, err := time.Parse(time.RFC3339, str)
			if err != nil {
				return in, invalidBodyField("expiresAt", "datetime", "", "expiresAt must be an RFC 3339 time")
			}
			in.ExpiresAt.Value = t
		}
	}
	if m, ok := raw["description"]; ok && !isNull(m) {
		var v string
		if json.Unmarshal(m, &v) != nil {
			return in, invalidBodyField("description", "type", "", "description has invalid type")
		}
		if utf8.RuneCountInString(v) > 255 {
			return in, invalidBodyField("description", "max", "255", "description must be at most 255")
		}
		in.Description = &v
	}
	return in, nil
}

// invalidBodyField is a request.invalid_body error naming one field, shaped
// like the generic body validator's output.
func invalidBodyField(field, rule, param, msg string) error {
	return apperr.ErrInvalidBody.WithStatus(400).WithMessage("invalid request body: " + msg).WithDetails(map[string]any{
		"reason":      "validation",
		"fieldErrors": []httpx.FieldError{{Field: field, Rule: rule, Param: param, Message: msg}},
	})
}

// UpdateCode applies the editable fields of a code.
func (s *Service) UpdateCode(ctx context.Context, id uint, in UpdateCodeInput) (*CodeView, error) {
	var out CodeView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c RedemptionCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, id).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrRedemptionUnavailable.WithStatus(404)
			}
			return err
		}
		if c.Status == CodeDeleted {
			return ErrRedemptionUnavailable.WithStatus(404)
		}
		if in.Status != nil {
			if *in.Status != CodeActive && *in.Status != CodeInactive {
				return invalidRedemption("status", "status")
			}
			c.Status = *in.Status
		}
		if in.MaxRedemptions.Set {
			if in.MaxRedemptions.Null {
				c.MaxRedemptions = nil
			} else {
				v := in.MaxRedemptions.Value
				if v <= 0 || v < c.RedeemedCount {
					return invalidRedemption("maxRedemptions", "max_redemptions")
				}
				c.MaxRedemptions = &v
			}
		}
		if in.PerUserLimit != nil {
			if *in.PerUserLimit < 1 || *in.PerUserLimit > 100 {
				return invalidRedemption("perUserLimit", "per_user_limit")
			}
			c.PerUserLimit = *in.PerUserLimit
		}
		if c.MaxRedemptions != nil && c.PerUserLimit > *c.MaxRedemptions {
			return invalidRedemption("perUserLimit", "limit_relationship")
		}
		if in.ExpiresAt.Set {
			if in.ExpiresAt.Null {
				c.ExpiresAt = nil
			} else {
				if !in.ExpiresAt.Value.After(s.now()) {
					return invalidRedemption("expiresAt", "expires_at")
				}
				c.ExpiresAt = utcPtr(in.ExpiresAt.Value)
			}
		}
		if in.Description != nil {
			d := strings.TrimSpace(*in.Description)
			if len(d) > 255 {
				return invalidRedemption("description", "description")
			}
			c.Description = d
		}
		if err := tx.Save(&c).Error; err != nil {
			return err
		}
		out = codeView(c, "")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCode soft-deletes a code (status deleted).
func (s *Service) DeleteCode(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Model(&RedemptionCode{}).Where("id = ? AND status <> ?", id, CodeDeleted).Update("status", CodeDeleted)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRedemptionUnavailable.WithStatus(404)
	}
	return nil
}

// BatchDeleteResult is one row of a batch delete.
type BatchDeleteResult struct {
	ID     uint   `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// BatchDeleteCodes deletes several codes, reporting each outcome.
func (s *Service) BatchDeleteCodes(ctx context.Context, ids []uint) []BatchDeleteResult {
	out := make([]BatchDeleteResult, 0, len(ids))
	for _, id := range ids {
		r := BatchDeleteResult{ID: id, Status: "deleted"}
		if err := s.DeleteCode(ctx, id); err != nil {
			if errors.Is(err, ErrRedemptionUnavailable) {
				r.Status = "not_found"
			} else {
				r.Status, r.Error = "failed", "delete failed"
			}
		}
		out = append(out, r)
	}
	return out
}

// RedemptionView is the API form of a redemption record.
type RedemptionView struct {
	ID                   uint      `json:"id"`
	CodeID               uint      `json:"codeID"`
	UserID               uint      `json:"userID"`
	Mode                 string    `json:"mode"`
	RewardType           string    `json:"rewardType"`
	CreditUSD            float64   `json:"creditUSD"`
	CreditNanousd        int64     `json:"creditNanousd"`
	PlanID               uint      `json:"planID"`
	SubscriptionID       uint      `json:"subscriptionID"`
	BalanceTransactionID uint      `json:"balanceTransactionID"`
	CreatedAt            time.Time `json:"createdAt"`
}

func redemptionView(r Redemption) RedemptionView {
	return RedemptionView{ID: r.ID, CodeID: r.CodeID, UserID: r.UserID, Mode: r.Mode, RewardType: r.RewardType,
		CreditUSD: NanoToUSD(r.CreditNanousd), CreditNanousd: r.CreditNanousd, PlanID: r.PlanID, SubscriptionID: r.SubscriptionID,
		BalanceTransactionID: r.BalanceTransactionID, CreatedAt: r.CreatedAt}
}

// RedeemResult is the outcome of a successful redemption.
type RedeemResult struct {
	Redemption   RedemptionView    `json:"redemption"`
	Account      *AccountView      `json:"account,omitempty"`
	Subscription *SubscriptionView `json:"subscription,omitempty"`
	Overview     *Overview         `json:"overview"`
}

// Redeem applies a code for a user.
func (s *Service) Redeem(ctx context.Context, userID uint, rawCode string) (*RedeemResult, error) {
	code := normalizeRedemptionCode(rawCode)
	if !validRedemptionCode(code) {
		return nil, invalidRedemption("code", "code_format")
	}
	if s.secrets == nil {
		return nil, ErrRedemptionSecret
	}
	mode := s.currentMode(ctx)
	hashes := s.secrets.LookupHashes(code)
	if len(hashes) == 0 {
		return nil, ErrRedemptionSecret
	}
	var found RedemptionCode
	if err := s.db.WithContext(ctx).Where("code_hash IN ?", hashes).First(&found).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrRedemptionUnavailable
		}
		return nil, err
	}
	var res RedeemResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now()
		var c RedemptionCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, found.ID).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrRedemptionUnavailable
			}
			return err
		}
		compatible := (mode == ModeUsage && c.Mode == ModeUsage) || (mode == ModePeriod && (c.Mode == ModeUsage || c.Mode == ModePeriod))
		if c.Status != CodeActive || !compatible || (c.ExpiresAt != nil && !c.ExpiresAt.After(now)) {
			return ErrRedemptionUnavailable
		}
		if c.MaxRedemptions != nil && c.RedeemedCount >= *c.MaxRedemptions {
			return ErrRedemptionExhausted
		}
		var prior int64
		if err := tx.Model(&Redemption{}).Where("code_id = ? AND user_id = ?", c.ID, userID).Count(&prior).Error; err != nil {
			return err
		}
		if int(prior) >= c.PerUserLimit {
			return ErrRedemptionUserLimit
		}
		rec := Redemption{CodeID: c.ID, UserID: userID, Mode: c.Mode, RewardType: c.RewardType}
		var nonce [4]byte
		_, _ = rand.Read(nonce[:])
		rec.RefNo = fmt.Sprintf("redemption_%d_%d_%s", userID, now.UnixNano(), hex.EncodeToString(nonce[:]))
		snap := map[string]any{"code_id": c.ID, "mode": c.Mode, "reward_type": c.RewardType, "credit_nanousd": c.CreditNanousd,
			"plan_id": c.PlanID, "duration_days": c.DurationDays, "description": c.Description}
		switch c.RewardType {
		case RewardSubscription:
			var plan Plan
			if err := tx.First(&plan, c.PlanID).Error; err != nil || !plan.IsActive || plan.Code == PlanFree {
				return ErrRedemptionUnavailable
			}
			price, err := s.defaultPrice(ctx, tx, plan.ID, true)
			if err != nil {
				return err
			}
			if price == nil {
				return ErrRedemptionUnavailable
			}
			sub, err := s.grantTime(tx, userID, plan, *price, time.Duration(c.DurationDays)*24*time.Hour, now)
			if err != nil {
				return err
			}
			rec.PlanID, rec.SubscriptionID = plan.ID, sub.ID
			sv := subscriptionView(*sub)
			res.Subscription = &sv
		default:
			acc, err := lockAccount(tx, userID)
			if err != nil {
				return err
			}
			newBal := acc.BalanceNanousd + c.CreditNanousd
			if err := tx.Model(&Account{}).Where("id = ?", acc.ID).Update("balance_nanousd", newBal).Error; err != nil {
				return err
			}
			desc := c.Description
			if desc == "" {
				desc = "redemption credit"
			}
			bt := BalanceTransaction{AccountID: acc.ID, UserID: userID, Type: TxRedemption, AmountNanousd: c.CreditNanousd,
				BalanceAfterNanousd: newBal, RefType: "redemption_code", RefID: c.ID, RefNo: rec.RefNo, Description: desc}
			if err := tx.Create(&bt).Error; err != nil {
				return err
			}
			rec.CreditNanousd, rec.BalanceTransactionID = c.CreditNanousd, bt.ID
			acc.BalanceNanousd = newBal
			acc.UpdatedAt = now
			av := accountView(*acc)
			res.Account = &av
		}
		b, _ := json.Marshal(snap)
		rec.SnapshotJSON = string(b)
		if err := tx.Create(&rec).Error; err != nil {
			return err
		}
		if err := tx.Model(&RedemptionCode{}).Where("id = ?", c.ID).Update("redeemed_count", gorm.Expr("redeemed_count + 1")).Error; err != nil {
			return err
		}
		res.Redemption = redemptionView(rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	ov, err := s.Overview(ctx, userID)
	if err != nil {
		return nil, err
	}
	res.Overview = ov
	return &res, nil
}

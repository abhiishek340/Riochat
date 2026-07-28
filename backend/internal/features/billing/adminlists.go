package billing

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Mode returns the current billing mode (self when unreadable).
func (s *Service) Mode(ctx context.Context) string { return s.currentMode(ctx) }

type userLabels struct{ Username, DisplayName, Label string }

func (s *Service) labelsFor(ctx context.Context, ids []uint) map[uint]userLabels {
	out := map[uint]userLabels{}
	if s.ports.Users == nil || len(ids) == 0 {
		return out
	}
	m, err := s.ports.Users.Users(ctx, ids)
	if err != nil {
		return out
	}
	for id, u := range m {
		l := u.DisplayName
		if l == "" {
			l = u.Username
		}
		out[id] = userLabels{Username: u.Username, DisplayName: u.DisplayName, Label: l}
	}
	return out
}

// CallLogQuery filters the administrator's call log.
type CallLogQuery struct {
	Query             string
	PlatformModelName string
	BillingMode       string
	UserID            uint
	CreatedFrom       *time.Time
	CreatedTo         *time.Time
	Sort              string
}

func createdOrder(sort string) string {
	switch sort {
	case "created_asc":
		return "created_at ASC, id ASC"
	case "tokens_desc":
		return "(input_tokens + cache_read_tokens + cache_write_tokens + output_tokens + reasoning_tokens) DESC, id DESC"
	case "cost_desc":
		return "billed_nanousd DESC, id DESC"
	case "latency_desc":
		return "latency_ms DESC, id DESC"
	}
	return "created_at DESC, id DESC"
}

// ListCallLogs lists ledger rows for administrators, including upstream names
// and the unsanitized snapshot.
func (s *Service) ListCallLogs(ctx context.Context, q CallLogQuery, offset, limit int) ([]AdminLedgerItem, int64, error) {
	db := s.db.WithContext(ctx).Model(&UsageLedger{})
	if qs := strings.TrimSpace(q.Query); qs != "" {
		p := likePattern(qs)
		// A run started by a request whose id equals the query also matches.
		var runRefs []string
		if s.ports.Runs != nil {
			if refs, err := s.ports.Runs.RefNosForRequestID(ctx, qs); err == nil {
				for _, r := range refs {
					if r != "" {
						runRefs = append(runRefs, r)
					}
				}
			}
		}
		cond := `(LOWER(platform_model_name) LIKE ? ESCAPE '\' OR LOWER(upstream_model_name) LIKE ? ESCAPE '\' OR LOWER(upstream_name) LIKE ? ESCAPE '\' ` +
			`OR LOWER(routed_binding_code) LIKE ? ESCAPE '\' OR LOWER(provider_protocol) LIKE ? ESCAPE '\' OR ref_no = ?`
		args := []any{p, p, p, p, p, qs}
		if len(runRefs) > 0 {
			cond += ` OR ref_no IN ?`
			args = append(args, runRefs)
		}
		db = db.Where(cond+`)`, args...)
	}
	if q.PlatformModelName != "" {
		db = db.Where("platform_model_name = ?", q.PlatformModelName)
	}
	if q.UserID != 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.CreatedFrom != nil {
		db = db.Where("created_at >= ?", q.CreatedFrom.UTC())
	}
	if q.CreatedTo != nil {
		db = db.Where("created_at <= ?", q.CreatedTo.UTC())
	}
	snapshotFilter := false
	switch q.BillingMode {
	case "free":
		db = db.Where("is_free_model = ?", true)
	case PricingToken, PricingCall, PricingDuration, PricingTiered:
		db = db.Where("is_free_model = ?", false)
		snapshotFilter = true
	}
	var rows []UsageLedger
	var total int64
	if snapshotFilter {
		// The pricing mode lives inside the snapshot JSON, so it is filtered here.
		var cand []UsageLedger
		if err := db.Session(&gorm.Session{}).Select("id", "pricing_snapshot_json").Order(createdOrder(q.Sort)).Find(&cand).Error; err != nil {
			return nil, 0, err
		}
		var ids []uint
		for _, c := range cand {
			var snap struct {
				PricingMode string `json:"pricing_mode"`
			}
			_ = json.Unmarshal([]byte(c.PricingSnapshotJSON), &snap)
			mode := snap.PricingMode
			if mode == "" {
				mode = PricingToken
			}
			if mode == q.BillingMode {
				ids = append(ids, c.ID)
			}
		}
		total = int64(len(ids))
		lo, hi := offset, offset+limit
		if lo > len(ids) {
			lo = len(ids)
		}
		if hi > len(ids) {
			hi = len(ids)
		}
		page := ids[lo:hi]
		if len(page) > 0 {
			if err := s.db.WithContext(ctx).Where("id IN ?", page).Order(createdOrder(q.Sort)).Find(&rows).Error; err != nil {
				return nil, 0, err
			}
		}
	} else {
		if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			return nil, 0, err
		}
		if err := db.Order(createdOrder(q.Sort)).Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
			return nil, 0, err
		}
	}
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	labels := s.labelsFor(ctx, ids)
	items := make([]AdminLedgerItem, 0, len(rows))
	for _, r := range rows {
		l := labels[r.UserID]
		li := ledgerItem(r, r.PricingSnapshotJSON)
		li.UsageDate = dateTime(r.UsageDate, s.loc)
		items = append(items, AdminLedgerItem{LedgerItem: li, UpstreamName: r.UpstreamName, Username: l.Username,
			UserDisplayName: l.DisplayName, UserLabel: l.Label})
	}
	return items, total, nil
}

// OrderQuery filters payment orders.
type OrderQuery struct {
	Query       string
	OrderType   string
	Provider    string
	Status      string
	UserID      uint
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Sort        string
}

// OrderView is the administrator's view of a payment order (no checkout URL).
type OrderView struct {
	ID                 uint       `json:"id"`
	OrderNo            string     `json:"orderNo"`
	OrderType          string     `json:"orderType"`
	UserID             uint       `json:"userID"`
	Username           string     `json:"username"`
	UserDisplayName    string     `json:"userDisplayName"`
	UserLabel          string     `json:"userLabel"`
	PlanID             uint       `json:"planID"`
	PriceID            uint       `json:"priceID"`
	Provider           string     `json:"provider"`
	Status             string     `json:"status"`
	BaseCurrency       string     `json:"baseCurrency"`
	BaseAmountCents    int64      `json:"baseAmountCents"`
	PayCurrency        string     `json:"payCurrency"`
	PayAmountCents     int64      `json:"payAmountCents"`
	FxRate             string     `json:"fxRate"`
	CreditNanousd      int64      `json:"creditNanousd"`
	CreditUSD          float64    `json:"creditUSD"`
	BillingInterval    string     `json:"billingInterval"`
	Cycles             int        `json:"cycles"`
	ExternalPaymentID  string     `json:"externalPaymentID"`
	ExternalCheckoutID string     `json:"externalCheckoutID"`
	PaidAt             *time.Time `json:"paidAt"`
	ExpiredAt          *time.Time `json:"expiredAt"`
	SnapshotJSON       string     `json:"snapshotJSON"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// ListOrders lists payment orders.
func (s *Service) ListOrders(ctx context.Context, q OrderQuery, offset, limit int) ([]OrderView, int64, error) {
	db := s.db.WithContext(ctx).Model(&PaymentOrder{})
	if qs := strings.TrimSpace(q.Query); qs != "" {
		p := likePattern(qs)
		db = db.Where(`(LOWER(order_no) LIKE ? ESCAPE '\' OR LOWER(provider) LIKE ? ESCAPE '\' OR LOWER(status) LIKE ? ESCAPE '\' `+
			`OR LOWER(external_payment_id) LIKE ? ESCAPE '\' OR LOWER(external_checkout_id) LIKE ? ESCAPE '\')`, p, p, p, p, p)
	}
	if q.OrderType != "" {
		db = db.Where("order_type = ?", q.OrderType)
	}
	if q.Provider != "" {
		db = db.Where("provider = ?", q.Provider)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.UserID != 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.CreatedFrom != nil {
		db = db.Where("created_at >= ?", q.CreatedFrom.UTC())
	}
	if q.CreatedTo != nil {
		db = db.Where("created_at <= ?", q.CreatedTo.UTC())
	}
	order := "created_at DESC, id DESC"
	switch q.Sort {
	case "created_asc":
		order = "created_at ASC, id ASC"
	case "paid_desc":
		order = "CASE WHEN paid_at IS NULL THEN 1 ELSE 0 END ASC, paid_at DESC, id DESC"
	case "amount_desc":
		order = "pay_amount_cents DESC, id DESC"
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PaymentOrder
	if err := db.Order(order).Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	labels := s.labelsFor(ctx, ids)
	out := make([]OrderView, 0, len(rows))
	for _, o := range rows {
		l := labels[o.UserID]
		out = append(out, OrderView{ID: o.ID, OrderNo: o.OrderNo, OrderType: o.OrderType, UserID: o.UserID, Username: l.Username,
			UserDisplayName: l.DisplayName, UserLabel: l.Label, PlanID: o.PlanID, PriceID: o.PriceID, Provider: o.Provider, Status: o.Status,
			BaseCurrency: o.BaseCurrency, BaseAmountCents: o.BaseAmountCents, PayCurrency: o.PayCurrency, PayAmountCents: o.PayAmountCents,
			FxRate: o.FxRate, CreditNanousd: o.CreditNanousd, CreditUSD: NanoToUSD(o.CreditNanousd), BillingInterval: o.BillingInterval,
			Cycles: o.Cycles, ExternalPaymentID: o.ExternalPaymentID, ExternalCheckoutID: o.ExternalCheckoutID, PaidAt: o.PaidAt,
			ExpiredAt: o.ExpiredAt, SnapshotJSON: orEmptyObject(o.SnapshotJSON), CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt})
	}
	return out, total, nil
}

// RedemptionQuery filters redemption records.
type RedemptionQuery struct {
	Query       string
	CodeID      uint
	UserID      uint
	RewardType  string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Sort        string
}

// RedemptionAdminView is the administrator's view of a redemption.
type RedemptionAdminView struct {
	ID                   uint      `json:"id"`
	CodeID               uint      `json:"codeID"`
	CodeHint             string    `json:"codeHint"`
	CodeDescription      string    `json:"codeDescription"`
	CodeStatus           string    `json:"codeStatus"`
	UserID               uint      `json:"userID"`
	Username             string    `json:"username"`
	UserDisplayName      string    `json:"userDisplayName"`
	UserLabel            string    `json:"userLabel"`
	Mode                 string    `json:"mode"`
	RewardType           string    `json:"rewardType"`
	CreditNanousd        int64     `json:"creditNanousd"`
	CreditUSD            float64   `json:"creditUSD"`
	PlanID               uint      `json:"planID"`
	PlanName             string    `json:"planName"`
	DurationDays         int       `json:"durationDays"`
	SubscriptionID       uint      `json:"subscriptionID"`
	BalanceBeforeNanousd *int64    `json:"balanceBeforeNanousd"`
	BalanceAfterNanousd  *int64    `json:"balanceAfterNanousd"`
	RefNo                string    `json:"refNo"`
	SnapshotJSON         string    `json:"snapshotJSON"`
	CreatedAt            time.Time `json:"createdAt"`
}

// ListRedemptions lists redemption records with their code details.
func (s *Service) ListRedemptions(ctx context.Context, q RedemptionQuery, offset, limit int) ([]RedemptionAdminView, int64, error) {
	db := s.db.WithContext(ctx).Model(&Redemption{})
	if qs := strings.TrimSpace(q.Query); qs != "" {
		p := likePattern(qs)
		db = db.Where(`(LOWER(ref_no) LIKE ? ESCAPE '\' OR code_id IN (SELECT id FROM billing_redemption_codes `+
			`WHERE LOWER(code_hint) LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\'))`, p, p, p)
	}
	if q.CodeID != 0 {
		db = db.Where("code_id = ?", q.CodeID)
	}
	if q.UserID != 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.RewardType != "" {
		db = db.Where("reward_type = ?", q.RewardType)
	}
	if q.CreatedFrom != nil {
		db = db.Where("created_at >= ?", q.CreatedFrom.UTC())
	}
	if q.CreatedTo != nil {
		db = db.Where("created_at <= ?", q.CreatedTo.UTC())
	}
	order := "created_at DESC, id DESC"
	if q.Sort == "created_asc" {
		order = "created_at ASC, id ASC"
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Redemption
	if err := db.Order(order).Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	var codeIDs, txIDs, planIDs, userIDs []uint
	for _, r := range rows {
		codeIDs = append(codeIDs, r.CodeID)
		userIDs = append(userIDs, r.UserID)
		if r.BalanceTransactionID != 0 {
			txIDs = append(txIDs, r.BalanceTransactionID)
		}
		if r.PlanID != 0 {
			planIDs = append(planIDs, r.PlanID)
		}
	}
	codes := map[uint]RedemptionCode{}
	if len(codeIDs) > 0 {
		var cs []RedemptionCode
		if err := s.db.WithContext(ctx).Unscoped().Where("id IN ?", codeIDs).Find(&cs).Error; err != nil {
			return nil, 0, err
		}
		for _, c := range cs {
			codes[c.ID] = c
		}
	}
	txs := map[uint]BalanceTransaction{}
	if len(txIDs) > 0 {
		var ts []BalanceTransaction
		if err := s.db.WithContext(ctx).Where("id IN ?", txIDs).Find(&ts).Error; err != nil {
			return nil, 0, err
		}
		for _, t := range ts {
			txs[t.ID] = t
		}
	}
	plans := map[uint]Plan{}
	if len(planIDs) > 0 {
		var ps []Plan
		if err := s.db.WithContext(ctx).Unscoped().Where("id IN ?", planIDs).Find(&ps).Error; err != nil {
			return nil, 0, err
		}
		for _, p := range ps {
			plans[p.ID] = p
		}
	}
	labels := s.labelsFor(ctx, userIDs)
	out := make([]RedemptionAdminView, 0, len(rows))
	for _, r := range rows {
		c := codes[r.CodeID]
		l := labels[r.UserID]
		v := RedemptionAdminView{ID: r.ID, CodeID: r.CodeID, CodeHint: c.CodeHint, CodeDescription: c.Description, CodeStatus: c.Status,
			UserID: r.UserID, Username: l.Username, UserDisplayName: l.DisplayName, UserLabel: l.Label, Mode: r.Mode, RewardType: r.RewardType,
			CreditNanousd: r.CreditNanousd, CreditUSD: NanoToUSD(r.CreditNanousd), PlanID: r.PlanID, PlanName: plans[r.PlanID].Name,
			SubscriptionID: r.SubscriptionID, RefNo: r.RefNo, SnapshotJSON: orEmptyObject(r.SnapshotJSON), CreatedAt: r.CreatedAt}
		var snap struct {
			DurationDays int `json:"duration_days"`
		}
		if json.Unmarshal([]byte(r.SnapshotJSON), &snap) == nil {
			v.DurationDays = snap.DurationDays
		}
		if t, ok := txs[r.BalanceTransactionID]; ok {
			after := t.BalanceAfterNanousd
			before := after - t.AmountNanousd
			v.BalanceAfterNanousd, v.BalanceBeforeNanousd = &after, &before
		}
		out = append(out, v)
	}
	return out, total, nil
}

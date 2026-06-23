package billing

import (
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Status and enum values stored in the tables.
const (
	ModeSelf   = "self"
	ModeUsage  = "usage"
	ModePeriod = "period"

	PricingToken    = "token"
	PricingCall     = "call"
	PricingDuration = "duration"
	PricingTiered   = "tiered"

	IntervalMonth    = "month"
	IntervalYear     = "year"
	IntervalLifetime = "lifetime"

	SubActive  = "active"
	SubExpired = "expired"

	OrderPending = "pending"
	OrderPaid    = "paid"
	OrderFailed  = "failed"
	OrderExpired = "expired"

	OrderSubscription = "subscription"
	OrderTopup        = "topup"

	ProviderStripe = "stripe"
	ProviderEpay   = "epay"

	TxTopup       = "topup"
	TxUsageDebit  = "usage_debit"
	TxAdminSet    = "admin_set"
	TxRedemption  = "redemption"
	TxUsageReserv = "usage_reserve"
	TxUsageRefund = "usage_refund"

	ResActive         = "active"
	ResSettled        = "settled"
	ResReleased       = "released"
	ResReconciliation = "reconciliation"

	CodeActive   = "active"
	CodeInactive = "inactive"
	CodeDeleted  = "deleted"

	RewardBalance      = "balance"
	RewardSubscription = "subscription"

	PlanFree = "free"

	CurrencyUSD = "USD"
	CurrencyCNY = "CNY"
)

// DateOnly is a calendar date persisted as "YYYY-MM-DD". It sidesteps every
// time-zone conversion between the Go value and a SQL date column.
type DateOnly struct{ Y, M, D int }

// DateOf returns the calendar date of t in t's own location.
func DateOf(t time.Time) DateOnly {
	y, m, d := t.Date()
	return DateOnly{y, int(m), d}
}

// String formats the date as YYYY-MM-DD.
func (d DateOnly) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Y, d.M, d.D) }

// IsZero reports the zero date.
func (d DateOnly) IsZero() bool { return d.Y == 0 }

// In returns midnight of the date in loc.
func (d DateOnly) In(loc *time.Location) time.Time {
	return time.Date(d.Y, time.Month(d.M), d.D, 0, 0, 0, 0, loc)
}

// GormDataType makes GORM create a date column.
func (DateOnly) GormDataType() string { return "date" }

// Value implements driver.Valuer.
func (d DateOnly) Value() (driver.Value, error) { return d.String(), nil }

// Scan implements sql.Scanner for text and time values.
func (d *DateOnly) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = DateOnly{}
	case time.Time:
		*d = DateOnly{v.Year(), int(v.Month()), v.Day()}
	case string:
		return d.parse(v)
	case []byte:
		return d.parse(string(v))
	default:
		return fmt.Errorf("billing: cannot scan %T into DateOnly", src)
	}
	return nil
}

func (d *DateOnly) parse(s string) error {
	if len(s) < 10 {
		*d = DateOnly{}
		return nil
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil {
		return err
	}
	*d = DateOnly{t.Year(), int(t.Month()), t.Day()}
	return nil
}

// Plan is a subscription plan.
type Plan struct {
	database.Model
	Code                string `gorm:"column:code;size:32;not null;uniqueIndex:uidx_billing_plans_code"`
	Name                string `gorm:"column:name;size:64;default:''"`
	Description         string `gorm:"column:description;size:255;default:''"`
	FeatureJSON         string `gorm:"column:feature_json;type:text"`
	PeriodCreditNanousd int64  `gorm:"column:period_credit_nanousd;default:0"`
	SortOrder           int    `gorm:"column:sort_order;default:0"`
	IsActive            bool   `gorm:"column:is_active;index:idx_billing_plans_is_active"`
	PermissionGroupID   *uint  `gorm:"column:permission_group_id;index:idx_billing_plans_permission_group"`
}

// TableName implements gorm's Tabler.
func (Plan) TableName() string { return "billing_plans" }

// Price is a purchasable price of a plan.
type Price struct {
	database.Model
	PlanID           uint   `gorm:"column:plan_id;index:idx_billing_prices_plan"`
	Code             string `gorm:"column:code;size:48;uniqueIndex:uidx_billing_prices_code"`
	BillingInterval  string `gorm:"column:billing_interval;size:16;default:'month'"`
	Currency         string `gorm:"column:currency;size:16;default:'USD'"`
	AmountCents      int64  `gorm:"column:amount_cents;default:0"`
	IsActive         bool   `gorm:"column:is_active"`
	IsDefault        bool   `gorm:"column:is_default"`
	ExternalPriceRef string `gorm:"column:external_price_ref;size:128;default:''"`
}

// TableName implements gorm's Tabler.
func (Price) TableName() string { return "billing_prices" }

// Subscription is one entitlement segment of a user.
type Subscription struct {
	database.Model
	UserID               uint       `gorm:"column:user_id;index:idx_billing_subscriptions_user"`
	PlanID               uint       `gorm:"column:plan_id;index:idx_billing_subscriptions_plan"`
	PriceID              uint       `gorm:"column:price_id;index:idx_billing_subscriptions_price"`
	Status               string     `gorm:"column:status;size:32;default:'active'"`
	StartAt              time.Time  `gorm:"column:start_at;not null"`
	CurrentPeriodStartAt time.Time  `gorm:"column:current_period_start_at;not null"`
	CurrentPeriodEndAt   *time.Time `gorm:"column:current_period_end_at"`
	CancelAtPeriodEnd    bool       `gorm:"column:cancel_at_period_end"`
	CanceledAt           *time.Time `gorm:"column:canceled_at"`
	AutoRenew            bool       `gorm:"column:auto_renew"`
}

// TableName implements gorm's Tabler.
func (Subscription) TableName() string { return "billing_subscriptions" }

// PaymentOrder is a checkout attempt.
type PaymentOrder struct {
	database.Model
	OrderNo            string     `gorm:"column:order_no;size:64;uniqueIndex:uidx_billing_payment_orders_no"`
	OrderType          string     `gorm:"column:order_type;size:32;default:'subscription'"`
	UserID             uint       `gorm:"column:user_id;index:idx_billing_payment_orders_user"`
	PlanID             uint       `gorm:"column:plan_id;index:idx_billing_payment_orders_plan"`
	PriceID            uint       `gorm:"column:price_id;index:idx_billing_payment_orders_price"`
	Provider           string     `gorm:"column:provider;size:32"`
	Status             string     `gorm:"column:status;size:32;default:'pending'"`
	BaseCurrency       string     `gorm:"column:base_currency;size:16;default:'USD'"`
	BaseAmountCents    int64      `gorm:"column:base_amount_cents"`
	PayCurrency        string     `gorm:"column:pay_currency;size:16;default:'CNY'"`
	PayAmountCents     int64      `gorm:"column:pay_amount_cents"`
	FxRate             string     `gorm:"column:fx_rate;size:32"`
	CreditNanousd      int64      `gorm:"column:credit_nanousd"`
	BillingInterval    string     `gorm:"column:billing_interval;size:16;default:'month'"`
	Cycles             int        `gorm:"column:cycles;default:1"`
	ExternalPaymentID  string     `gorm:"column:external_payment_id;size:128;index:idx_billing_payment_orders_ext_pay"`
	ExternalCheckoutID string     `gorm:"column:external_checkout_id;size:128;index:idx_billing_payment_orders_ext_checkout"`
	CheckoutURL        string     `gorm:"column:checkout_url;type:text"`
	PaidAt             *time.Time `gorm:"column:paid_at"`
	ExpiredAt          *time.Time `gorm:"column:expired_at"`
	SnapshotJSON       string     `gorm:"column:snapshot_json;type:text"`
}

// TableName implements gorm's Tabler.
func (PaymentOrder) TableName() string { return "billing_payment_orders" }

// Account is a user's prepaid balance.
type Account struct {
	database.Model
	UserID         uint   `gorm:"column:user_id;uniqueIndex:uidx_billing_accounts_user"`
	Currency       string `gorm:"column:currency;size:16;default:'USD'"`
	BalanceNanousd int64  `gorm:"column:balance_nanousd;default:0"`
	Status         string `gorm:"column:status;size:32;default:'active'"`
}

// TableName implements gorm's Tabler.
func (Account) TableName() string { return "billing_accounts" }

// BalanceTransaction is one signed balance movement.
type BalanceTransaction struct {
	database.Model
	AccountID           uint   `gorm:"column:account_id;index:idx_billing_balance_tx_account"`
	UserID              uint   `gorm:"column:user_id;index:idx_billing_balance_tx_user"`
	Type                string `gorm:"column:type;size:32;index:idx_billing_balance_tx_type"`
	AmountNanousd       int64  `gorm:"column:amount_nanousd"`
	BalanceAfterNanousd int64  `gorm:"column:balance_after_nanousd"`
	RefType             string `gorm:"column:ref_type;size:64"`
	RefID               uint   `gorm:"column:ref_id"`
	RefNo               string `gorm:"column:ref_no;size:128;index:idx_billing_balance_tx_ref_no"`
	Description         string `gorm:"column:description;size:255"`
}

// TableName implements gorm's Tabler.
func (BalanceTransaction) TableName() string { return "billing_balance_transactions" }

// UsageReservation holds budget for one in-flight paid run.
type UsageReservation struct {
	database.Model
	UserID              uint       `gorm:"column:user_id;uniqueIndex:uidx_billing_reservation_user_ref"`
	RefNo               string     `gorm:"column:ref_no;size:128;uniqueIndex:uidx_billing_reservation_user_ref"`
	Mode                string     `gorm:"column:mode;size:16"`
	BalanceNanousd      int64      `gorm:"column:balance_nanousd"`
	PeriodCreditNanousd int64      `gorm:"column:period_credit_nanousd"`
	PeriodLimitNanousd  int64      `gorm:"column:period_limit_nanousd"`
	PeriodStartAt       *time.Time `gorm:"column:period_start_at"`
	PeriodEndAt         *time.Time `gorm:"column:period_end_at"`
	Status              string     `gorm:"column:status;size:24"`
	UsageLedgerID       uint       `gorm:"column:usage_ledger_id;default:0"`
	ExpiresAt           time.Time  `gorm:"column:expires_at;not null"`
	SettledAt           *time.Time `gorm:"column:settled_at"`
	ReleasedAt          *time.Time `gorm:"column:released_at"`
	ReconciliationAt    *time.Time `gorm:"column:reconciliation_at"`
	FailureCode         string     `gorm:"column:failure_code;size:64"`
}

// TableName implements gorm's Tabler.
func (UsageReservation) TableName() string { return "billing_usage_reservations" }

// RedemptionCode is a redeemable code (plaintext only as ciphertext).
type RedemptionCode struct {
	database.Model
	CodeHash        string     `gorm:"column:code_hash;size:64;uniqueIndex:uidx_billing_redemption_codes_hash"`
	CodeEncrypted   string     `gorm:"column:code_encrypted;type:text"`
	CodeHint        string     `gorm:"column:code_hint;size:32"`
	Mode            string     `gorm:"column:mode;size:16;default:'usage'"`
	RewardType      string     `gorm:"column:reward_type;size:32;default:'balance'"`
	CreditNanousd   int64      `gorm:"column:credit_nanousd"`
	PlanID          uint       `gorm:"column:plan_id;default:0"`
	DurationDays    int        `gorm:"column:duration_days"`
	MaxRedemptions  *int       `gorm:"column:max_redemptions"`
	PerUserLimit    int        `gorm:"column:per_user_limit;default:1"`
	RedeemedCount   int        `gorm:"column:redeemed_count;default:0"`
	Status          string     `gorm:"column:status;size:32;default:'active'"`
	ExpiresAt       *time.Time `gorm:"column:expires_at"`
	Description     string     `gorm:"column:description;size:255"`
	CreatedByUserID uint       `gorm:"column:created_by_user_id"`
}

// TableName implements gorm's Tabler.
func (RedemptionCode) TableName() string { return "billing_redemption_codes" }

// Redemption records one successful redemption.
type Redemption struct {
	database.Model
	CodeID               uint   `gorm:"column:code_id;index:idx_billing_redemptions_code;index:idx_billing_redemptions_code_user,priority:1"`
	UserID               uint   `gorm:"column:user_id;index:idx_billing_redemptions_code_user,priority:2"`
	Mode                 string `gorm:"column:mode;size:16"`
	RewardType           string `gorm:"column:reward_type;size:32"`
	CreditNanousd        int64  `gorm:"column:credit_nanousd"`
	PlanID               uint   `gorm:"column:plan_id;default:0"`
	SubscriptionID       uint   `gorm:"column:subscription_id;default:0"`
	BalanceTransactionID uint   `gorm:"column:balance_transaction_id;default:0"`
	RefNo                string `gorm:"column:ref_no;size:128;index:idx_billing_redemptions_ref_no"`
	SnapshotJSON         string `gorm:"column:snapshot_json;type:text;default:'{}'"`
}

// TableName implements gorm's Tabler.
func (Redemption) TableName() string { return "billing_redemptions" }

// ModelPrice is the price row of one platform model.
type ModelPrice struct {
	database.Model
	PlatformModelName           string `gorm:"column:platform_model_name;size:128;uniqueIndex:uidx_billing_model_prices_name"`
	Currency                    string `gorm:"column:currency;size:16;default:'USD'"`
	IsFree                      bool   `gorm:"column:is_free"`
	PricingMode                 string `gorm:"column:pricing_mode;size:16;default:'token'"`
	InputNanousdPerMTokens      int64  `gorm:"column:input_nanousd_per_m_tokens;default:0"`
	CacheReadNanousdPerMTokens  int64  `gorm:"column:cache_read_nanousd_per_m_tokens;default:0"`
	CacheWriteNanousdPerMTokens int64  `gorm:"column:cache_write_nanousd_per_m_tokens;default:0"`
	OutputNanousdPerMTokens     int64  `gorm:"column:output_nanousd_per_m_tokens;default:0"`
	CallNanousdPerCall          int64  `gorm:"column:call_nanousd_per_call;default:0"`
	DurationNanousdPerSecond    int64  `gorm:"column:duration_nanousd_per_second;default:0"`
	CacheWritePriceBasis        string `gorm:"column:cache_write_price_basis;size:32;default:''"`
	TieredPricingJSON           string `gorm:"column:tiered_pricing_json;type:text;default:'{}'"`
	SchedulePricingJSON         string `gorm:"column:schedule_pricing_json;type:text;default:'{}'"`
}

// TableName implements gorm's Tabler.
func (ModelPrice) TableName() string { return "billing_model_prices" }

// UsageLedger is the immutable record of one billable invocation.
type UsageLedger struct {
	database.Model
	UserID              uint      `gorm:"column:user_id;index:idx_billing_usage_ledgers_user_date,priority:1;index:idx_billing_usage_ledgers_user_at,priority:1"`
	RefNo               string    `gorm:"column:ref_no;size:128;default:''"`
	ConversationID      uint      `gorm:"column:conversation_id;index:idx_billing_usage_ledgers_conversation"`
	ProviderProtocol    string    `gorm:"column:provider_protocol;size:64;index:idx_billing_usage_ledgers_protocol"`
	UpstreamName        string    `gorm:"column:upstream_name;size:128"`
	PlatformModelName   string    `gorm:"column:platform_model_name;size:128;index:idx_billing_usage_ledgers_model"`
	RoutedBindingCode   string    `gorm:"column:routed_binding_code;size:64;index:idx_billing_usage_ledgers_binding"`
	UpstreamModelName   string    `gorm:"column:upstream_model_name;size:256"`
	IsFreeModel         bool      `gorm:"column:is_free_model;index:idx_billing_usage_ledgers_free"`
	BillingAt           time.Time `gorm:"column:billing_at;index:idx_billing_usage_ledgers_user_at,priority:2"`
	UsageDate           DateOnly  `gorm:"column:usage_date;type:date;index:idx_billing_usage_ledgers_user_date,priority:2"`
	InputTokens         int64     `gorm:"column:input_tokens"`
	CacheReadTokens     int64     `gorm:"column:cache_read_tokens"`
	CacheWriteTokens    int64     `gorm:"column:cache_write_tokens"`
	CacheWrite5mTokens  int64     `gorm:"column:cache_write_5m_tokens"`
	CacheWrite1hTokens  int64     `gorm:"column:cache_write_1h_tokens"`
	OutputTokens        int64     `gorm:"column:output_tokens"`
	ReasoningTokens     int64     `gorm:"column:reasoning_tokens"`
	CallCount           int64     `gorm:"column:call_count"`
	DurationSeconds     int64     `gorm:"column:duration_seconds"`
	LatencyMS           int64     `gorm:"column:latency_ms"`
	UsageSpeed          string    `gorm:"column:usage_speed;size:32"`
	ServiceTier         string    `gorm:"column:service_tier;size:32"`
	BilledCurrency      string    `gorm:"column:billed_currency;size:16;default:'USD'"`
	BilledNanousd       int64     `gorm:"column:billed_nanousd"`
	BalanceAfterNanousd *int64    `gorm:"column:balance_after_nanousd"`
	PricingSnapshotJSON string    `gorm:"column:pricing_snapshot_json;type:text"`
}

// TableName implements gorm's Tabler.
func (UsageLedger) TableName() string { return "billing_usage_ledgers" }

func allModels() []any {
	return []any{
		&Plan{}, &Price{}, &Subscription{}, &PaymentOrder{}, &Account{}, &BalanceTransaction{},
		&UsageReservation{}, &RedemptionCode{}, &Redemption{}, &ModelPrice{}, &UsageLedger{},
	}
}

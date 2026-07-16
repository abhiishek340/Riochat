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

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const orderLifetime = 30 * time.Minute

// CheckoutRequest is the body of POST /billing/payments/checkout.
type CheckoutRequest struct {
	OrderType        string `json:"orderType" binding:"omitempty,oneof=subscription topup"`
	PriceID          uint   `json:"priceID" binding:"omitempty,min=1"`
	AmountMinorUnits int64  `json:"amountMinorUnits" binding:"omitempty,min=0"`
	Cycles           int    `json:"cycles" binding:"omitempty,min=1,max=120"`
	PaymentProvider  string `json:"paymentProvider" binding:"omitempty,oneof=stripe epay"`
	EpayType         string `json:"epayType" binding:"max=32"`
	SuccessURL       string `json:"successURL" binding:"max=512"`
	CancelURL        string `json:"cancelURL" binding:"max=512"`
}

// CheckoutView is the checkout payload returned to the client.
type CheckoutView struct {
	OrderNo            string     `json:"orderNo"`
	OrderType          string     `json:"orderType"`
	Provider           string     `json:"provider"`
	Status             string     `json:"status"`
	CheckoutURL        string     `json:"checkoutURL"`
	ExternalCheckoutID string     `json:"externalCheckoutID"`
	BaseAmountCents    int64      `json:"baseAmountCents"`
	BaseCurrency       string     `json:"baseCurrency"`
	PayAmountCents     int64      `json:"payAmountCents"`
	PayCurrency        string     `json:"payCurrency"`
	FxRate             string     `json:"fxRate"`
	CreditNanousd      int64      `json:"creditNanousd"`
	CreditUSD          float64    `json:"creditUSD"`
	ExpiredAt          *time.Time `json:"expiredAt"`
}

func checkoutView(o PaymentOrder) CheckoutView {
	return CheckoutView{OrderNo: o.OrderNo, OrderType: o.OrderType, Provider: o.Provider, Status: o.Status, CheckoutURL: o.CheckoutURL,
		ExternalCheckoutID: o.ExternalCheckoutID, BaseAmountCents: o.BaseAmountCents, BaseCurrency: o.BaseCurrency,
		PayAmountCents: o.PayAmountCents, PayCurrency: o.PayCurrency, FxRate: o.FxRate, CreditNanousd: o.CreditNanousd,
		CreditUSD: NanoToUSD(o.CreditNanousd), ExpiredAt: o.ExpiredAt}
}

func newOrderNo(now time.Time) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "pay_" + now.UTC().Format("20060102150405") + "_" + hex.EncodeToString(b[:]), nil
}

func fxOrDefault(rate float64) float64 {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return defaultUSDToCNY
	}
	return rate
}

// payQuote is the payable amount and conversion of an order.
type payQuote struct {
	PayCurrency string
	PayCents    int64
	FxRate      string
}

// quoteSubscription converts a USD base amount into the pay currency.
func quoteSubscription(baseCurrency string, baseCents int64, provider, display string, rate float64) (payQuote, error) {
	pay := baseCurrency
	if provider == ProviderEpay || display == CurrencyCNY {
		pay = CurrencyCNY
	}
	q := payQuote{PayCurrency: pay, PayCents: baseCents, FxRate: "1"}
	if pay != baseCurrency {
		if baseCurrency != CurrencyUSD || pay != CurrencyCNY {
			return q, ErrInvalidOrder
		}
		rate = fxOrDefault(rate)
		q.PayCents = int64(math.Round(float64(baseCents) * rate))
		q.FxRate = formatFloat(rate)
	}
	if q.PayCents <= 0 {
		return q, ErrInvalidOrder
	}
	return q, nil
}

// topupQuote prices a top-up entered in the display currency.
type topupQuote struct {
	payQuote
	CreditNano int64
	BaseCents  int64
}

func quoteTopup(entered int64, provider, display string, rate float64) (topupQuote, error) {
	if entered <= 0 || entered > 1_000_000_000_000 {
		return topupQuote{}, ErrInvalidOrder
	}
	if display != CurrencyUSD && display != CurrencyCNY {
		return topupQuote{}, ErrInvalidOrder
	}
	rate = fxOrDefault(rate)
	usd := float64(entered) / 100
	if display == CurrencyCNY {
		usd = usd / rate
	}
	tq := topupQuote{CreditNano: USDToNano(usd), BaseCents: int64(math.Round(usd * 100))}
	payCur := CurrencyUSD
	if provider == ProviderEpay || display == CurrencyCNY {
		payCur = CurrencyCNY
	}
	tq.PayCurrency = payCur
	switch {
	case payCur == display:
		tq.PayCents = entered
		tq.FxRate = "1"
		if display == CurrencyCNY {
			tq.FxRate = formatFloat(rate)
		}
	case payCur == CurrencyCNY: // entered in USD, paying CNY
		tq.PayCents = int64(math.Round(usd * rate * 100))
		tq.FxRate = formatFloat(rate)
	default:
		return tq, ErrInvalidOrder
	}
	if tq.CreditNano <= 0 || tq.PayCents <= 0 {
		return tq, ErrInvalidOrder
	}
	return tq, nil
}

type gatewayPrep struct {
	provider   string
	successURL string
	cancelURL  string
	notifyURL  string
	epayType   string
	epayGW     string
}

func unavailable503() error { return ErrProviderUnavailable.WithStatus(503) }

// prepareProvider validates provider configuration and resolves addresses.
func (s *Service) prepareProvider(rs RuntimeSettings, req CheckoutRequest, ri RequestInfo) (gatewayPrep, error) {
	prep := gatewayPrep{}
	provider := strings.ToLower(strings.TrimSpace(req.PaymentProvider))
	if provider == "" {
		if len(rs.PaymentProviders) == 0 {
			return prep, ErrProviderUnavailable
		}
		provider = rs.PaymentProviders[0]
	} else if !rs.hasProvider(provider) {
		return prep, ErrProviderUnavailable
	}
	prep.provider = provider
	cfg := s.cfg.Load()
	webBase, err := publicBase(cfg, cfg.Server.PublicWebBaseURL, ri, true)
	if err != nil {
		return prep, unavailable503()
	}
	switch provider {
	case ProviderStripe:
		if rs.StripeSecretKey == "" {
			return prep, unavailable503()
		}
		if prep.successURL, err = resolveReturnURL(webBase, req.SuccessURL, "/settings/subscription?payment=success"); err != nil {
			return prep, err
		}
		if prep.cancelURL, err = resolveReturnURL(webBase, req.CancelURL, "/settings/subscription?payment=cancel"); err != nil {
			return prep, err
		}
	case ProviderEpay:
		if rs.EpayPID == "" || rs.EpayKey == "" {
			return prep, unavailable503()
		}
		if rs.EpayGatewayURL == "" {
			return prep, unavailable503()
		}
		if _, err := normalizeEpayGateway(rs.EpayGatewayURL); err != nil {
			return prep, ErrEpayGatewayInvalid
		}
		prep.epayGW = rs.EpayGatewayURL
		typ := strings.TrimSpace(req.EpayType)
		if typ == "" {
			typ = rs.EpayTypes[0].Type
		} else {
			ok := false
			for _, t := range rs.EpayTypes {
				if t.Type == typ {
					ok = true
				}
			}
			if !ok {
				return prep, ErrEpayTypeUnsupported
			}
		}
		prep.epayType = typ
		apiBase, err := publicBase(cfg, cfg.Server.PublicAPIBaseURL, ri, false)
		if err != nil {
			return prep, unavailable503()
		}
		prep.notifyURL = apiBase + "/api/v1/billing/payments/epay/notify"
		if prep.successURL, err = resolveReturnURL(webBase, req.SuccessURL, "/settings/subscription?payment=success"); err != nil {
			return prep, err
		}
	}
	return prep, nil
}

func orderTypeOf(req CheckoutRequest) string {
	t := strings.TrimSpace(req.OrderType)
	if t == OrderSubscription || t == OrderTopup {
		return t
	}
	if req.PriceID == 0 && req.AmountMinorUnits > 0 {
		return OrderTopup
	}
	return OrderSubscription
}

// Checkout creates a pending order and the gateway checkout for it.
func (s *Service) Checkout(ctx context.Context, userID uint, req CheckoutRequest, ri RequestInfo, audit func(PaymentOrder)) (*CheckoutView, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	prep, err := s.prepareProvider(rs, req, ri)
	if err != nil {
		return nil, err
	}
	order, planName, planDesc, err := s.createOrder(ctx, userID, rs, prep.provider, req)
	if err != nil {
		if errors.Is(err, ErrPaymentRequired) || errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrPlanNotFound) || errors.Is(err, ErrInvalidOrder) {
			if te, ok := asTyped(err); ok {
				return nil, te.WithStatus(400)
			}
		}
		return nil, err
	}
	switch prep.provider {
	case ProviderStripe:
		name, desc := stripeProductText(*order, planName, planDesc)
		sess, err := s.createStripeSession(ctx, stripeSessionParams{
			SecretKey: rs.StripeSecretKey, SuccessURL: prep.successURL, CancelURL: prep.cancelURL, OrderNo: order.OrderNo,
			OrderType: order.OrderType, UserID: userID, BaseCurrency: order.BaseCurrency, BaseAmountCents: order.BaseAmountCents,
			PayCurrency: order.PayCurrency, PayAmountCents: order.PayAmountCents, FxRate: order.FxRate, ProductName: name, ProductDesc: desc,
		})
		if err != nil {
			s.log.Warn("billing_stripe_checkout_failed")
			return nil, ErrCheckoutFailed.WithCause(err)
		}
		order.ExternalCheckoutID, order.CheckoutURL = sess.ID, sess.URL
	case ProviderEpay:
		name, _ := stripeProductText(*order, planName, planDesc)
		u, err := buildEpayURL(epayParams{Gateway: prep.epayGW, PID: rs.EpayPID, Key: rs.EpayKey, Type: prep.epayType,
			OrderNo: order.OrderNo, NotifyURL: prep.notifyURL, ReturnURL: prep.successURL, Name: name,
			PayCurrency: order.PayCurrency, PayCents: order.PayAmountCents})
		if err != nil {
			if errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrEpayGatewayInvalid) {
				return nil, err
			}
			return nil, ErrCheckoutFailed.WithCause(err)
		}
		order.CheckoutURL = u
	}
	if err := s.db.WithContext(ctx).Model(&PaymentOrder{}).Where("id = ?", order.ID).
		Updates(map[string]any{"external_checkout_id": order.ExternalCheckoutID, "checkout_url": order.CheckoutURL}).Error; err != nil {
		return nil, err
	}
	if audit != nil {
		audit(*order)
	}
	v := checkoutView(*order)
	return &v, nil
}

func asTyped(err error) (*typedErr, bool) {
	var te *typedErr
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

func stripeProductText(o PaymentOrder, planName, planDesc string) (string, string) {
	if o.OrderType == OrderTopup {
		cur := firstNonEmpty(o.PayCurrency, o.BaseCurrency, CurrencyUSD)
		amount := o.PayAmountCents
		if amount == 0 {
			amount = o.BaseAmountCents
		}
		return "Balance top-up", fmt.Sprintf("Credit in %s: %s", cur, formatMoney(amount))
	}
	if planName == "" {
		return "Subscription", "Subscription"
	}
	return planName, firstNonEmpty(planDesc, planName)
}

// createOrder validates the request against the billing mode and persists a
// pending order. It also returns the plan name/description for product text.
func (s *Service) createOrder(ctx context.Context, userID uint, rs RuntimeSettings, provider string, req CheckoutRequest) (*PaymentOrder, string, string, error) {
	now := s.now()
	orderNo, err := newOrderNo(now)
	if err != nil {
		return nil, "", "", err
	}
	exp := now.Add(orderLifetime)
	o := &PaymentOrder{OrderNo: orderNo, OrderType: orderTypeOf(req), UserID: userID, Provider: provider, Status: OrderPending,
		ExpiredAt: &exp, Cycles: 1}
	var planName, planDesc string
	snapshot := map[string]any{"provider": provider}
	switch o.OrderType {
	case OrderTopup:
		if rs.Mode != ModeUsage && rs.Mode != ModePeriod {
			return nil, "", "", ErrPaymentRequired
		}
		tq, err := quoteTopup(req.AmountMinorUnits, provider, rs.DisplayCurrency, rs.USDToCNYRate)
		if err != nil {
			return nil, "", "", err
		}
		o.BaseCurrency, o.BaseAmountCents = CurrencyUSD, tq.BaseCents
		o.PayCurrency, o.PayAmountCents, o.FxRate = tq.PayCurrency, tq.PayCents, tq.FxRate
		o.CreditNanousd, o.BillingInterval, o.Cycles = tq.CreditNano, IntervalLifetime, 1
		snapshot["entered_amount_minor_units"] = req.AmountMinorUnits
		snapshot["entered_currency"] = rs.DisplayCurrency
		snapshot["credit_nanousd"] = tq.CreditNano
	default:
		if rs.Mode != ModePeriod {
			return nil, "", "", ErrPaymentRequired
		}
		if provider != ProviderStripe && provider != ProviderEpay {
			return nil, "", "", ErrProviderUnavailable
		}
		cycles := req.Cycles
		if cycles < 1 {
			cycles = 1
		}
		var price Price
		if err := s.db.WithContext(ctx).First(&price, req.PriceID).Error; err != nil {
			if database.IsNotFound(err) {
				return nil, "", "", ErrPlanNotFound
			}
			return nil, "", "", err
		}
		var plan Plan
		if err := s.db.WithContext(ctx).First(&plan, price.PlanID).Error; err != nil {
			if database.IsNotFound(err) {
				return nil, "", "", ErrPlanNotFound
			}
			return nil, "", "", err
		}
		if !price.IsActive || !plan.IsActive {
			return nil, "", "", ErrInvalidOrder
		}
		interval := normalizeInterval(price.BillingInterval)
		if price.AmountCents <= 0 || interval == IntervalLifetime {
			return nil, "", "", ErrInvalidOrder
		}
		o.PlanID, o.PriceID = plan.ID, price.ID
		o.BaseCurrency = firstNonEmpty(price.Currency, CurrencyUSD)
		o.BaseAmountCents = price.AmountCents * int64(cycles)
		o.BillingInterval, o.Cycles = interval, cycles
		q, err := quoteSubscription(o.BaseCurrency, o.BaseAmountCents, provider, rs.DisplayCurrency, rs.USDToCNYRate)
		if err != nil {
			return nil, "", "", err
		}
		o.PayCurrency, o.PayAmountCents, o.FxRate = q.PayCurrency, q.PayCents, q.FxRate
		planName, planDesc = firstNonEmpty(plan.Name, plan.Code), firstNonEmpty(plan.Description, plan.Code)
		snapshot["plan_id"], snapshot["price_id"] = plan.ID, price.ID
		snapshot["plan_code"], snapshot["price_code"] = plan.Code, price.Code
		snapshot["plan_name"] = planName
		snapshot["billing_interval"], snapshot["cycles"] = interval, cycles
	}
	snapshot["base_currency"], snapshot["base_amount_cents"] = o.BaseCurrency, o.BaseAmountCents
	snapshot["pay_currency"], snapshot["pay_amount_cents"], snapshot["fx_rate"] = o.PayCurrency, o.PayAmountCents, o.FxRate
	b, _ := json.Marshal(snapshot)
	o.SnapshotJSON = string(b)
	if err := database.Translate(s.db.WithContext(ctx).Create(o).Error); err != nil {
		return nil, "", "", err
	}
	return o, planName, planDesc, nil
}

// CompleteOrder marks an order paid and delivers its goods (idempotent). The
// returned flag is true only when this call delivered them.
func (s *Service) CompleteOrder(ctx context.Context, orderNo, externalPaymentID string, paidAt time.Time) (PaymentOrder, bool, error) {
	if paidAt.IsZero() {
		paidAt = s.now()
	}
	paidAt = paidAt.UTC()
	var order PaymentOrder
	var delivered bool
	var late error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrOrderNotFound
			}
			return err
		}
		if order.Status == OrderPaid {
			return nil
		}
		if order.Status != OrderPending {
			return ErrOrderStateInvalid
		}
		if order.ExpiredAt != nil && order.ExpiredAt.Before(paidAt) {
			if err := tx.Model(&PaymentOrder{}).Where("id = ?", order.ID).Update("status", OrderExpired).Error; err != nil {
				return err
			}
			order.Status = OrderExpired
			late = ErrOrderStateInvalid
			return nil
		}
		switch order.OrderType {
		case OrderTopup:
			if order.CreditNanousd <= 0 {
				return ErrInvalidOrder
			}
			acc, err := lockAccount(tx, order.UserID)
			if err != nil {
				return err
			}
			newBal := acc.BalanceNanousd + order.CreditNanousd
			if err := tx.Model(&Account{}).Where("id = ?", acc.ID).Update("balance_nanousd", newBal).Error; err != nil {
				return err
			}
			if err := tx.Create(&BalanceTransaction{AccountID: acc.ID, UserID: acc.UserID, Type: TxTopup, AmountNanousd: order.CreditNanousd,
				BalanceAfterNanousd: newBal, RefType: "payment_order", RefID: order.ID, RefNo: order.OrderNo, Description: "balance top-up"}).Error; err != nil {
				return err
			}
		default:
			var plan Plan
			var price Price
			if err := tx.First(&plan, order.PlanID).Error; err != nil {
				return ErrPlanNotFound
			}
			if err := tx.First(&price, order.PriceID).Error; err != nil {
				return ErrPlanNotFound
			}
			if !plan.IsActive || !price.IsActive {
				return ErrPlanNotFound
			}
			end, finite := addInterval(paidAt, normalizeInterval(order.BillingInterval), order.Cycles)
			if !finite || !end.After(paidAt) {
				return ErrInvalidOrder
			}
			if _, err := s.grantTime(tx, order.UserID, plan, price, end.Sub(paidAt), paidAt); err != nil {
				return err
			}
		}
		if err := tx.Model(&PaymentOrder{}).Where("id = ?", order.ID).
			Updates(map[string]any{"status": OrderPaid, "external_payment_id": externalPaymentID, "paid_at": paidAt}).Error; err != nil {
			return err
		}
		order.Status, order.ExternalPaymentID, order.PaidAt = OrderPaid, externalPaymentID, &paidAt
		delivered = true
		return nil
	})
	if err != nil {
		return order, false, err
	}
	if late != nil {
		return order, false, late
	}
	return order, delivered, nil
}

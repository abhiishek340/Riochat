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

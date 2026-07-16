package billing

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// WebhookOutcome is the result of a verified payment callback.
type WebhookOutcome struct {
	Ignored bool
	Order   PaymentOrder
	// Delivered is true when this call delivered the goods (activation or
	// credit); repeats report false.
	Delivered bool
}

func (s *Service) loadOrder(ctx context.Context, orderNo string) (*PaymentOrder, error) {
	var o PaymentOrder
	if err := s.db.WithContext(ctx).Where("order_no = ?", orderNo).First(&o).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return &o, nil
}

// StripeWebhookReady reports payment.webhook_not_configured when no webhook
// secret is stored. The HTTP handler calls it before reading the body.
func (s *Service) StripeWebhookReady(ctx context.Context) error {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return err
	}
	if rs.StripeWebhookSecret == "" {
		return ErrWebhookNotConfigured
	}
	return nil
}

// HandleStripeWebhook verifies and applies a Stripe webhook delivery.
func (s *Service) HandleStripeWebhook(ctx context.Context, body []byte, signature string) (*WebhookOutcome, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if rs.StripeWebhookSecret == "" {
		return nil, ErrWebhookNotConfigured
	}
	if err := verifyStripeSignature(signature, body, rs.StripeWebhookSecret, s.now()); err != nil {
		return nil, err
	}
	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, ErrInvalidEvent
	}
	obj := ev.Data.Object
	if ev.Type != "checkout.session.completed" || (obj.PaymentStatus != "" && obj.PaymentStatus != "paid") {
		return &WebhookOutcome{Ignored: true}, nil
	}
	orderNo := strings.TrimSpace(obj.Metadata["order_no"])
	if orderNo == "" {
		orderNo = strings.TrimSpace(obj.ClientReferenceID)
	}
	if orderNo == "" {
		return nil, ErrOrderNoRequired
	}
	order, err := s.loadOrder(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Provider != ProviderStripe ||
		(obj.ID != "" && order.ExternalCheckoutID != "" && obj.ID != order.ExternalCheckoutID) ||
		obj.AmountTotal != order.PayAmountCents || !strings.EqualFold(obj.Currency, order.PayCurrency) {
		return nil, ErrNotificationMismatch
	}
	ext := firstNonEmpty(ev.paymentIntentID(), obj.ID)
	done, delivered, err := s.CompleteOrder(ctx, orderNo, ext, s.now())
	if err != nil {
		return nil, err
	}
	return &WebhookOutcome{Order: done, Delivered: delivered}, nil
}

// HandleEpayNotify verifies and applies an EPay notification. Any error means
// the caller answers "fail".
func (s *Service) HandleEpayNotify(ctx context.Context, params map[string]string) (*WebhookOutcome, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if rs.EpayKey == "" || !verifyEpay(params, rs.EpayKey) {
		return nil, ErrInvalidSignature
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		return &WebhookOutcome{Ignored: true}, nil
	}
	orderNo := strings.TrimSpace(params["out_trade_no"])
	if orderNo == "" {
		return nil, ErrOrderNoRequired
	}
	order, err := s.loadOrder(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if order.Provider != ProviderEpay || params["pid"] != rs.EpayPID || order.PayCurrency != CurrencyCNY ||
		!moneyMatches(params["money"], order.PayAmountCents) {
		return nil, ErrNotificationMismatch
	}
	done, delivered, err := s.CompleteOrder(ctx, orderNo, params["trade_no"], s.now())
	if err != nil {
		return nil, err
	}
	return &WebhookOutcome{Order: done, Delivered: delivered}, nil
}

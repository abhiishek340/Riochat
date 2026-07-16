package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	stripeDefaultBase    = "https://api.stripe.com"
	stripeTimeout        = 15 * time.Second
	stripeResponseLimit  = 1 << 20
	stripeSigTolerance   = 5 * time.Minute
	stripeSessionsPath   = "/v1/checkout/sessions"
	stripeWebhookMaxBody = 1 << 20
)

type stripeSessionParams struct {
	SecretKey       string
	SuccessURL      string
	CancelURL       string
	OrderNo         string
	OrderType       string
	UserID          uint
	BaseCurrency    string
	BaseAmountCents int64
	PayCurrency     string
	PayAmountCents  int64
	FxRate          string
	ProductName     string
	ProductDesc     string
}

type stripeSession struct{ ID, URL string }

// createStripeSession creates a hosted Checkout Session.
func (s *Service) createStripeSession(ctx context.Context, p stripeSessionParams) (stripeSession, error) {
	base := s.ports.StripeBaseURL
	if base == "" {
		base = stripeDefaultBase
	}
	cur := strings.ToLower(firstNonEmpty(p.PayCurrency, p.BaseCurrency, CurrencyUSD))
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	form.Set("client_reference_id", p.OrderNo)
	form.Set("metadata[order_no]", p.OrderNo)
	form.Set("metadata[order_type]", p.OrderType)
	form.Set("metadata[user_id]", strconv.FormatUint(uint64(p.UserID), 10))
	form.Set("metadata[base_currency]", p.BaseCurrency)
	form.Set("metadata[base_amount_cents]", strconv.FormatInt(p.BaseAmountCents, 10))
	form.Set("metadata[pay_currency]", p.PayCurrency)
	form.Set("metadata[pay_amount_cents]", strconv.FormatInt(p.PayAmountCents, 10))
	form.Set("metadata[fx_rate]", p.FxRate)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", cur)
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(p.PayAmountCents, 10))
	form.Set("line_items[0][price_data][product_data][name]", p.ProductName)
	if p.ProductDesc != "" {
		form.Set("line_items[0][price_data][product_data][description]", p.ProductDesc)
	}
	ctx, cancel := context.WithTimeout(ctx, stripeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+stripeSessionsPath, strings.NewReader(form.Encode()))
	if err != nil {
		return stripeSession{}, err
	}
	req.SetBasicAuth(p.SecretKey, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return stripeSession{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, stripeResponseLimit))
	if err != nil {
		return stripeSession{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return stripeSession{}, fmt.Errorf("stripe: unexpected status %d", resp.StatusCode)
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return stripeSession{}, err
	}
	u, err := url.Parse(out.URL)
	if out.ID == "" || err != nil || u.Scheme != "https" || u.Host == "" {
		return stripeSession{}, errors.New("stripe: invalid session response")
	}
	return stripeSession{ID: out.ID, URL: out.URL}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// stripeSignature returns the v1 signature for a payload (used by tests and
// verification).
func stripeSignature(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// verifyStripeSignature checks a Stripe-Signature header: a t= timestamp within
// five minutes of now and at least one matching v1= HMAC-SHA256 signature.
func verifyStripeSignature(header string, body []byte, secret string, now time.Time) error {
	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ErrInvalidSignature
			}
			ts = n
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 || len(sigs) == 0 || secret == "" {
		return ErrInvalidSignature
	}
	diff := now.Sub(time.Unix(ts, 0))
	if diff < -stripeSigTolerance || diff > stripeSigTolerance {
		return ErrInvalidSignature
	}
	want := stripeSignature(secret, ts, body)
	for _, got := range sigs {
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(want)) == 1 {
			return nil
		}
	}
	return ErrInvalidSignature
}

// stripeEvent is the part of a webhook event the area reads.
type stripeEvent struct {
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID                string            `json:"id"`
			ClientReferenceID string            `json:"client_reference_id"`
			PaymentStatus     string            `json:"payment_status"`
			PaymentIntent     json.RawMessage   `json:"payment_intent"`
			AmountTotal       int64             `json:"amount_total"`
			Currency          string            `json:"currency"`
			Metadata          map[string]string `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

// paymentIntentID reads the payment intent reference, which Stripe sends as an
// id string or as an expanded object.
func (e stripeEvent) paymentIntentID() string {
	raw := e.Data.Object.PaymentIntent
	if len(raw) == 0 {
		return ""
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var obj struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.ID
	}
	return ""
}

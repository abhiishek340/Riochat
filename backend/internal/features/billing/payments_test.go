package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const (
	testStripeSecret  = "sk_test_123"
	testStripeWebhook = "whsec_test"
	testEpayKey       = "epay-merchant-key"
	testEpayPID       = "1001"
)

type stripeFake struct {
	srv    *httptest.Server
	calls  int
	form   url.Values
	user   string
	status int
	body   string
}

func newStripeFake(t *testing.T) *stripeFake {
	f := &stripeFake{status: 200}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		_ = r.ParseForm()
		f.form = r.PostForm
		f.user, _, _ = r.BasicAuth()
		if r.URL.Path != "/v1/checkout/sessions" || r.Method != "POST" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(f.status)
		if f.body != "" {
			_, _ = w.Write([]byte(f.body))
			return
		}
		_, _ = w.Write([]byte(`{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func payEnv(t *testing.T, st *stripeFake, opts ...envOpt) *env {
	base := map[string]string{
		KeyMode: "period", KeyPaymentProviders: "stripe,epay",
		KeyStripeSecretKey: testStripeSecret, KeyStripeWebhookSecret: testStripeWebhook,
		KeyEpayGatewayURL: "https://pay.example.com/gw", KeyEpayPID: testEpayPID, KeyEpayKey: testEpayKey,
	}
	opts = append(opts, withSeed(base), withPorts(func(p *Ports) {
		if st != nil {
			p.StripeBaseURL = st.srv.URL
		}
	}))
	return newEnv(t, opts...)
}

type checkoutOut struct {
	Checkout CheckoutView `json:"checkout"`
}

func (e *env) checkout(uid uint, body map[string]any) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/billing/payments/checkout", e.user(uid), body)
}

func TestCheckoutStripeSubscription(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	w := e.checkout(5, map[string]any{"priceID": e.priceID("pro-monthly"), "paymentProvider": "stripe"})
	testkit.Expect(t, w, 200, "")
	c := decodeData[checkoutOut](t, w).Checkout
	if c.OrderType != "subscription" || c.Provider != "stripe" || c.Status != "pending" || c.PayCurrency != "USD" || c.PayAmountCents != 2000 ||
		c.BaseAmountCents != 2000 || c.FxRate != "1" || c.ExternalCheckoutID != "cs_test_1" || !strings.HasPrefix(c.CheckoutURL, "https://checkout.stripe.com/") {
		t.Fatalf("checkout %+v", c)
	}
	if !strings.HasPrefix(c.OrderNo, "pay_20260301090000_") || len(c.OrderNo) != len("pay_20260301090000_")+16 {
		t.Fatalf("order no %s", c.OrderNo)
	}
	if c.ExpiredAt == nil || !c.ExpiredAt.Equal(e.Clock.Now().Add(30*time.Minute)) {
		t.Fatalf("expiry %v", c.ExpiredAt)
	}
	// the gateway request
	f := st.form
	if st.user != testStripeSecret || f.Get("mode") != "payment" || f.Get("client_reference_id") != c.OrderNo || f.Get("metadata[order_no]") != c.OrderNo ||
		f.Get("metadata[user_id]") != "5" || f.Get("line_items[0][price_data][currency]") != "usd" || f.Get("line_items[0][price_data][unit_amount]") != "2000" ||
		f.Get("line_items[0][quantity]") != "1" || f.Get("metadata[pay_amount_cents]") != "2000" || f.Get("metadata[fx_rate]") != "1" {
		t.Fatalf("stripe form %v", f)
	}
	if f.Get("line_items[0][price_data][product_data][name]") != "Pro" || !strings.Contains(f.Get("success_url"), "/settings/subscription?payment=success") ||
		!strings.Contains(f.Get("cancel_url"), "payment=cancel") {
		t.Fatalf("stripe form %v", f)
	}
	// the order row
	var o PaymentOrder
	if err := e.DB.Where("order_no = ?", c.OrderNo).First(&o).Error; err != nil {
		t.Fatal(err)
	}
	if o.UserID != 5 || o.PlanID == 0 || o.Cycles != 1 || o.BillingInterval != "month" || o.CheckoutURL != c.CheckoutURL || o.ExternalCheckoutID != "cs_test_1" {
		t.Fatalf("order %+v", o)
	}
	var snap map[string]any
	_ = json.Unmarshal([]byte(o.SnapshotJSON), &snap)
	if snap["plan_code"] != "pro" || snap["provider"] != "stripe" || snap["pay_amount_cents"].(float64) != 2000 {
		t.Fatalf("snapshot %v", snap)
	}
	if en, ok := e.audit.last("billing.payment.checkout"); !ok || en.ResourceID != c.OrderNo || en.Resource != "billing_payment_order" || en.ActorUserID != 5 {
		t.Fatalf("audit %+v", en)
	}
}

func TestCheckoutCyclesAndCNYDisplay(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	e.setSettings(map[string]string{KeyDisplayCurrency: "CNY"})
	w := e.checkout(5, map[string]any{"priceID": e.priceID("pro-monthly"), "paymentProvider": "stripe", "cycles": 3})
	c := decodeData[checkoutOut](t, w).Checkout
	if c.BaseAmountCents != 6000 || c.PayCurrency != "CNY" || c.PayAmountCents != 43200 || c.FxRate != "7.2" {
		t.Fatalf("%+v", c)
	}
	if st.form.Get("line_items[0][price_data][currency]") != "cny" {
		t.Fatalf("form %v", st.form)
	}
}

func TestCheckoutEpaySubscription(t *testing.T) {
	e := payEnv(t, nil)
	w := e.checkout(5, map[string]any{"priceID": e.priceID("pro-monthly"), "paymentProvider": "epay", "epayType": "wxpay", "successURL": "/done"})
	testkit.Expect(t, w, 200, "")
	c := decodeData[checkoutOut](t, w).Checkout
	if c.PayCurrency != "CNY" || c.PayAmountCents != 14400 || c.FxRate != "7.2" || c.ExternalCheckoutID != "" {
		t.Fatalf("%+v", c)
	}
	u, err := url.Parse(c.CheckoutURL)
	if err != nil || u.Host != "pay.example.com" || u.Path != "/gw/submit.php" {
		t.Fatalf("url %s", c.CheckoutURL)
	}
	q := u.Query()
	if q.Get("money") != "144.00" || q.Get("pid") != testEpayPID || q.Get("type") != "wxpay" || q.Get("out_trade_no") != c.OrderNo ||
		q.Get("sign_type") != "MD5" || !strings.HasSuffix(q.Get("notify_url"), "/api/v1/billing/payments/epay/notify") ||
		!strings.HasSuffix(q.Get("return_url"), "/done") || q.Get("name") == "" {
		t.Fatalf("query %v", q)
	}
	params := map[string]string{}
	for k := range q {
		params[k] = q.Get(k)
	}
	if !verifyEpay(params, testEpayKey) || verifyEpay(params, "other") {
		t.Fatal("signature does not verify")
	}
	// default type is the first configured one
	w = e.checkout(5, map[string]any{"priceID": e.priceID("pro-monthly"), "paymentProvider": "epay"})
	c = decodeData[checkoutOut](t, w).Checkout
	u, _ = url.Parse(c.CheckoutURL)
	if u.Query().Get("type") != "alipay" {
		t.Fatalf("default type %s", u.Query().Get("type"))
	}
}

func TestCheckoutTopup(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	// USD display: 5000 minor units -> 50 USD
	w := e.checkout(5, map[string]any{"amountMinorUnits": 5000, "paymentProvider": "stripe"})
	testkit.Expect(t, w, 200, "")
	c := decodeData[checkoutOut](t, w).Checkout
	if c.OrderType != "topup" || c.CreditNanousd != 50_000_000_000 || c.PayCurrency != "USD" || c.PayAmountCents != 5000 || c.BaseAmountCents != 5000 {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(st.form.Get("line_items[0][price_data][product_data][name]"), "top-up") {
		t.Fatalf("product %v", st.form)
	}
	// USD entered, paid through EPay in CNY
	w = e.checkout(5, map[string]any{"amountMinorUnits": 5000, "paymentProvider": "epay"})
	c = decodeData[checkoutOut](t, w).Checkout
	if c.PayCurrency != "CNY" || c.PayAmountCents != 36000 || c.FxRate != "7.2" || c.CreditNanousd != 50_000_000_000 {
		t.Fatalf("epay topup %+v", c)
	}
	// CNY display: the entered amount is exactly what is paid
	e.setSettings(map[string]string{KeyDisplayCurrency: "CNY"})
	w = e.checkout(5, map[string]any{"amountMinorUnits": 1000, "paymentProvider": "epay"})
	c = decodeData[checkoutOut](t, w).Checkout
	if c.PayAmountCents != 1000 || c.PayCurrency != "CNY" || c.CreditNanousd != 1_388_888_889 || c.BaseAmountCents != 139 {
		t.Fatalf("cny topup %+v", c)
	}
	// top-up is allowed in usage mode, refused in self mode
	e.mode("usage")
	testkit.Expect(t, e.checkout(5, map[string]any{"amountMinorUnits": 1000, "paymentProvider": "epay"}), 200, "")
	e.mode("self")
	testkit.Expect(t, e.checkout(5, map[string]any{"amountMinorUnits": 1000, "paymentProvider": "epay"}), 400, "billing.payment_required")
	e.mode("period")
	// invalid amount
	testkit.Expect(t, e.checkout(5, map[string]any{"orderType": "topup", "amountMinorUnits": 0, "paymentProvider": "epay"}), 400, "payment.invalid_order")
	testkit.Expect(t, e.checkout(5, map[string]any{"orderType": "topup", "amountMinorUnits": -5}), 400, "request.invalid_body")
}

func TestCheckoutErrors(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	price := e.priceID("pro-monthly")
	// subscription requires period mode
	e.mode("usage")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price}), 400, "billing.payment_required")
	e.mode("period")
	// unknown / inactive / free prices
	// domain failures of the checkout flow answer 400 (spec 9.4 step 4)
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": 9999}), 400, "billing.plan_not_found")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": e.priceID("free-default")}), 400, "payment.invalid_order")
	// unsupported epay type
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "epay", "epayType": "paypal"}), 400, "payment.epay_type_unsupported")
	// cross origin and malformed return urls
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "successURL": "https://evil.example.org/x"}), 400, "payment.return_url_cross_origin")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "cancelURL": "//evil.example.org/x"}), 400, "payment.return_url_cross_origin")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "successURL": "relative/path"}), 400, "payment.return_url_invalid")
	// body validation
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "paypal"}), 400, "request.invalid_body")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "cycles": 121}), 400, "request.invalid_body")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "orderType": "gift"}), 400, "request.invalid_body")
	// provider not enabled
	e.setSettings(map[string]string{KeyPaymentProviders: "epay"})
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe"}), 400, "payment.provider_unavailable")
	// no provider enabled
	e.setSettings(map[string]string{KeyPaymentProviders: "disabled"})
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price}), 400, "payment.provider_unavailable")
	// failed requests created no orders
	var n int64
	e.DB.Model(&PaymentOrder{}).Count(&n)
	if n != 0 {
		t.Fatalf("orders %d", n)
	}
}

func TestCheckoutDefaultsToFirstEnabledProvider(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	e.setSettings(map[string]string{KeyPaymentProviders: "epay,stripe"})
	c := decodeData[checkoutOut](t, e.checkout(5, map[string]any{"priceID": e.priceID("pro-monthly")})).Checkout
	if c.Provider != "epay" {
		t.Fatalf("provider %s", c.Provider)
	}
}

func TestCheckoutProviderMisconfigured(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	price := e.priceID("pro-monthly")
	e.setSettings(map[string]string{KeyStripeSecretKey: ""})
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe"}), 503, "payment.provider_unavailable")
	e.setSettings(map[string]string{KeyEpayKey: ""})
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "epay"}), 503, "payment.provider_unavailable")
	e.setSettings(map[string]string{KeyEpayKey: testEpayKey, KeyEpayGatewayURL: ""})
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "epay"}), 503, "payment.provider_unavailable")
	// a bad stored gateway (bypassing validation) is reported as invalid
	e.settings.mu.Lock()
	e.settings.m[KeyEpayGatewayURL] = "ftp://pay.example.com"
	e.settings.mu.Unlock()
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "epay"}), 503, "payment.epay_gateway_invalid")
}

func TestCheckoutGatewayFailure(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	price := e.priceID("pro-monthly")
	for _, tc := range []struct {
		status int
		body   string
	}{{500, "oops"}, {200, `{"id":"","url":"https://x.example/c"}`}, {200, `{"id":"cs","url":"http://insecure.example/c"}`}, {200, `not json`}} {
		st.status, st.body = tc.status, tc.body
		testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe"}), 502, "payment.checkout_failed")
	}
	// the pending orders remain
	var n int64
	e.DB.Model(&PaymentOrder{}).Where("status = ?", "pending").Count(&n)
	if n != 4 {
		t.Fatalf("pending orders %d", n)
	}
}

func TestCheckoutConfiguredPublicWebBase(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st, withKit(testkit.WithConfig(func(c *config.Config) { c.Server.PublicWebBaseURL = "https://app.example.com" })))
	price := e.priceID("pro-monthly")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "successURL": "https://app.example.com/ok"}), 200, "")
	if st.form.Get("success_url") != "https://app.example.com/ok" {
		t.Fatalf("success %s", st.form.Get("success_url"))
	}
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "successURL": "https://APP.example.com/ok2"}), 200, "")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe", "successURL": "http://app.example.com/ok"}), 400, "payment.return_url_cross_origin")
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price, "paymentProvider": "stripe"}), 200, "")
	if st.form.Get("success_url") != "https://app.example.com/settings/subscription?payment=success" {
		t.Fatalf("default success %s", st.form.Get("success_url"))
	}
}

func TestPublicBaseProductionNeedsConfiguredAddresses(t *testing.T) {
	cfg := config.Default()
	cfg.App.Env = config.EnvProd
	ri := RequestInfo{Origin: "https://attacker.example", Host: "attacker.example"}
	if _, err := publicBase(&cfg, "", ri, true); err == nil {
		t.Fatal("production must not derive the base from request headers")
	}
	if b, err := publicBase(&cfg, "https://app.example.com/", ri, true); err != nil || b != "https://app.example.com" {
		t.Fatalf("%s %v", b, err)
	}
	cfg.App.Env = config.EnvDev
	if b, _ := publicBase(&cfg, "", ri, true); b != "https://attacker.example" {
		t.Fatalf("origin %s", b)
	}
	if b, _ := publicBase(&cfg, "", RequestInfo{ForwardedProto: "https", ForwardedHost: "fw.example", Host: "int"}, false); b != "https://fw.example" {
		t.Fatalf("forwarded %s", b)
	}
	if b, _ := publicBase(&cfg, "", RequestInfo{Host: "h.example"}, false); b != "http://h.example" {
		t.Fatalf("host %s", b)
	}
}

func TestResolveReturnURL(t *testing.T) {
	base := "https://app.example.com"
	ok := map[string]string{"": base + "/d", "/a/b?x=1": base + "/a/b?x=1", "https://app.example.com/z": "https://app.example.com/z"}
	for in, want := range ok {
		got, err := resolveReturnURL(base, in, "/d")
		if err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := resolveReturnURL(base, "//evil.com", "/d"); !isCode(err, ErrReturnURLCrossOrigin) {
		t.Errorf("protocol relative: %v", err)
	}
	if _, err := resolveReturnURL(base, "https://evil.com", "/d"); !isCode(err, ErrReturnURLCrossOrigin) {
		t.Errorf("other origin: %v", err)
	}
	if _, err := resolveReturnURL(base, "javascript:alert(1)", "/d"); !isCode(err, ErrReturnURLInvalid) {
		t.Errorf("javascript: %v", err)
	}
	if _, err := resolveReturnURL(base, "foo/bar", "/d"); !isCode(err, ErrReturnURLInvalid) {
		t.Errorf("relative: %v", err)
	}
}

func TestEpayGatewayNormalization(t *testing.T) {
	ok := map[string]string{
		"https://pay.example.com":               "https://pay.example.com/submit.php",
		"https://pay.example.com/":              "https://pay.example.com/submit.php",
		"https://pay.example.com/api/":          "https://pay.example.com/api/submit.php",
		"https://pay.example.com/Submit.PHP":    "https://pay.example.com/Submit.PHP",
		"https://pay.example.com/x/submit.php/": "https://pay.example.com/x/submit.php",
		"http://pay.example.com:8080/gw":        "http://pay.example.com:8080/gw/submit.php",
	}
	for in, want := range ok {
		got, err := normalizeEpayGateway(in)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "ftp://x.com", "https://u:p@x.com", "https://x.com/?a=1", "https://x.com/#f", "https://", "not a url", "x.com"} {
		if _, err := normalizeEpayGateway(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEpaySignatureAndMoney(t *testing.T) {
	p := map[string]string{"b": "2", "a": "1", "empty": " ", "sign": "zzz", "sign_type": "MD5"}
	want := "a=1&b=2" + "k"
	// md5("a=1&b=2k")
	if got := epaySign(p, "k"); got != md5hex(want) {
		t.Fatalf("sign %s", got)
	}
	p["sign"] = strings.ToUpper(epaySign(p, "k"))
	if !verifyEpay(p, "k") {
		t.Fatal("uppercase signature should verify")
	}
	if verifyEpay(p, "") || verifyEpay(map[string]string{"a": "1"}, "k") {
		t.Fatal("empty key / missing sign must fail")
	}
	for in, ok := range map[string]bool{"144.00": true, "144": true, "144.0": true, "144.001": true, "143.99": false, "abc": false, "": false} {
		if moneyMatches(in, 14400) != ok {
			t.Errorf("moneyMatches(%q)=%v", in, !ok)
		}
	}
	if formatMoney(5) != "0.05" || formatMoney(14400) != "144.00" || formatMoney(123456) != "1234.56" {
		t.Fatal("formatMoney")
	}
}

// ---- completion ----

func (e *env) pendingOrder(uid uint, provider string, body map[string]any) PaymentOrder {
	e.t.Helper()
	body["paymentProvider"] = provider
	w := e.checkout(uid, body)
	testkit.Expect(e.t, w, 200, "")
	c := decodeData[checkoutOut](e.t, w).Checkout
	var o PaymentOrder
	if err := e.DB.Where("order_no = ?", c.OrderNo).First(&o).Error; err != nil {
		e.t.Fatal(err)
	}
	return o
}

func (e *env) stripeEvent(o PaymentOrder, mut func(map[string]any)) (*httptest.ResponseRecorder, []byte) {
	obj := map[string]any{"id": o.ExternalCheckoutID, "payment_status": "paid", "amount_total": o.PayAmountCents,
		"currency": strings.ToLower(o.PayCurrency), "payment_intent": "pi_1", "client_reference_id": o.OrderNo,
		"metadata": map[string]any{"order_no": o.OrderNo}}
	ev := map[string]any{"type": "checkout.session.completed", "data": map[string]any{"object": obj}}
	if mut != nil {
		mut(ev)
	}
	raw, _ := json.Marshal(ev)
	return e.postStripe(raw, e.Clock.Now().Unix()), raw
}

func (e *env) postStripe(raw []byte, ts int64) *httptest.ResponseRecorder {
	hdr := map[string]string{"Stripe-Signature": "t=" + strconv.FormatInt(ts, 10) + ",v1=" + stripeSignature(testStripeWebhook, ts, raw)}
	return e.do("POST", "/api/v1/billing/payments/stripe/webhook", hdr, raw)
}

func TestStripeWebhookActivatesSubscriptionOnce(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	o := e.pendingOrder(5, "stripe", map[string]any{"priceID": e.priceID("pro-monthly")})
	e.Clock.Advance(time.Minute)
	w, _ := e.stripeEvent(o, nil)
	testkit.Expect(t, w, 200, "")
	if decodeData[map[string]any](t, w)["ok"] != true {
		t.Fatalf("body %s", w.Body)
	}
	var got PaymentOrder
	e.DB.First(&got, o.ID)
	if got.Status != "paid" || got.PaidAt == nil || got.ExternalPaymentID != "pi_1" {
		t.Fatalf("order %+v", got)
	}
	snap, _ := e.svc.CurrentSubscription(t.Context(), 5)
	paid := e.Clock.Now()
	if snap.Tier != "pro" || snap.ExpiresAt == nil || !snap.ExpiresAt.Equal(paid.AddDate(0, 1, 0)) {
		t.Fatalf("subscription %+v", snap)
	}
	en, ok := e.audit.last("billing.payment.completed")
	if !ok || en.ActorUserID != 5 || en.Detail["source"] != "stripe.webhook" {
		t.Fatalf("audit %+v", en)
	}
	// replay: ok, no extra grant
	w, _ = e.stripeEvent(o, nil)
	testkit.Expect(t, w, 200, "")
	var n int64
	e.DB.Model(&Subscription{}).Where("user_id = 5 AND plan_id = ?", got.PlanID).Count(&n)
	if n != 1 {
		t.Fatalf("subscriptions %d", n)
	}
}

func TestStripeWebhookTopupCreditsOnce(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	o := e.pendingOrder(6, "stripe", map[string]any{"amountMinorUnits": 2500})
	for i := 0; i < 2; i++ {
		w, _ := e.stripeEvent(o, nil)
		testkit.Expect(t, w, 200, "")
	}
	if e.balance(6) != 25_000_000_000 {
		t.Fatalf("balance %d", e.balance(6))
	}
	var txs []BalanceTransaction
	e.DB.Where("user_id = 6").Find(&txs)
	if len(txs) != 1 || txs[0].Type != "topup" || txs[0].RefType != "payment_order" || txs[0].RefID != o.ID || txs[0].RefNo != o.OrderNo || txs[0].Description == "" {
		t.Fatalf("txs %+v", txs)
	}
}

func TestStripeWebhookRejections(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	o := e.pendingOrder(5, "stripe", map[string]any{"priceID": e.priceID("pro-monthly")})
	url := "/api/v1/billing/payments/stripe/webhook"
	// wrong amount
	w, _ := e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["amount_total"] = 1
	})
	testkit.Expect(t, w, 400, "payment.notification_mismatch")
	// wrong currency
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["currency"] = "eur"
	})
	testkit.Expect(t, w, 400, "payment.notification_mismatch")
	// wrong session id
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["id"] = "cs_other"
	})
	testkit.Expect(t, w, 400, "payment.notification_mismatch")
	// uppercase currency is fine for comparison, but the order is still unpaid here
	// unknown order
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		obj := ev["data"].(map[string]any)["object"].(map[string]any)
		obj["metadata"] = map[string]any{"order_no": "nope"}
	})
	testkit.Expect(t, w, 400, "payment.order_not_found")
	// missing order number
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		obj := ev["data"].(map[string]any)["object"].(map[string]any)
		obj["metadata"] = map[string]any{}
		delete(obj, "client_reference_id")
	})
	testkit.Expect(t, w, 400, "payment.order_no_required")
	// fall back to client_reference_id
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["metadata"] = map[string]any{}
	})
	testkit.Expect(t, w, 200, "")
	// ignored events
	w, _ = e.stripeEvent(o, func(ev map[string]any) { ev["type"] = "charge.refunded" })
	testkit.Expect(t, w, 200, "")
	if decodeData[map[string]any](t, w)["ignored"] != true {
		t.Fatalf("body %s", w.Body)
	}
	w, _ = e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["payment_status"] = "unpaid"
	})
	if decodeData[map[string]any](t, w)["ignored"] != true {
		t.Fatalf("unpaid body %s", w.Body)
	}
	// signature problems
	raw := []byte(`{"type":"ping"}`)
	ts := e.Clock.Now().Unix()
	good := "v1=" + stripeSignature(testStripeWebhook, ts, raw)
	cases := map[string]string{
		"missing":       "",
		"stale":         "t=" + strconv.FormatInt(ts-301, 10) + ",v1=" + stripeSignature(testStripeWebhook, ts-301, raw),
		"future":        "t=" + strconv.FormatInt(ts+301, 10) + ",v1=" + stripeSignature(testStripeWebhook, ts+301, raw),
		"wrong secret":  "t=" + strconv.FormatInt(ts, 10) + ",v1=" + stripeSignature("other", ts, raw),
		"bad timestamp": "t=abc," + good,
		"no v1":         "t=" + strconv.FormatInt(ts, 10),
		"bad hex":       "t=" + strconv.FormatInt(ts, 10) + ",v1=zz",
	}
	for name, h := range cases {
		w := e.do("POST", url, map[string]string{"Stripe-Signature": h}, raw)
		if w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
			continue
		}
		testkit.Expect(t, w, 400, "payment.invalid_signature")
	}
	// multiple v1 signatures: any match accepts; edge of the tolerance accepts
	h := "t=" + strconv.FormatInt(ts-300, 10) + ",v1=deadbeef,v1=" + stripeSignature(testStripeWebhook, ts-300, raw)
	testkit.Expect(t, e.do("POST", url, map[string]string{"Stripe-Signature": h}, raw), 200, "")
	// invalid JSON with a valid signature
	bad := []byte(`{not json`)
	testkit.Expect(t, e.postStripe(bad, ts), 400, "payment.invalid_event")
	// too large
	big := make([]byte, stripeWebhookMaxBody+10)
	testkit.Expect(t, e.postStripe(big, ts), 413, "payment.webhook_body_too_large")
	// not configured
	e.setSettings(map[string]string{KeyStripeWebhookSecret: ""})
	testkit.Expect(t, e.postStripe(raw, ts), 400, "payment.webhook_not_configured")
}

func TestStripeWebhookRejectsOrderOfOtherProvider(t *testing.T) {
	e := payEnv(t, newStripeFake(t))
	o := e.pendingOrder(5, "epay", map[string]any{"priceID": e.priceID("pro-monthly")})
	w, _ := e.stripeEvent(o, func(ev map[string]any) {
		ev["data"].(map[string]any)["object"].(map[string]any)["currency"] = "cny"
	})
	testkit.Expect(t, w, 400, "payment.notification_mismatch")
}

func (e *env) epayNotify(method string, o PaymentOrder, mut func(map[string]string)) *httptest.ResponseRecorder {
	p := map[string]string{"pid": testEpayPID, "trade_no": "T123", "out_trade_no": o.OrderNo, "type": "alipay", "name": "x",
		"money": formatMoney(o.PayAmountCents), "trade_status": "TRADE_SUCCESS"}
	if mut != nil {
		mut(p)
	}
	if p["sign"] == "" {
		p["sign"] = epaySign(p, testEpayKey)
	}
	if p["sign"] == "-" {
		delete(p, "sign")
	}
	q := url.Values{}
	for k, v := range p {
		q.Set(k, v)
	}
	if method == "GET" {
		return e.do("GET", "/api/v1/billing/payments/epay/notify?"+q.Encode(), nil, nil)
	}
	req := httptest.NewRequest("POST", "/api/v1/billing/payments/epay/notify", strings.NewReader(q.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:1"
	w := httptest.NewRecorder()
	e.Engine.ServeHTTP(w, req)
	return w
}

func TestEpayNotifyCompletesOrder(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		e := payEnv(t, nil)
		o := e.pendingOrder(5, "epay", map[string]any{"priceID": e.priceID("pro-monthly")})
		w := e.epayNotify(method, o, nil)
		if w.Code != 200 || w.Body.String() != "success" {
			t.Fatalf("%s: %d %q", method, w.Code, w.Body)
		}
		var got PaymentOrder
		e.DB.First(&got, o.ID)
		if got.Status != "paid" || got.ExternalPaymentID != "T123" {
			t.Fatalf("%s order %+v", method, got)
		}
		snap, _ := e.svc.CurrentSubscription(t.Context(), 5)
		if snap.Tier != "pro" {
			t.Fatalf("tier %s", snap.Tier)
		}
		if en, ok := e.audit.last("billing.payment.completed"); !ok || en.Detail["source"] != "epay.notify" {
			t.Fatalf("audit %+v", en)
		}
		// repeat is safe
		w = e.epayNotify(method, o, nil)
		if w.Code != 200 || w.Body.String() != "success" {
			t.Fatalf("repeat: %d %q", w.Code, w.Body)
		}
		var n int64
		e.DB.Model(&Subscription{}).Where("user_id = 5 AND plan_id = ?", got.PlanID).Count(&n)
		if n != 1 {
			t.Fatalf("subscriptions %d", n)
		}
	}
}

func TestEpayNotifyRejections(t *testing.T) {
	e := payEnv(t, nil)
	o := e.pendingOrder(5, "epay", map[string]any{"priceID": e.priceID("pro-monthly")})
	fail := func(name string, mut func(map[string]string)) {
		w := e.epayNotify("GET", o, mut)
		if w.Code != 400 || w.Body.String() != "fail" {
			t.Errorf("%s: %d %q", name, w.Code, w.Body)
		}
	}
	fail("bad signature", func(p map[string]string) { p["sign"] = "0000" })
	fail("missing signature", func(p map[string]string) { p["sign"] = "-" })
	fail("wrong pid", func(p map[string]string) { p["pid"] = "9999" })
	fail("wrong money", func(p map[string]string) { p["money"] = "1.00" })
	fail("unknown order", func(p map[string]string) { p["out_trade_no"] = "nope" })
	fail("missing order", func(p map[string]string) { delete(p, "out_trade_no") })
	// an unrelated status is acknowledged and does nothing
	w := e.epayNotify("GET", o, func(p map[string]string) { p["trade_status"] = "WAIT_BUYER_PAY" })
	if w.Code != 200 || w.Body.String() != "success" {
		t.Fatalf("ignored: %d %q", w.Code, w.Body)
	}
	var got PaymentOrder
	e.DB.First(&got, o.ID)
	if got.Status != "pending" {
		t.Fatalf("order %s", got.Status)
	}
	// no key configured
	e.setSettings(map[string]string{KeyEpayKey: ""})
	w = e.epayNotify("GET", o, nil)
	if w.Code != 400 || w.Body.String() != "fail" {
		t.Fatalf("no key: %d %q", w.Code, w.Body)
	}
}

func TestEpayNotifyForStripeOrderFails(t *testing.T) {
	e := payEnv(t, newStripeFake(t))
	o := e.pendingOrder(5, "stripe", map[string]any{"priceID": e.priceID("pro-monthly")})
	w := e.epayNotify("GET", o, nil)
	if w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
}

func TestLatePaymentExpiresOrder(t *testing.T) {
	e := payEnv(t, newStripeFake(t))
	o := e.pendingOrder(5, "stripe", map[string]any{"priceID": e.priceID("pro-monthly")})
	e.Clock.Advance(31 * time.Minute)
	w, _ := e.stripeEvent(o, nil)
	testkit.Expect(t, w, 400, "payment.order_state_invalid")
	var got PaymentOrder
	e.DB.First(&got, o.ID)
	if got.Status != "expired" {
		t.Fatalf("status %s", got.Status)
	}
	if snap, _ := e.svc.CurrentSubscription(t.Context(), 5); snap.Tier != "free" {
		t.Fatalf("late payment granted %s", snap.Tier)
	}
	// an expired order cannot be completed afterwards either
	w, _ = e.stripeEvent(o, nil)
	testkit.Expect(t, w, 400, "payment.order_state_invalid")
}

func TestCompleteOrderDirect(t *testing.T) {
	e := payEnv(t, nil)
	ctx := context.Background()
	if _, _, err := e.svc.CompleteOrder(ctx, "ghost", "x", time.Time{}); !isCode(err, ErrOrderNotFound) {
		t.Fatalf("%v", err)
	}
	o := e.pendingOrder(5, "epay", map[string]any{"amountMinorUnits": 100})
	done, delivered, err := e.svc.CompleteOrder(ctx, o.OrderNo, "ext", time.Time{})
	if err != nil || !delivered || done.Status != "paid" {
		t.Fatalf("%+v %v %v", done, delivered, err)
	}
	_, delivered, err = e.svc.CompleteOrder(ctx, o.OrderNo, "ext", time.Time{})
	if err != nil || delivered {
		t.Fatalf("repeat delivered=%v err=%v", delivered, err)
	}
	// failed orders cannot be completed
	e.DB.Model(&PaymentOrder{}).Where("id = ?", o.ID).Update("status", "failed")
	if _, _, err := e.svc.CompleteOrder(ctx, o.OrderNo, "ext", time.Time{}); !isCode(err, ErrOrderStateInvalid) {
		t.Fatalf("failed: %v", err)
	}
}

func TestCompletedLifetimeSubscriptionOrderIsRejected(t *testing.T) {
	e := payEnv(t, nil)
	pro := e.planByCode("pro")
	// a paid lifetime price cannot be sold through checkout
	price := Price{PlanID: pro.ID, Code: "pro-life", BillingInterval: "lifetime", Currency: "USD", AmountCents: 9900, IsActive: true}
	e.DB.Create(&price)
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price.ID, "paymentProvider": "epay"}), 400, "payment.invalid_order")
}

func TestPaidOrdersQueueOnTimeline(t *testing.T) {
	e := payEnv(t, nil)
	ctx := context.Background()
	price := e.priceID("pro-monthly")
	for i := 0; i < 2; i++ {
		o := e.pendingOrder(5, "epay", map[string]any{"priceID": price})
		if _, _, err := e.svc.CompleteOrder(ctx, o.OrderNo, "x"+strconv.Itoa(i), time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := e.svc.CurrentSubscription(ctx, 5)
	start := e.Clock.Now()
	if snap.ExpiresAt == nil || !snap.ExpiresAt.Equal(start.AddDate(0, 2, 0)) {
		// the second grant is placed after the first (a month is a month long each)
		first := start.AddDate(0, 1, 0)
		want := first.Add(first.Sub(start))
		if !snap.ExpiresAt.Equal(want) {
			t.Fatalf("expiry %v, want %v or %v", snap.ExpiresAt, start.AddDate(0, 2, 0), want)
		}
	}
}

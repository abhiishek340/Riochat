package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Keys of the `billing` settings namespace.
const (
	KeyMode                     = "mode"
	KeyPrepaidAmountUSD         = "prepaid_amount_usd"
	KeyNativeToolBillingEnabled = "native_tool_billing_enabled"
	KeyNativeToolPricingJSON    = "native_tool_pricing_json"
	KeyUSDToCNYRate             = "usd_to_cny_rate"
	KeyDisplayCurrency          = "display_currency"
	KeyPaymentProviders         = "payment_providers"
	KeyStripePublishableKey     = "stripe_publishable_key"
	KeyStripeSecretKey          = "stripe_secret_key"
	KeyStripeWebhookSecret      = "stripe_webhook_secret"
	KeyEpayGatewayURL           = "epay_gateway_url"
	KeyEpayTypes                = "epay_types"
	KeyEpayPID                  = "epay_pid"
	KeyEpayKey                  = "epay_key"
)

// SensitiveSettingKeys are stored encrypted and never returned by billing APIs.
var SensitiveSettingKeys = []string{KeyStripeSecretKey, KeyStripeWebhookSecret, KeyEpayKey}

const defaultUSDToCNY = 7.2

// EpayType is one payment method offered through the EPay gateway.
type EpayType struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

var defaultEpayTypes = []EpayType{{Name: "Alipay", Type: "alipay"}, {Name: "WeChat Pay", Type: "wxpay"}}

// RuntimeSettings is the normalized view of the billing namespace.
type RuntimeSettings struct {
	Mode                     string
	PrepaidAmountUSD         float64
	PrepaidAmountNanousd     int64
	prepaidErr               error
	NativeToolBillingEnabled bool
	NativeOverrides          map[string]NativeToolOverride
	USDToCNYRate             float64
	DisplayCurrency          string
	PaymentProviders         []string
	EpayTypes                []EpayType
	StripePublishableKey     string
	StripeSecretKey          string
	StripeWebhookSecret      string
	EpayGatewayURL           string
	EpayPID                  string
	EpayKey                  string
}

func normalizeMode(v string) string {
	switch strings.TrimSpace(v) {
	case ModeUsage:
		return ModeUsage
	case ModePeriod:
		return ModePeriod
	}
	return ModeSelf
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}

// parseProviders parses the comma separated provider list. ok is false when an
// unknown name appears.
func parseProviders(v string) (list []string, ok bool) {
	ok = true
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch p {
		case "", "disabled":
			continue
		case ProviderStripe, ProviderEpay:
			if !seen[p] {
				seen[p] = true
				list = append(list, p)
			}
		default:
			ok = false
		}
	}
	return list, ok
}

func validEpayTypeCode(t string) bool {
	if t == "" || len(t) > 32 {
		return false
	}
	for _, r := range t {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// parseEpayTypes reads the epay_types JSON leniently: invalid entries and
// duplicates are dropped, at most 10 are kept, and the defaults apply when
// nothing valid remains.
func parseEpayTypes(v string) []EpayType {
	var raw []EpayType
	if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &raw); err != nil {
		return append([]EpayType(nil), defaultEpayTypes...)
	}
	seen := map[string]bool{}
	var out []EpayType
	for _, t := range raw {
		t.Name, t.Type = strings.TrimSpace(t.Name), strings.TrimSpace(t.Type)
		if t.Name == "" || len([]rune(t.Name)) > 64 || !validEpayTypeCode(t.Type) || seen[t.Type] {
			continue
		}
		seen[t.Type] = true
		out = append(out, t)
		if len(out) == 10 {
			break
		}
	}
	if len(out) == 0 {
		return append([]EpayType(nil), defaultEpayTypes...)
	}
	return out
}

// parseSettings normalizes a raw key/value map (missing keys use defaults).
func parseSettings(m map[string]string) RuntimeSettings {
	rs := RuntimeSettings{
		Mode:                     normalizeMode(m[KeyMode]),
		NativeToolBillingEnabled: parseBool(m[KeyNativeToolBillingEnabled], true),
		USDToCNYRate:             defaultUSDToCNY,
		DisplayCurrency:          CurrencyUSD,
		StripePublishableKey:     strings.TrimSpace(m[KeyStripePublishableKey]),
		StripeSecretKey:          strings.TrimSpace(m[KeyStripeSecretKey]),
		StripeWebhookSecret:      strings.TrimSpace(m[KeyStripeWebhookSecret]),
		EpayGatewayURL:           strings.TrimSpace(m[KeyEpayGatewayURL]),
		EpayPID:                  strings.TrimSpace(m[KeyEpayPID]),
		EpayKey:                  strings.TrimSpace(m[KeyEpayKey]),
	}
	// Prepaid amount: lenient for display, strict (error) for authorization.
	if raw, ok := m[KeyPrepaidAmountUSD]; ok && strings.TrimSpace(raw) != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		switch {
		case err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1_000_000:
			rs.prepaidErr = fmt.Errorf("invalid prepaid amount %q", raw)
		default:
			rs.PrepaidAmountUSD = f
			rs.PrepaidAmountNanousd = USDToNano(f)
		}
	}
	if raw := strings.TrimSpace(m[KeyUSDToCNYRate]); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 && !math.IsInf(f, 0) && !math.IsNaN(f) {
			rs.USDToCNYRate = f
		}
	}
	if m[KeyDisplayCurrency] == CurrencyCNY {
		rs.DisplayCurrency = CurrencyCNY
	}
	rs.PaymentProviders, _ = parseProviders(m[KeyPaymentProviders])
	if rs.PaymentProviders == nil {
		rs.PaymentProviders = []string{}
	}
	if raw, ok := m[KeyEpayTypes]; ok {
		rs.EpayTypes = parseEpayTypes(raw)
	} else {
		rs.EpayTypes = append([]EpayType(nil), defaultEpayTypes...)
	}
	ov, err := parseNativeOverrides(m[KeyNativeToolPricingJSON])
	if err != nil {
		ov = map[string]NativeToolOverride{}
	}
	rs.NativeOverrides = ov
	return rs
}

func (rs RuntimeSettings) hasProvider(p string) bool {
	for _, x := range rs.PaymentProviders {
		if x == p {
			return true
		}
	}
	return false
}

// loadSettings reads and normalizes the billing namespace.
func (s *Service) loadSettings(ctx context.Context) (RuntimeSettings, error) {
	m, err := s.ports.Settings.LoadBilling(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return parseSettings(m), nil
}

// currentMode returns the billing mode, falling back to self on any failure.
func (s *Service) currentMode(ctx context.Context) string {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return ModeSelf
	}
	return rs.Mode
}

func settingErr(field, rule, param string) error {
	d := map[string]any{"field": field, "rule": rule}
	if param != "" {
		d["param"] = param
	}
	return ErrSettingsInvalidValue.WithDetails(d)
}

func paymentSettingErr(field, rule, param string) error {
	d := map[string]any{"field": field, "rule": rule}
	if param != "" {
		d["param"] = param
	}
	return ErrBillingPaymentInvalid.WithDetails(d)
}

// ValidateSettingValues checks a (partial) patch of billing settings. It is
// used by the PATCH config handler and is exported for the settings area.
func ValidateSettingValues(values map[string]string) error {
	for key, v := range values {
		switch key {
		case KeyMode:
			switch strings.TrimSpace(v) {
			case ModeSelf, ModePeriod, ModeUsage:
			default:
				return settingErr(key, "oneof", "self period usage")
			}
		case KeyPrepaidAmountUSD:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1_000_000 {
				return settingErr(key, "range", "0-1000000")
			}
		case KeyNativeToolBillingEnabled:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "false":
			default:
				return settingErr(key, "boolean", "")
			}
		case KeyNativeToolPricingJSON:
			if _, err := parseNativeOverrides(v); err != nil {
				return settingErr(key, "native_tool_pricing", "")
			}
		case KeyUSDToCNYRate:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || math.IsNaN(f) || f < 0.000001 || f > 1000 {
				return settingErr(key, "range", "0.000001-1000")
			}
		case KeyDisplayCurrency:
			if v != CurrencyUSD && v != CurrencyCNY {
				return settingErr(key, "oneof", "USD CNY")
			}
		case KeyPaymentProviders:
			if _, ok := parseProviders(v); !ok {
				return paymentSettingErr(key, "oneof", "stripe epay")
			}
		case KeyStripePublishableKey, KeyStripeSecretKey, KeyStripeWebhookSecret, KeyEpayPID, KeyEpayKey:
			if utf8.RuneCountInString(v) > 512 {
				return paymentSettingErr(key, "max", "512")
			}
		case KeyEpayGatewayURL:
			if utf8.RuneCountInString(v) > 512 {
				return paymentSettingErr(key, "max", "512")
			}
			if strings.TrimSpace(v) != "" {
				if _, err := normalizeEpayGateway(v); err != nil {
					return paymentSettingErr(key, "gateway", "")
				}
			}
		case KeyEpayTypes:
			if err := validateEpayTypes(v); err != nil {
				return paymentSettingErr(key, "epay_types", "")
			}
		}
	}
	return nil
}

func validateEpayTypes(v string) error {
	if utf8.RuneCountInString(v) > 4000 {
		return errors.New("too long")
	}
	var raw []EpayType
	if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &raw); err != nil {
		return err
	}
	if len(raw) < 1 || len(raw) > 10 {
		return errors.New("count")
	}
	seen := map[string]bool{}
	for _, t := range raw {
		name, typ := strings.TrimSpace(t.Name), strings.TrimSpace(t.Type)
		if name == "" || len([]rune(name)) > 64 || !validEpayTypeCode(typ) || seen[typ] {
			return errors.New("entry")
		}
		seen[typ] = true
	}
	return nil
}

// httpURL parses an absolute http(s) URL without user info.
func httpURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("not an http(s) url")
	}
	if u.User != nil {
		return nil, errors.New("credentials not allowed")
	}
	return u, nil
}

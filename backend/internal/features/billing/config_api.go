package billing

import (
	"context"
	"sort"
	"strings"
)

// ConfigView builds the public billing configuration object. Secrets never
// appear in it.
func (s *Service) ConfigView(ctx context.Context) (map[string]any, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mode":                     rs.Mode,
		"prepaidAmountUSD":         rs.PrepaidAmountUSD,
		"prepaidAmountNanousd":     rs.PrepaidAmountNanousd,
		"nativeToolBillingEnabled": rs.NativeToolBillingEnabled,
		"nativeToolPricing":        nativePricingView(rs.NativeOverrides),
		"paymentProviders":         rs.PaymentProviders,
		"usdToCNYRate":             rs.USDToCNYRate,
		"displayCurrency":          rs.DisplayCurrency,
		"epayTypes":                rs.EpayTypes,
	}, nil
}

// PatchConfigInput is the body of PATCH /admin/billing/config.
type PatchConfigInput struct {
	Mode                     string             `json:"mode" binding:"required,oneof=self period usage"`
	PrepaidAmountUSD         *float64           `json:"prepaidAmountUSD" binding:"omitempty,gte=0"`
	USDToCNYRate             *float64           `json:"usdToCNYRate" binding:"omitempty,gt=0"`
	DisplayCurrency          *string            `json:"displayCurrency" binding:"omitempty,oneof=USD CNY"`
	NativeToolBillingEnabled *bool              `json:"nativeToolBillingEnabled"`
	NativeToolPricing        *[]NativeToolInput `json:"nativeToolPricing"`
}

// LockedSettingKeys lists the billing settings (namespaced) that may not change
// while billingGating is off: the mode and the payment providers.
func LockedSettingKeys(gatingOn bool, current RuntimeSettings, patch map[string]string) []string {
	if gatingOn {
		return nil
	}
	var keys []string
	if v, ok := patch[KeyMode]; ok && normalizeMode(v) != current.Mode {
		keys = append(keys, "billing."+KeyMode)
	}
	if v, ok := patch[KeyPaymentProviders]; ok {
		next, _ := parseProviders(v)
		a, b := append([]string(nil), next...), append([]string(nil), current.PaymentProviders...)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			keys = append(keys, "billing."+KeyPaymentProviders)
		}
	}
	return keys
}

// PatchConfig applies the administrator's configuration patch.
func (s *Service) PatchConfig(ctx context.Context, in PatchConfigInput) (map[string]any, error) {
	values := map[string]string{KeyMode: in.Mode}
	if in.PrepaidAmountUSD != nil {
		values[KeyPrepaidAmountUSD] = formatFloat(*in.PrepaidAmountUSD)
	}
	if in.USDToCNYRate != nil {
		values[KeyUSDToCNYRate] = formatFloat(*in.USDToCNYRate)
	}
	if in.DisplayCurrency != nil {
		values[KeyDisplayCurrency] = *in.DisplayCurrency
	}
	if in.NativeToolBillingEnabled != nil {
		if *in.NativeToolBillingEnabled {
			values[KeyNativeToolBillingEnabled] = "true"
		} else {
			values[KeyNativeToolBillingEnabled] = "false"
		}
	}
	if in.NativeToolPricing != nil {
		ov, err := normalizeNativeInputs(*in.NativeToolPricing)
		if err != nil {
			return nil, ErrInvalidNativeToolPrice.WithCause(err)
		}
		values[KeyNativeToolPricingJSON] = marshalNativeOverrides(ov)
	}
	cur, err := s.loadSettings(ctx)
	if err != nil {
		return nil, ErrSettingsUnavailable.WithCause(err)
	}
	if keys := LockedSettingKeys(s.gatingOn(), cur, values); len(keys) > 0 {
		return nil, featureLocked(keys...)
	}
	if err := ValidateSettingValues(values); err != nil {
		return nil, err
	}
	if err := s.ports.Settings.UpdateBilling(ctx, values); err != nil {
		return nil, err
	}
	return s.ConfigView(ctx)
}

package user

import (
	"context"
	"sort"
)

// Billing modes as the administration views understand them.
const (
	modeSelf   = "self"
	modeUsage  = "usage"
	modePeriod = "period"
)

// AdminUserView is the user object of the administration routes: the common
// view plus the billing account figures.
type AdminUserView struct {
	UserView
	BillingAccountCurrency string  `json:"billingAccountCurrency"`
	BillingBalanceNanousd  int64   `json:"billingBalanceNanousd"`
	BillingBalanceUSD      float64 `json:"billingBalanceUSD"`
	BillingAccountStatus   string  `json:"billingAccountStatus"`
}

// adminViews builds user views in bulk for the administration routes.
type adminViews struct {
	accounts *Accounts
	billing  Billing
}

func (v adminViews) mode(ctx context.Context) string {
	if v.billing == nil {
		return modeSelf
	}
	return v.billing.Mode(ctx)
}

// build derives the views of the given accounts, in the same order.
func (v adminViews) build(ctx context.Context, accts []Account) ([]AdminUserView, error) {
	out := make([]AdminUserView, 0, len(accts))
	if len(accts) == 0 {
		return out, nil
	}
	ids := make([]uint, 0, len(accts))
	for _, a := range accts {
		ids = append(ids, a.ID)
	}
	links, err := v.accounts.IdentitiesForUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	active, err := v.accounts.LatestSessionActivity(ctx, ids)
	if err != nil {
		return nil, err
	}
	twoFactor, err := v.accounts.TwoFactorStates(ctx, ids)
	if err != nil {
		return nil, err
	}
	creds, err := v.accounts.CredentialsForUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	var subs map[uint]SubscriptionInfo
	var billed map[uint]BillingAccountInfo
	if v.billing != nil {
		switch v.billing.Mode(ctx) {
		case modeSelf:
			subs, err = v.billing.Subscriptions(ctx, ids)
		case modeUsage:
			billed, err = v.billing.Accounts(ctx, ids)
		case modePeriod:
			if subs, err = v.billing.Subscriptions(ctx, ids); err == nil {
				billed, err = v.billing.Accounts(ctx, ids)
			}
		}
		if err != nil {
			return nil, err
		}
	}

	for _, a := range accts {
		in := ViewInput{Account: a, TwoFactorAvailable: true}
		if c, ok := creds[a.ID]; ok {
			c := c
			in.Credential = &c
			in.InitialSecurityRequired = c.MustResetPassword || a.OnboardingCompletedAt == nil
		} else {
			in.InitialSecurityRequired = a.OnboardingCompletedAt == nil
		}
		if t, ok := active[a.ID]; ok {
			t := t
			in.LastActiveAt = &t
		}
		if st, ok := twoFactor[a.ID]; ok {
			in.TwoFactorEnabled, in.TwoFactorRecoveryCount = st.Enabled, st.RecoveryCount
		}
		if s, ok := subs[a.ID]; ok {
			s := s
			in.Subscription = &s
		}
		if b, ok := billed[a.ID]; ok {
			b := b
			in.Billing = &b
		}
		seen := map[string]bool{}
		for _, l := range links[a.ID] {
			if seen[l.ProviderPublicID] {
				continue
			}
			seen[l.ProviderPublicID] = true
			in.Providers = append(in.Providers, IdentityProviderSummary{
				ID: l.ProviderPublicID, Type: l.ProviderType, Name: l.ProviderName, Slug: l.ProviderSlug, LogoURL: l.ProviderLogoURL,
			})
		}
		view := BuildView(in)
		out = append(out, AdminUserView{
			UserView:               view,
			BillingAccountCurrency: view.BillingCurrency,
			BillingBalanceNanousd:  view.BillingBalanceNanousd,
			BillingBalanceUSD:      float64(view.BillingBalanceNanousd) / 1e9,
			BillingAccountStatus:   view.BillingStatus,
		})
	}
	return out, nil
}

// one builds the view of a single account.
func (v adminViews) one(ctx context.Context, a Account) (AdminUserView, error) {
	list, err := v.build(ctx, []Account{a})
	if err != nil {
		return AdminUserView{}, err
	}
	return list[0], nil
}

// userLabel is the name an event or log row shows for a user: the display
// name, else the username, else the numeric id.
func userLabel(a Account) string {
	if n := trimmed(a.DisplayName); n != "" {
		return n
	}
	return trimmed(a.Username)
}

func sortedIDs(set map[uint]bool) []uint {
	out := make([]uint, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

package user

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// fakeBilling is a Billing backed by the package's own row mappings.
type fakeBilling struct {
	db   *gorm.DB
	mode string
}

func (f *fakeBilling) Mode(context.Context) string { return f.mode }

func (f *fakeBilling) Subscriptions(ctx context.Context, ids []uint) (map[uint]SubscriptionInfo, error) {
	out := map[uint]SubscriptionInfo{}
	for _, id := range ids {
		var sub subscriptionRow
		if err := f.db.WithContext(ctx).Where("user_id = ? AND status = ?", id, "active").Order("id DESC").Limit(1).Find(&sub).Error; err != nil {
			return nil, err
		}
		if sub.ID == 0 {
			continue
		}
		var plan planRow
		f.db.Where("id = ?", sub.PlanID).Take(&plan)
		info := SubscriptionInfo{Tier: plan.Code, PlanName: plan.Name, Status: "active", ExpiresAt: sub.CurrentPeriodEndAt}
		pid := plan.ID
		info.PlanID = &pid
		if plan.Code == "free" {
			info.Status, info.ExpiresAt = "free", nil
		}
		out[id] = info
	}
	return out, nil
}

func (f *fakeBilling) Accounts(ctx context.Context, ids []uint) (map[uint]BillingAccountInfo, error) {
	out := map[uint]BillingAccountInfo{}
	var rows []billingAccountRow
	if err := f.db.WithContext(ctx).Where("user_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = BillingAccountInfo{Currency: r.Currency, Balance: r.BalanceNanousd, Status: r.Status}
	}
	return out, nil
}

func (f *fakeBilling) AssignPlan(ctx context.Context, userID uint, code string, exp *time.Time) error {
	var plan planRow
	if err := f.db.Where("code = ? AND is_active = ?", code, true).Take(&plan).Error; err != nil {
		return ErrInvalidSubscriptionTier
	}
	if code != "free" {
		if exp == nil {
			return ErrSubscriptionExpiryMissing
		}
		if !exp.After(time.Now().Add(-100 * 365 * 24 * time.Hour)) {
			return ErrInvalidSubscriptionExpiry
		}
	}
	now := time.Now().UTC()
	f.db.Model(&subscriptionRow{}).Where("user_id = ? AND status = ?", userID, "active").Update("status", "expired")
	row := subscriptionRow{UserID: userID, PlanID: plan.ID, Status: "active", StartAt: now, CurrentPeriodStartAt: now}
	if code != "free" {
		row.CurrentPeriodEndAt = exp
	}
	return f.db.Create(&row).Error
}

// adminEnv is a mounted user area with an administrator ready to act.
type adminEnv struct {
	t       *testing.T
	env     *testkit.Env
	billing *fakeBilling
	admin   Account
}

func newAdminEnv(t *testing.T, mode string, extra ...func(*Ports)) *adminEnv {
	t.Helper()
	ae := &adminEnv{t: t}
	ports := Ports{}
	for _, f := range extra {
		f(&ports)
	}
	var billingPort *fakeBilling
	ports.Billing = lazyBilling{get: func() Billing { return billingPort }}
	ae.env = newEnv(t, ports)
	if err := ae.env.DB.AutoMigrate(&mfaRow{}); err != nil {
		t.Fatal(err)
	}
	billingPort = &fakeBilling{db: ae.env.DB, mode: mode}
	ae.billing = billingPort
	ae.admin = seedAccount(t, ae.env.DB, "root-admin", func(a *Account) { a.Role = RoleAdmin })
	return ae
}

// lazyBilling lets the test build the fake after the app (and its database) exists.
type lazyBilling struct{ get func() Billing }

func (l lazyBilling) Mode(ctx context.Context) string { return l.get().Mode(ctx) }
func (l lazyBilling) Subscriptions(ctx context.Context, ids []uint) (map[uint]SubscriptionInfo, error) {
	return l.get().Subscriptions(ctx, ids)
}
func (l lazyBilling) Accounts(ctx context.Context, ids []uint) (map[uint]BillingAccountInfo, error) {
	return l.get().Accounts(ctx, ids)
}
func (l lazyBilling) AssignPlan(ctx context.Context, id uint, code string, exp *time.Time) error {
	return l.get().AssignPlan(ctx, id, code, exp)
}

func (ae *adminEnv) as(a Account) map[string]string {
	return ae.env.Bearer(testkit.Claims{UserID: a.ID, Username: a.Username, Role: a.Role, SessionID: a.ID})
}

func (ae *adminEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	ae.t.Helper()
	return ae.env.Do(method, "/api/v1"+path, ae.as(ae.admin), body)
}

func (ae *adminEnv) doAs(a Account, method, path string, body any) *httptest.ResponseRecorder {
	ae.t.Helper()
	return ae.env.Do(method, "/api/v1"+path, ae.as(a), body)
}

func (ae *adminEnv) expect(w *httptest.ResponseRecorder, status int, code string) {
	ae.t.Helper()
	testkit.Expect(ae.t, w, status, code)
}

func (ae *adminEnv) seed(username string, mod ...func(*Account)) Account {
	return seedAccount(ae.t, ae.env.DB, username, mod...)
}

func (ae *adminEnv) session(userID uint) uint {
	ae.t.Helper()
	now := ae.env.Clock.Now()
	s := sessionRow{SessionID: fmt.Sprintf("s-%d-%d", userID, now.UnixNano()), UserID: userID, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := ae.env.DB.Create(&s).Error; err != nil {
		ae.t.Fatal(err)
	}
	return s.ID
}

func (ae *adminEnv) revoked(sessionID uint) (bool, string) {
	var s sessionRow
	if err := ae.env.DB.Where("id = ?", sessionID).Take(&s).Error; err != nil {
		ae.t.Fatal(err)
	}
	return s.RevokedAt != nil, s.RevokeReason
}

func (ae *adminEnv) reload(id uint) Account {
	ae.t.Helper()
	var a Account
	if err := ae.env.DB.Where("id = ?", id).Take(&a).Error; err != nil {
		ae.t.Fatal(err)
	}
	return a
}

func (ae *adminEnv) audits(action string) []audit.Log {
	var logs []audit.Log
	ae.env.DB.Where("action = ?", action).Order("id ASC").Find(&logs)
	return logs
}

type userDoc struct {
	ID                    uint       `json:"id"`
	PublicID              string     `json:"publicID"`
	Username              string     `json:"username"`
	DisplayName           string     `json:"displayName"`
	Email                 string     `json:"email"`
	Phone                 string     `json:"phone"`
	Role                  string     `json:"role"`
	Status                string     `json:"status"`
	Timezone              string     `json:"timezone"`
	Locale                string     `json:"locale"`
	EmailVerifiedAt       *time.Time `json:"emailVerifiedAt"`
	LastLoginAt           *time.Time `json:"lastLoginAt"`
	LastActiveAt          *time.Time `json:"lastActiveAt"`
	TwoFactorAvailable    bool       `json:"twoFactorAvailable"`
	TwoFactorEnabled      bool       `json:"twoFactorEnabled"`
	TwoFactorRequired     bool       `json:"twoFactorRequired"`
	TwoFactorRecoveryCnt  int        `json:"twoFactorRecoveryCount"`
	SubscriptionTier      string     `json:"subscriptionTier"`
	SubscriptionPlanID    *uint      `json:"subscriptionPlanID"`
	SubscriptionPlanName  string     `json:"subscriptionPlanName"`
	SubscriptionStatus    string     `json:"subscriptionStatus"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt"`
	BillingCurrency       string     `json:"billingAccountCurrency"`
	BillingNano           int64      `json:"billingBalanceNanousd"`
	BillingUSD            float64    `json:"billingBalanceUSD"`
	BillingStatus         string     `json:"billingAccountStatus"`
	Providers             []struct {
		ID, Type, Name, Slug, LogoURL string
	} `json:"identityProviders"`
}

func (ae *adminEnv) userFrom(w *httptest.ResponseRecorder) userDoc {
	ae.t.Helper()
	testkit.Expect(ae.t, w, 200, "")
	var out struct {
		User userDoc `json:"user"`
	}
	testkit.Decode(ae.t, w, &out)
	return out.User
}

func (ae *adminEnv) page(w *httptest.ResponseRecorder) (int64, []userDoc) {
	ae.t.Helper()
	testkit.Expect(ae.t, w, 200, "")
	var out struct {
		Total   int64     `json:"total"`
		Results []userDoc `json:"results"`
	}
	testkit.Decode(ae.t, w, &out)
	return out.Total, out.Results
}

func names(list []userDoc) string {
	var n []string
	for _, u := range list {
		n = append(n, u.Username)
	}
	return strings.Join(n, ",")
}

var _ = apperr.ErrInvalidRequest

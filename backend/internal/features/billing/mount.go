// Package billing implements metering of model usage, the three billing modes,
// subscription plans, prepaid balance, redemption codes, online payments and
// model price administration.
package billing

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Mount registers the area with default (standalone) ports. The integrator
// normally uses MountWith to supply adapters to the other areas.
func Mount(d deps.Deps) error { return MountWith(Ports{}, nil)(d) }

// MountWith returns a mount function wired to the given ports. publish, when
// non-nil, receives the Service so other areas can call its exported methods.
func MountWith(p Ports, publish func(*Service)) func(deps.Deps) error {
	return func(d deps.Deps) error {
		svc := NewService(d, p)
		if publish != nil {
			publish(svc)
		}
		svc.routes(d)
		d.Lifecycle.OnStart("billing-bind-default-group", func(ctx context.Context) error {
			if err := svc.bindDefaultGroup(ctx); err != nil {
				d.Logger.Warn("billing_bind_default_group_failed", logx.F("error", err.Error()))
			}
			return nil
		})
		return nil
	}
}

func pathUint(c *gin.Context, name string, errOnBad error) (uint, bool) {
	n, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || n == 0 {
		httpx.Fail(c, http.StatusBadRequest, errOnBad)
		return 0, false
	}
	return uint(n), true
}

func queryUint(c *gin.Context, name string) (uint, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		httpx.InvalidQuery(c, name)
		return 0, false
	}
	return uint(n), true
}

func queryTime(c *gin.Context, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		httpx.InvalidQuery(c, name)
		return nil, false
	}
	return &t, true
}

func fail(c *gin.Context, err error) { httpx.FailErr(c, err) }

func (s *Service) routes(d deps.Deps) {
	auth, admin := d.Routes.Auth, d.Routes.Admin
	gated := d.Gated(capability.BillingGating)

	// ---- authenticated, not gated ----
	configHandler := func(c *gin.Context) {
		cfg, err := s.ConfigView(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"config": cfg})
	}
	auth.GET("/billing/config", configHandler)
	admin.GET("/admin/billing/config", configHandler)

	auth.GET("/billing/account", func(c *gin.Context) {
		acc, err := s.GetAccount(c.Request.Context(), middleware.MustPrincipal(c).UserID)
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"account": acc})
	})
	auth.GET("/billing/overview", func(c *gin.Context) {
		ov, err := s.Overview(c.Request.Context(), middleware.MustPrincipal(c).UserID)
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"overview": ov})
	})
	auth.GET("/billing/usage", func(c *gin.Context) {
		pg := pagination.FromRequest(c.Request)
		items, total, err := s.ListUsage(c.Request.Context(), middleware.MustPrincipal(c).UserID,
			UsageQuery{Query: c.Query("query"), Status: c.Query("status"), Sort: c.Query("sort")}, pg.Offset(), pg.Limit())
		if err != nil {
			fail(c, err)
			return
		}
		httpx.Paged(c, total, items)
	})
	auth.GET("/billing/usage/monthly", func(c *gin.Context) {
		months, _ := strconv.Atoi(strings.TrimSpace(c.Query("months")))
		out, err := s.MonthlyUsage(c.Request.Context(), middleware.MustPrincipal(c).UserID, months)
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, out)
	})
	auth.GET("/billing/usage/daily", func(c *gin.Context) {
		_, hasDays := c.GetQuery("days")
		out, err := s.DailyUsage(c.Request.Context(), middleware.MustPrincipal(c).UserID, DailyQuery{
			StartDate: c.Query("start_date"), EndDate: c.Query("end_date"), Days: c.Query("days"), HasDays: hasDays})
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, out)
	})

	// ---- authenticated, gated ----
	plansHandler := func(c *gin.Context) {
		plans, err := s.ListPlans(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, plans)
	}
	gated.Auth.GET("/billing/plans", plansHandler)
	gated.Admin.GET("/admin/billing/plans", plansHandler)

	gated.Auth.POST("/billing/subscriptions", func(c *gin.Context) {
		var req struct {
			PriceID uint `json:"priceID" binding:"required,min=1"`
			Cycles  int  `json:"cycles" binding:"omitempty,min=1,max=120"`
		}
		if !httpx.BindJSON(c, &req) {
			return
		}
		uid := middleware.MustPrincipal(c).UserID
		sub, err := s.SubscribeFree(c.Request.Context(), uid, req.PriceID, req.Cycles)
		if err != nil {
			fail(c, err)
			return
		}
		s.audit(c, uid, "subscribe", "billing_subscription", strconv.FormatUint(uint64(sub.ID), 10),
			map[string]any{"planID": sub.PlanID, "priceID": sub.PriceID})
		httpx.OK(c, gin.H{"subscription": subscriptionView(*sub)})
	})
	gated.Auth.POST("/billing/payments/checkout", func(c *gin.Context) {
		var req CheckoutRequest
		if !httpx.BindJSON(c, &req) {
			return
		}
		uid := middleware.MustPrincipal(c).UserID
		view, err := s.Checkout(c.Request.Context(), uid, req, requestInfoOf(c.Request), func(o PaymentOrder) {
			s.audit(c, uid, "billing.payment.checkout", "billing_payment_order", o.OrderNo, map[string]any{
				"provider": o.Provider, "orderType": o.OrderType, "planID": o.PlanID, "priceID": o.PriceID,
				"baseAmountCents": o.BaseAmountCents, "baseCurrency": o.BaseCurrency, "payAmountCents": o.PayAmountCents,
				"payCurrency": o.PayCurrency, "fxRate": o.FxRate})
		})
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"checkout": view})
	})
	gated.Auth.POST("/billing/redemptions", func(c *gin.Context) {
		var req struct {
			Code string `json:"code" binding:"required,min=3,max=64"`
		}
		if !httpx.BindJSON(c, &req) {
			return
		}
		res, err := s.Redeem(c.Request.Context(), middleware.MustPrincipal(c).UserID, req.Code)
		if err != nil {
			fail(c, err)
			return
		}
		httpx.OK(c, res)
	})

	// ---- public payment callbacks (gated) ----
	gated.Public.POST("/billing/payments/stripe/webhook", func(c *gin.Context) {
		if err := s.StripeWebhookReady(c.Request.Context()); err != nil {
			fail(c, err)
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, stripeWebhookMaxBody+1))
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrInvalidWebhookBody)
			return
		}
		if len(body) > stripeWebhookMaxBody {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrWebhookBodyTooLarge)
			return
		}
		out, err := s.HandleStripeWebhook(c.Request.Context(), body, c.GetHeader("Stripe-Signature"))
		if err != nil {
			fail(c, err)
			return
		}
		if out.Ignored {
			httpx.OK(c, gin.H{"ignored": true})
			return
		}
		s.audit(c, out.Order.UserID, "billing.payment.completed", "billing_payment_order", out.Order.OrderNo,
			map[string]any{"source": "stripe.webhook", "activated": out.Delivered})
		httpx.OK(c, gin.H{"ok": true})
	})
	epayNotify := func(c *gin.Context) {
		_ = c.Request.ParseForm()
		params := map[string]string{}
		for k, v := range c.Request.URL.Query() {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
		for k, v := range c.Request.PostForm {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
		out, err := s.HandleEpayNotify(c.Request.Context(), params)
		if err != nil {
			c.String(http.StatusBadRequest, "fail")
			return
		}
		if !out.Ignored {
			s.audit(c, out.Order.UserID, "billing.payment.completed", "billing_payment_order", out.Order.OrderNo,
				map[string]any{"source": "epay.notify", "activated": out.Delivered})
		}
		c.String(http.StatusOK, "success")
	}
	gated.Public.GET("/billing/payments/epay/notify", epayNotify)
	gated.Public.POST("/billing/payments/epay/notify", epayNotify)

	s.adminRoutes(admin, gated)
}

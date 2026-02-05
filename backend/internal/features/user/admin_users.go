package user

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// adminAPI serves the /admin/users routes.
type adminAPI struct {
	accounts *Accounts
	billing  Billing
	audit    Auditor
	views    adminViews
	clock    clock.Clock
	cfg      *config.Holder
}

const (
	resourceUser = "user"

	reasonPasswordReset = "admin reset password"
	reasonTwoFactor     = "admin reset 2fa"
	reasonRevokeAll     = "admin revoke all sessions"
)

// mountAdmin registers the account administration routes. They exist only
// while the multi-user capability is on.
func (a *adminAPI) mount(g gin.IRoutes) {
	g.GET("/admin/users", a.list)
	g.POST("/admin/users", a.create)
	g.POST("/admin/users/import/openwebui", a.importOpenWebUI)
	g.PATCH("/admin/users/:id", a.patch)
	g.DELETE("/admin/users/:id", a.remove)
	g.PATCH("/admin/users/:id/status", a.setStatus)
	g.POST("/admin/users/:id/reset-password", a.resetPassword)
	g.POST("/admin/users/:id/reset-2fa", a.resetTwoFactor)
	g.POST("/admin/users/:id/revoke-sessions", a.revokeSessions)
	g.GET("/admin/user-auth-events", a.authEvents)
}

func mountAdmin(d adminDeps) {
	api := &adminAPI{
		accounts: d.accounts, billing: d.billing, audit: d.audit,
		views: adminViews{accounts: d.accounts, billing: d.billing}, clock: d.clock, cfg: d.cfg,
	}
	api.mount(d.routes.Admin)
}

// ---- shared helpers ------------------------------------------------------------

func fail(c *gin.Context, err error) { httpx.FailErr(c, err) }

// userID parses the {id} path segment: a base-10 positive integer.
func userID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || uint64(uint(n)) != n {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
		return 0, false
	}
	return uint(n), true
}

func idText(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func isAdminRole(role string) bool { return role == RoleAdmin || role == RoleSuperAdmin }

// target loads the account an administrator acts on.
func (a *adminAPI) target(c *gin.Context, id uint) (*Account, bool) {
	t, err := a.accounts.GetByID(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return nil, false
	}
	return t, true
}

// actor re-reads the acting user and requires a stored admin role. A missing
// row answers like a missing target (404).
func (a *adminAPI) actor(c *gin.Context) (*Account, bool) {
	p := middleware.MustPrincipal(c)
	acting, err := a.accounts.GetByID(c.Request.Context(), p.UserID)
	if err != nil {
		fail(c, err)
		return nil, false
	}
	if !isAdminRole(acting.Role) {
		httpx.Fail(c, http.StatusForbidden, ErrAdminOnly)
		return nil, false
	}
	return acting, true
}

// guard refuses a plain administrator acting on a superadmin.
func guard(c *gin.Context, actor, target *Account) bool {
	if actor.Role != RoleSuperAdmin && target.Role == RoleSuperAdmin {
		httpx.Fail(c, http.StatusForbidden, ErrSuperadminManagement)
		return false
	}
	return true
}

// load resolves target and actor and applies the superadmin protection, in the
// order the routes document.
func (a *adminAPI) load(c *gin.Context, id uint) (acting, target *Account, ok bool) {
	if target, ok = a.target(c, id); !ok {
		return nil, nil, false
	}
	if acting, ok = a.actor(c); !ok {
		return nil, nil, false
	}
	if !guard(c, acting, target) {
		return nil, nil, false
	}
	return acting, target, true
}

func (a *adminAPI) record(c *gin.Context, action string, target uint, detail any) {
	a.recordResource(c, action, idText(target), detail)
}

func (a *adminAPI) recordResource(c *gin.Context, action, resourceID string, detail any) {
	if a.audit != nil {
		a.audit.RecordRequest(c, action, resourceUser, resourceID, detail)
	}
}

func (a *adminAPI) respondUser(c *gin.Context, id uint) {
	fresh, err := a.accounts.GetByID(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	view, err := a.views.one(c.Request.Context(), *fresh)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"user": view})
}

// badRequestIfUntyped keeps typed errors from other areas but gives them a 400
// when they carry no status of their own.
func billingFailure(err error) error {
	if te, ok := apperr.Find(err); ok && te.Status == 0 {
		return te.WithStatus(http.StatusBadRequest)
	}
	return err
}

// ---- list ------------------------------------------------------------------------------

func (a *adminAPI) list(c *gin.Context) {
	q := c.Request.URL.Query()
	f := ListFilter{
		Query:              strings.TrimSpace(q.Get("q")),
		ProviderSlug:       strings.TrimSpace(q.Get("identity_provider")),
		SubscriptionStatus: strings.TrimSpace(q.Get("subscription_status")),
	}
	rows, total, err := a.accounts.List(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	views, err := a.views.build(c.Request.Context(), rows)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, views)
}

// ---- create ----------------------------------------------------------------------------

type createBody struct {
	Username              string     `json:"username" binding:"required,min=3,max=16"`
	Password              string     `json:"password" binding:"required,min=8,max=128"`
	AvatarURL             string     `json:"avatarURL" binding:"max=2048"`
	DisplayName           string     `json:"displayName" binding:"omitempty,min=3,max=16"`
	Email                 string     `json:"email" binding:"omitempty,email,max=128"`
	Phone                 string     `json:"phone" binding:"max=32"`
	Timezone              string     `json:"timezone" binding:"max=64"`
	Locale                string     `json:"locale" binding:"max=16"`
	SubscriptionTier      string     `json:"subscriptionTier" binding:"max=32"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt"`
}

func (a *adminAPI) create(c *gin.Context) {
	var req createBody
	if !httpx.BindJSONPlain(c, &req) {
		return
	}
	if _, ok := a.actor(c); !ok {
		return
	}
	ctx := c.Request.Context()
	acct, err := a.accounts.Create(ctx, CreateInput{
		Username: req.Username, Password: req.Password, AvatarURL: req.AvatarURL, DisplayName: req.DisplayName,
		Email: req.Email, Phone: req.Phone, Timezone: req.Timezone, Locale: req.Locale,
		BillingMode: a.views.mode(ctx), SubscriptionTier: req.SubscriptionTier, SubscriptionExpiresAt: req.SubscriptionExpiresAt,
	})
	if err != nil {
		fail(c, err)
		return
	}
	a.record(c, "admin_create_user", acct.ID, gin.H{"username": acct.Username})
	view, err := a.views.one(ctx, *acct)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"user": view})
}

// ---- status ----------------------------------------------------------------------------

type statusBody struct {
	Status string `json:"status" binding:"required,max=32"`
	Reason string `json:"reason" binding:"max=255"`
}

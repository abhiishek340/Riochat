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

// settableStatus is one of the four statuses an administrator may set.
func settableStatus(s string) bool {
	switch s {
	case StatusActive, StatusLocked, StatusSuspended, StatusDeactivated:
		return true
	}
	return false
}

func (a *adminAPI) setStatus(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	var req statusBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	status := strings.TrimSpace(req.Status)
	if !settableStatus(status) {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidStatus)
		return
	}
	_, target, ok := a.load(c, id)
	if !ok {
		return
	}
	if target.Role == RoleSuperAdmin {
		httpx.Fail(c, http.StatusConflict, ErrSuperadminStatus)
		return
	}
	from := target.Status
	if err := a.accounts.ApplyStatus(c.Request.Context(), id, status); err != nil {
		fail(c, err)
		return
	}
	a.record(c, "admin_update_user_status", id, gin.H{"from_status": from, "to_status": status, "reason": req.Reason})
	a.respondUser(c, id)
}

// ---- credentials and sessions ---------------------------------------------------------

type resetPasswordBody struct {
	NewPassword       string `json:"newPassword" binding:"required,min=8,max=128"`
	MustResetPassword *bool  `json:"mustResetPassword"`
}

func (a *adminAPI) resetPassword(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	var req resetPasswordBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	acting, target, ok := a.load(c, id)
	if !ok {
		return
	}
	if target.Role == RoleSuperAdmin {
		httpx.Fail(c, http.StatusConflict, ErrSuperadminPassword)
		return
	}
	must := req.MustResetPassword == nil || *req.MustResetPassword
	ctx := c.Request.Context()
	if err := a.accounts.ResetPassword(ctx, id, req.NewPassword, must); err != nil {
		fail(c, err)
		return
	}
	if err := a.accounts.RevokeAllSessions(ctx, id, reasonPasswordReset); err != nil {
		fail(c, err)
		return
	}
	state := reqctx.FromRequest(c.Request)
	// The trace event is best effort: a failure to store it never fails the reset.
	_ = a.accounts.RecordAuthEvent(ctx, AuthEvent{
		RequestID: state.RequestID(), UserID: id, EventType: "password_reset", Result: "success",
		Reason: "admin_reset_password", ClientIP: state.ClientIP(), UserAgent: c.Request.UserAgent(),
		DetailJSON: `{"actor_user_id":"` + idText(acting.ID) + `","must_reset_password":"` + strconv.FormatBool(must) + `"}`,
	})
	a.record(c, "admin_reset_user_password", id, gin.H{"must_reset_password": strconv.FormatBool(must)})
	httpx.OK(c, gin.H{"reset": true})
}

func (a *adminAPI) resetTwoFactor(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	_, target, ok := a.load(c, id)
	if !ok {
		return
	}
	if target.Role == RoleSuperAdmin {
		httpx.Fail(c, http.StatusConflict, ErrSuperadminTwoFactor)
		return
	}
	ctx := c.Request.Context()
	available, err := a.accounts.ResetTwoFactor(ctx, id)
	if err != nil {
		fail(c, err)
		return
	}
	if !available {
		httpx.OK(c, gin.H{"reset": true})
		return
	}
	if err := a.accounts.RevokeAllSessions(ctx, id, reasonTwoFactor); err != nil {
		fail(c, err)
		return
	}
	a.record(c, "admin_reset_user_2fa", id, gin.H{"target_user_id": idText(id)})
	httpx.OK(c, gin.H{"reset": true})
}

func (a *adminAPI) revokeSessions(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	if _, _, ok := a.load(c, id); !ok {
		return
	}
	if err := a.accounts.RevokeAllSessions(c.Request.Context(), id, reasonRevokeAll); err != nil {
		fail(c, err)
		return
	}
	a.record(c, "admin_revoke_user_sessions", id, gin.H{"target_user_id": idText(id)})
	httpx.OK(c, gin.H{"revoked": true})
}

// ---- delete ----------------------------------------------------------------------------

func (a *adminAPI) remove(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	acting, target, ok := a.load(c, id)
	if !ok {
		return
	}
	if acting.ID == target.ID {
		httpx.Fail(c, http.StatusConflict, ErrSelfDelete)
		return
	}
	if target.Role == RoleSuperAdmin {
		httpx.Fail(c, http.StatusConflict, ErrSuperadminDeleteConf)
		return
	}
	if err := a.accounts.HardDelete(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	a.record(c, "admin_delete_user", id, gin.H{"target_user_id": idText(id), "username": target.Username, "public_id": target.PublicID})
	httpx.OK(c, gin.H{"deleted": true})
}

// ---- authentication events -----------------------------------------------------------

// AuthEventView is one authentication event with the current name of its user.
type AuthEventView struct {
	ID              uint      `json:"id"`
	RequestID       string    `json:"requestID"`
	UserID          uint      `json:"userID"`
	Username        string    `json:"username"`
	UserDisplayName string    `json:"userDisplayName"`
	UserLabel       string    `json:"userLabel"`
	EventType       string    `json:"eventType"`
	Result          string    `json:"result"`
	Reason          string    `json:"reason"`
	ClientIP        string    `json:"clientIP"`
	UserAgent       string    `json:"userAgent"`
	DetailJSON      string    `json:"detailJSON"`
	OccurredAt      time.Time `json:"occurredAt"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (a *adminAPI) authEvents(c *gin.Context) {
	q := c.Request.URL.Query()
	f := AuthEventFilter{EventType: q.Get("event_type"), Result: q.Get("result")}
	if raw := strings.TrimSpace(q.Get("user_id")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || n == 0 || uint64(uint(n)) != n {
			httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
			return
		}
		f.UserID = uint(n)
	}
	ctx := c.Request.Context()
	rows, total, err := a.accounts.ListAuthEvents(ctx, f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	idSet := map[uint]bool{}
	for _, r := range rows {
		if r.UserID != 0 {
			idSet[r.UserID] = true
		}
	}
	people, err := a.accounts.ByIDs(ctx, sortedIDs(idSet))
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]AuthEventView, 0, len(rows))
	for _, r := range rows {
		v := AuthEventView{
			ID: r.ID, RequestID: r.RequestID, UserID: r.UserID, EventType: r.EventType, Result: r.Result,
			Reason: r.Reason, ClientIP: r.ClientIP, UserAgent: r.UserAgent, DetailJSON: r.DetailJSON,
			OccurredAt: r.OccurredAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if p, ok := people[r.UserID]; ok {
			v.Username, v.UserDisplayName = p.Username, p.DisplayName
			v.UserLabel = userLabel(p)
			if v.UserLabel == "" {
				v.UserLabel = idText(p.ID)
			}
		}
		out = append(out, v)
	}
	httpx.Paged(c, total, out)
}

// adminDeps are the collaborators of the administration routes.
type adminDeps struct {
	routes   deps.Routes
	accounts *Accounts
	billing  Billing
	audit    Auditor
	clock    clock.Clock
	cfg      *config.Holder
}

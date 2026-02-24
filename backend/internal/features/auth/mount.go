package auth

import (
	"context"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

// Mount registers the area with no external collaborators.
func Mount(d deps.Deps) error { return MountWith(d, Hooks{}) }

// MountWith registers the area and connects the given collaborators. The
// integrator calls it from a registry adapter when settings, billing, file and
// audit areas exist.
func MountWith(d deps.Deps, hooks Hooks) error {
	svc := newService(d, hooks)
	h := &handlers{svc: svc}

	d.Sessions.Set(middleware.SessionValidatorFunc(svc.ValidateSession))

	pub, authd := d.Routes.Public, d.Routes.Auth
	reg := d.Gated(capability.Registration)
	sec := d.Gated(capability.AccountSecurity)
	idp := d.Gated(capability.IdentityProviders)

	// Sign-in and tokens.
	pub.GET("/auth/login-options", h.loginOptions)
	pub.POST("/auth/login", h.login)
	pub.POST("/auth/2fa/email/start", h.twoFactorEmailStart)
	pub.POST("/auth/2fa/verify", h.twoFactorVerify)
	pub.POST("/auth/refresh", h.refresh)
	authd.POST("/auth/logout", h.logout)
	sec.Auth.POST("/auth/logout-all", h.logoutAll)
	sec.Auth.GET("/auth/sessions", h.listSessions)
	sec.Auth.PUT("/auth/sessions/current/location", h.updateLocation)
	sec.Auth.POST("/auth/sessions/:session_id/logout", h.logoutSession)

	// Registration and password recovery.
	reg.Public.POST("/auth/register/email/start", h.registerStart)
	reg.Public.POST("/auth/register/email/complete", h.registerComplete)
	sec.Public.POST("/auth/password/reset/start", h.resetStart)
	sec.Public.POST("/auth/password/reset/complete", h.resetComplete)
	sec.Auth.POST("/auth/password/change/start", h.passwordChangeStart)
	sec.Auth.POST("/auth/password/change/complete", h.passwordChangeComplete)

	// Profile and account.
	authd.GET("/me", h.me)
	authd.PATCH("/me", h.patchMe)
	authd.PATCH("/me/username", h.patchUsername)
	authd.POST("/me/onboarding/complete", h.onboardingComplete)
	sec.Auth.POST("/me/delete/start", h.deleteStart)
	sec.Auth.DELETE("/me", h.deleteAccount)

	// Email lifecycle.
	sec.Auth.POST("/me/email/bootstrap/start", h.emailBootstrapStart)
	sec.Auth.POST("/me/email/bootstrap/complete", h.emailBootstrapComplete)
	sec.Auth.POST("/me/email/verify-current/start", h.emailVerifyCurrentStart)
	sec.Auth.POST("/me/email/verify-current/complete", h.emailVerifyCurrentComplete)
	sec.Auth.POST("/me/email/change/start-current", h.emailChangeStartCurrent)
	sec.Auth.POST("/me/email/change/start-new", h.emailChangeStartNew)
	sec.Auth.POST("/me/email/change/complete", h.emailChangeComplete)

	// Two-factor authentication.
	sec.Auth.GET("/me/2fa", h.mfaStatus)
	sec.Auth.POST("/me/2fa/setup/start", h.mfaSetupStart)
	sec.Auth.POST("/me/2fa/setup/confirm", h.mfaSetupConfirm)
	sec.Auth.DELETE("/me/2fa/setup", h.mfaSetupCancel)
	sec.Auth.POST("/me/2fa/recovery/regenerate", h.mfaRegenerate)
	sec.Auth.POST("/me/2fa/disable", h.mfaDisable)

	// Third-party identities.
	idp.Public.POST("/auth/providers/:slug/authorize", h.providerAuthorize)
	idp.Public.GET("/auth/providers/:slug/callback", h.providerCallback)
	idp.Public.POST("/auth/providers/:slug/exchange", h.providerExchange)
	idp.Auth.GET("/me/identities", h.listIdentities)
	idp.Auth.POST("/me/identities/providers/:slug/authorize", h.bindAuthorize)
	idp.Auth.POST("/me/identities/providers/:slug/exchange", h.bindExchange)
	idp.Auth.DELETE("/me/identities/:identity_id", h.deleteIdentity)

	// Provider administration.
	idpAdmin := idp.Admin
	idpAdmin.GET("/admin/auth/providers", h.adminListProviders)
	idpAdmin.POST("/admin/auth/providers", h.adminCreateProvider)
	idpAdmin.PATCH("/admin/auth/providers/:provider_id", h.adminUpdateProvider)
	idpAdmin.PATCH("/admin/auth/provider-order", h.adminReorderProviders)
	idpAdmin.DELETE("/admin/auth/providers/:provider_id", h.adminDeleteProvider)

	// Desktop sidecar.
	if d.Config.Load().IsLocal() {
		pub.POST("/auth/local/exchange", h.localExchange)
		d.LocalGrant.Set(svc.IssueLocalGrant)
		d.Lifecycle.OnStart("auth-local-owner", func(ctx context.Context) error {
			_, err := svc.EnsureLocalOwner(ctx)
			return err
		})
	} else {
		d.Lifecycle.OnStart("auth-bootstrap-admin", svc.BootstrapAdmin)
	}
	return nil
}

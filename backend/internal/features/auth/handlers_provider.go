package auth

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

type authorizeReq struct {
	ClientID      string `json:"clientID" binding:"required,max=128"`
	RedirectURI   string `json:"redirectURI" binding:"required,max=2048"`
	CodeChallenge string `json:"codeChallenge" binding:"required,min=43,max=128"`
	ClientState   string `json:"clientState" binding:"required,min=43,max=128"`
	Intent        string `json:"intent" binding:"omitempty,oneof=login register"`
	Next          string `json:"next" binding:"max=2048"`
}

type exchangeReq struct {
	ClientID     string `json:"clientID" binding:"required,max=128"`
	Grant        string `json:"grant" binding:"required,min=43,max=128"`
	CodeVerifier string `json:"codeVerifier" binding:"required,min=43,max=128"`
}

type providerReq struct {
	Type                string `json:"type" binding:"required,oneof=oidc oauth2"`
	Name                string `json:"name" binding:"required,max=80"`
	Slug                string `json:"slug" binding:"max=64"`
	LogoURL             string `json:"logoURL" binding:"max=512"`
	LoginEnabled        *bool  `json:"loginEnabled"`
	RegistrationEnabled *bool  `json:"registrationEnabled"`
	ClientID            string `json:"clientID" binding:"required,max=255"`
	ClientSecret        string `json:"clientSecret" binding:"max=4096"`
	IssuerURL           string `json:"issuerURL" binding:"max=512"`
	DiscoveryURL        string `json:"discoveryURL" binding:"max=512"`
	AuthURL             string `json:"authURL" binding:"max=512"`
	TokenURL            string `json:"tokenURL" binding:"max=512"`
	UserInfoURL         string `json:"userinfoURL" binding:"max=512"`
	JWKSURL             string `json:"jwksURL" binding:"max=512"`
	Scopes              string `json:"scopes" binding:"max=255"`
	DefaultRole         string `json:"defaultRole" binding:"omitempty,oneof=user admin superadmin"`
	SubjectField        string `json:"subjectField" binding:"max=64"`
	EmailField          string `json:"emailField" binding:"max=64"`
	EmailVerifiedField  string `json:"emailVerifiedField" binding:"max=64"`
	NameField           string `json:"nameField" binding:"max=64"`
	AvatarField         string `json:"avatarField" binding:"max=64"`
}

func (r providerReq) input() providerInput {
	return providerInput{
		Type: r.Type, Name: r.Name, Slug: r.Slug, LogoURL: r.LogoURL, LoginEnabled: r.LoginEnabled,
		RegistrationEnabled: r.RegistrationEnabled, ClientID: r.ClientID, ClientSecret: r.ClientSecret,
		IssuerURL: r.IssuerURL, DiscoveryURL: r.DiscoveryURL, AuthURL: r.AuthURL, TokenURL: r.TokenURL,
		UserInfoURL: r.UserInfoURL, JWKSURL: r.JWKSURL, Scopes: r.Scopes, DefaultRole: r.DefaultRole,
		SubjectField: r.SubjectField, EmailField: r.EmailField, EmailVerifiedField: r.EmailVerifiedField,
		NameField: r.NameField, AvatarField: r.AvatarField,
	}
}

type providerOrderReq struct {
	ProviderIDs []string `json:"providerIDs" binding:"required,dive,required,max=64"`
}

func noStore(c *gin.Context) { c.Header("Cache-Control", "no-store") }

func (h *handlers) providerAuthorize(c *gin.Context) {
	noStore(c)
	var req authorizeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.ProviderAuthorize(reqCtx(c), c.Param("slug"), authorizeInput{
		ClientID: req.ClientID, RedirectURI: req.RedirectURI, CodeChallenge: req.CodeChallenge,
		ClientState: req.ClientState, Intent: req.Intent, Next: req.Next,
	}, 0)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) bindAuthorize(c *gin.Context) {
	noStore(c)
	var req authorizeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	p := middleware.MustPrincipal(c)
	out, err := h.svc.ProviderAuthorize(reqCtx(c), c.Param("slug"), authorizeInput{
		ClientID: req.ClientID, RedirectURI: req.RedirectURI, CodeChallenge: req.CodeChallenge,
		ClientState: req.ClientState, Next: req.Next,
	}, p.UserID)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) providerCallback(c *gin.Context) {
	noStore(c)
	target, err := h.svc.ProviderCallback(reqCtx(c), c.Param("slug"),
		c.Query("code"), c.Query("state"), c.Query("error"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Redirect(http.StatusFound, target)
}

func (h *handlers) providerExchange(c *gin.Context) {
	noStore(c)
	var req exchangeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.ProviderExchange(reqCtx(c), c.Param("slug"), exchangeInput{
		ClientID: req.ClientID, Grant: req.Grant, CodeVerifier: req.CodeVerifier,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.deliver(c, out)
}

func (h *handlers) bindExchange(c *gin.Context) {
	noStore(c)
	var req exchangeReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	p := middleware.MustPrincipal(c)
	out, err := h.svc.BindExchange(reqCtx(c), p.UserID, c.Param("slug"), exchangeInput{
		ClientID: req.ClientID, Grant: req.Grant, CodeVerifier: req.CodeVerifier,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"identity": out})
}

func (h *handlers) listIdentities(c *gin.Context) {
	rows, err := h.svc.ListIdentities(reqCtx(c), middleware.MustPrincipal(c).UserID)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"results": rows})
}

func (h *handlers) deleteIdentity(c *gin.Context) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("identity_id")), 10, 64)
	if err != nil || id == 0 {
		failStatus(c, http.StatusBadRequest, ErrIdentityInvalidID)
		return
	}
	if err := h.svc.UnbindIdentity(reqCtx(c), middleware.MustPrincipal(c).UserID, uint(id)); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

// ---- administration ---------------------------------------------------------------------

func (h *handlers) adminListProviders(c *gin.Context) {
	rows, err := h.svc.ListProviders(reqCtx(c))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, int64(len(rows)), rows)
}

func (h *handlers) adminCreateProvider(c *gin.Context) {
	var req providerReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.CreateProvider(reqCtx(c), middleware.MustPrincipal(c).Role, req.input())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) adminUpdateProvider(c *gin.Context) {
	var req providerReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.UpdateProvider(reqCtx(c), middleware.MustPrincipal(c).Role, c.Param("provider_id"), req.input())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) adminReorderProviders(c *gin.Context) {
	var req providerOrderReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := h.svc.ReorderProviders(reqCtx(c), req.ProviderIDs); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"updated": true})
}

func (h *handlers) adminDeleteProvider(c *gin.Context) {
	force := strings.EqualFold(strings.TrimSpace(c.Query("force")), "true")
	if err := h.svc.DeleteProvider(reqCtx(c), c.Param("provider_id"), force); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

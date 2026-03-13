package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const (
	apiBase  = "https://api.example.com"
	webRedir = "http://localhost:3000/auth/callback?provider=acme"
)

// upstream is a stand-in identity provider.
type upstream struct {
	*httptest.Server
	mu          sync.Mutex
	profile     map[string]any
	emails      []map[string]any
	tokenStatus int
	tokenForms  []url.Values
	userAuth    []string
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{profile: map[string]any{"sub": "subject-1", "email": "alice@example.com", "email_verified": true, "name": "Alice A", "picture": "https://img.example/a.png"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		_ = r.ParseForm()
		u.tokenForms = append(u.tokenForms, r.PostForm)
		if u.tokenStatus != 0 {
			w.WriteHeader(u.tokenStatus)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-123","token_type":"bearer"}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.userAuth = append(u.userAuth, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(u.profile)
	})
	mux.HandleFunc("/userinfo/emails", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		_ = json.NewEncoder(w).Encode(u.emails)
	})
	u.Server = httptest.NewServer(mux)
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) setProfile(p map[string]any) {
	u.mu.Lock()
	u.profile = p
	u.mu.Unlock()
}

type providerEnv struct {
	*testEnv
	up    *upstream
	admin string // bearer of a superadmin
}

func newProviderEnv(t *testing.T) *providerEnv {
	up := newUpstream(t)
	e := newEnv(t,
		withCfg(func(c *configT) {
			c.Server.PublicAPIBaseURL = apiBase
			c.Server.CORSAllowOrigin = "https://app.example.com,*"
		}),
		withHooks(func(h *Hooks) { h.HTTPClient = up.Client() }))
	_, admin := e.session(userSpec{Username: "root-admin", Role: RoleSuperAdmin, Onboarded: true})
	return &providerEnv{testEnv: e, up: up, admin: admin}
}

func (p *providerEnv) body(over map[string]any) map[string]any {
	b := map[string]any{
		"type": "oauth2", "name": "Acme", "slug": "acme", "clientID": "cid", "clientSecret": "s3cret",
		"authURL": p.up.URL + "/auth", "tokenURL": p.up.URL + "/token", "userinfoURL": p.up.URL + "/userinfo",
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (p *providerEnv) create(over map[string]any) map[string]any {
	p.t.Helper()
	w := p.request("POST", "/admin/auth/providers", p.admin, p.body(over))
	testkit.Expect(p.t, w, 200, "")
	return dataMap(p.t, w)
}

// ---- administration -------------------------------------------------------------------------------

func TestProviderAdminCreateAndList(t *testing.T) {
	p := newProviderEnv(t)
	got := p.create(nil)
	if got["slug"] != "acme" || got["clientID"] != "cid" || got["scopes"] != "profile email" || got["defaultRole"] != "user" ||
		got["subjectField"] != "sub" || got["emailField"] != "email" || got["emailVerifiedField"] != "email_verified" ||
		got["nameField"] != "name" || got["avatarField"] != "picture" || got["loginEnabled"] != true || got["registrationEnabled"] != true {
		t.Fatalf("defaults: %v", got)
	}
	if _, leaked := got["clientSecret"]; leaked || strings.Contains(mustJSON(t, got), "s3cret") {
		t.Fatalf("secret leaked: %v", got)
	}
	var row Provider
	p.DB.Where("slug = ?", "acme").First(&row)
	if row.ClientSecretEncrypted == "" || strings.Contains(row.ClientSecretEncrypted, "s3cret") || row.SortOrder != 100 || !row.PKCEEnabled {
		t.Fatalf("row: %+v", row)
	}

	p.create(map[string]any{"type": "oidc", "name": "Oidc Co", "slug": "", "issuerURL": "https://issuer.example.com", "authURL": "", "tokenURL": "", "userinfoURL": ""})
	w := p.request("GET", "/admin/auth/providers", p.admin, nil)
	testkit.Expect(t, w, 200, "")
	var page struct {
		Total   int
		Results []map[string]any
	}
	testkit.Decode(t, w, &page)
	if page.Total != 2 || page.Results[1]["slug"] != "oidc-co" || page.Results[1]["scopes"] != "openid profile email" {
		t.Fatalf("list: %+v", page)
	}

	// duplicates and non-admins
	testkit.Expect(t, p.request("POST", "/admin/auth/providers", p.admin, p.body(nil)), 400, "request.invalid")
	_, user := p.session(userSpec{Username: "plain", Onboarded: true})
	testkit.Expect(t, p.request("GET", "/admin/auth/providers", user, nil), 403, "auth.admin_required")
	testkit.Expect(t, p.request("GET", "/admin/auth/providers", "", nil), 401, "auth.invalid_token")
}

func TestProviderAdminValidation(t *testing.T) {
	p := newProviderEnv(t)
	cases := []struct {
		name string
		over map[string]any
		code string
		st   int
	}{
		{"oauth2 without userinfo", map[string]any{"userinfoURL": ""}, "auth.provider_oauth_urls_required", 400},
		{"metadata endpoint", map[string]any{"tokenURL": "http://169.254.169.254/x"}, "auth.provider_invalid", 400},
		{"credentials in url", map[string]any{"authURL": "https://u:p@x.example/a"}, "auth.provider_invalid", 400},
		{"non http scheme", map[string]any{"authURL": "file:///etc/passwd"}, "auth.provider_invalid", 400},
		{"oidc without issuer", map[string]any{"type": "oidc"}, "auth.provider_oidc_issuer_required", 400},
		{"missing secret", map[string]any{"clientSecret": ""}, "auth.provider_client_secret_required", 400},
		{"whitespace name", map[string]any{"name": "   "}, "auth.provider_name_required", 400},
		{"punctuation slug and name", map[string]any{"name": "!!!", "slug": ""}, "auth.provider_slug_required", 400},
		{"logo javascript", map[string]any{"logoURL": "javascript:alert(1)"}, "auth.provider_logo_url_invalid", 400},
		{"logo protocol relative", map[string]any{"logoURL": "//evil.example/x.png"}, "auth.provider_logo_url_invalid", 400},
		{"registration without login", map[string]any{"loginEnabled": false, "registrationEnabled": true}, "auth.provider_registration_requires_login", 400},
		{"bad type", map[string]any{"type": "saml"}, "request.invalid_body", 400},
		{"bad role", map[string]any{"defaultRole": "owner"}, "request.invalid_body", 400},
		{"missing client id", map[string]any{"clientID": ""}, "request.invalid_body", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testkit.Expect(t, p.request("POST", "/admin/auth/providers", p.admin, p.body(c.over)), c.st, c.code)
		})
	}
	// loopback and private endpoints are fine for administrators
	p.create(map[string]any{"slug": "private", "name": "Priv", "authURL": "http://10.1.2.3/auth"})
}

func TestProviderSuperadminDefaultRoleProtected(t *testing.T) {
	p := newProviderEnv(t)
	_, admin := p.session(userSpec{Username: "an-admin", Role: RoleAdmin, Onboarded: true})
	testkit.Expect(t, p.request("POST", "/admin/auth/providers", admin, p.body(map[string]any{"defaultRole": "superadmin"})), 403, "auth.provider_superadmin_default_role_protected")
	testkit.Expect(t, p.request("POST", "/admin/auth/providers", admin, p.body(map[string]any{"defaultRole": "admin"})), 200, "")
	p.create(map[string]any{"slug": "super", "name": "Super", "defaultRole": "superadmin"})
}

func TestProviderUpdateIsFullRespecification(t *testing.T) {
	p := newProviderEnv(t)
	created := p.create(map[string]any{"scopes": "custom scope", "logoURL": "/logo.png", "nameField": "login"})
	id := created["publicID"].(string)

	// blank secret keeps the stored one; omitted optional fields revert
	body := p.body(map[string]any{"name": "Acme 2", "clientSecret": ""})
	w := p.request("PATCH", "/admin/auth/providers/"+id, p.admin, body)
	testkit.Expect(t, w, 200, "")
	got := dataMap(t, w)
	if got["name"] != "Acme 2" || got["scopes"] != "profile email" || got["logoURL"] != "" || got["nameField"] != "name" {
		t.Fatalf("update: %v", got)
	}
	var row Provider
	p.DB.Where("public_id = ?", id).First(&row)
	secret, _, err := p.Deps.Secrets.Decrypt(row.ClientSecretEncrypted)
	if err != nil || secret != "s3cret" {
		t.Fatalf("stored secret changed: %q %v", secret, err)
	}
	body["clientSecret"] = "rotated"
	testkit.Expect(t, p.request("PATCH", "/admin/auth/providers/"+id, p.admin, body), 200, "")
	p.DB.Where("public_id = ?", id).First(&row)
	if secret, _, _ = p.Deps.Secrets.Decrypt(row.ClientSecretEncrypted); secret != "rotated" {
		t.Fatalf("secret not replaced")
	}
	testkit.Expect(t, p.request("PATCH", "/admin/auth/providers/nope", p.admin, body), 400, "request.invalid")
}

func TestProviderOrdering(t *testing.T) {
	p := newProviderEnv(t)
	a := p.create(map[string]any{"slug": "a", "name": "A"})["publicID"].(string)
	b := p.create(map[string]any{"slug": "b", "name": "B"})["publicID"].(string)
	c := p.create(map[string]any{"slug": "c", "name": "C"})["publicID"].(string)

	testkit.Expect(t, p.request("PATCH", "/admin/auth/provider-order", p.admin, map[string]any{"providerIDs": []string{c, a, b}}), 200, "")
	var slugs []string
	p.DB.Model(&Provider{}).Order("sort_order").Pluck("slug", &slugs)
	if strings.Join(slugs, ",") != "c,a,b" {
		t.Fatalf("order %v", slugs)
	}
	var orders []int
	p.DB.Model(&Provider{}).Order("sort_order").Pluck("sort_order", &orders)
	if fmt.Sprint(orders) != "[100 200 300]" {
		t.Fatalf("sort orders %v", orders)
	}
	testkit.Expect(t, p.request("PATCH", "/admin/auth/provider-order", p.admin, map[string]any{"providerIDs": []string{a, a}}), 400, "auth.provider_order_invalid")
	testkit.Expect(t, p.request("PATCH", "/admin/auth/provider-order", p.admin, map[string]any{"providerIDs": []string{a, "  "}}), 400, "auth.provider_id_required")
	testkit.Expect(t, p.request("PATCH", "/admin/auth/provider-order", p.admin, map[string]any{"providerIDs": []string{a, "ghost"}}), 400, "request.invalid")
	testkit.Expect(t, p.request("PATCH", "/admin/auth/provider-order", p.admin, map[string]any{}), 400, "request.invalid_body")

	// the public list follows the same order and hides client ids
	w := p.request("GET", "/auth/login-options", "", nil)
	provs := dataMap(t, w)["providers"].([]any)
	if len(provs) != 3 || provs[0].(map[string]any)["slug"] != "c" {
		t.Fatalf("providers: %v", provs)
	}
	if _, has := provs[0].(map[string]any)["clientID"]; has {
		t.Fatalf("clientID must stay admin-only")
	}
}

func TestLoginOptionsHideDisabledProviders(t *testing.T) {
	p := newProviderEnv(t)
	p.create(map[string]any{"slug": "on", "name": "On"})
	p.create(map[string]any{"slug": "off", "name": "Off", "loginEnabled": false, "registrationEnabled": false})
	p.create(map[string]any{"slug": "reg-only-not-allowed", "name": "Reg", "loginEnabled": true, "registrationEnabled": false})
	list := func() int {
		return len(dataMap(t, p.request("GET", "/auth/login-options", "", nil))["providers"].([]any))
	}
	if n := list(); n != 2 {
		t.Fatalf("providers visible: %d", n)
	}
	p.Settings.set(func(s *Settings) { s.ThirdPartyLoginEnabled = false })
	if n := list(); n != 0 {
		t.Fatalf("third-party login off must hide providers: %d", n)
	}
}

func TestProviderDeleteDependencyCheck(t *testing.T) {
	p := newProviderEnv(t)
	prov := p.create(nil)
	id := prov["publicID"].(string)
	var row Provider
	p.DB.Where("public_id = ?", id).First(&row)

	only := p.seedUser(userSpec{Username: "sso-only", Onboarded: true})
	p.linkTo(only.ID, row.ID, "s-1")
	withPw := p.seedUser(userSpec{Username: "has-pw", Password: "correct-horse-9", Onboarded: true})
	p.linkTo(withPw.ID, row.ID, "s-2")

	w := p.request("DELETE", "/admin/auth/providers/"+id, p.admin, nil)
	testkit.Expect(t, w, 409, "identity_provider.delete_conflict")
	if d := details(t, w); d["dependentUsers"] != float64(1) {
		t.Fatalf("details: %v", d)
	}
	w = p.request("DELETE", "/admin/auth/providers/"+id+"?force=true", p.admin, nil)
	testkit.Expect(t, w, 200, "")
	var links, provs int64
	p.DB.Unscoped().Model(&UserLink{}).Count(&links)
	p.DB.Unscoped().Model(&Provider{}).Count(&provs)
	if links != 0 || provs != 0 {
		t.Fatalf("forced delete leaves links=%d providers=%d", links, provs)
	}
	testkit.Expect(t, p.request("DELETE", "/admin/auth/providers/"+id, p.admin, nil), 400, "request.invalid")

	// a user with a second identity does not block deletion
	p1 := p.create(map[string]any{"slug": "p1", "name": "P1"})
	p2 := p.create(map[string]any{"slug": "p2", "name": "P2"})
	var r1, r2 Provider
	p.DB.Where("public_id = ?", p1["publicID"]).First(&r1)
	p.DB.Where("public_id = ?", p2["publicID"]).First(&r2)
	u := p.seedUser(userSpec{Username: "two-ids", Onboarded: true})
	p.linkTo(u.ID, r1.ID, "x")
	p.linkTo(u.ID, r2.ID, "y")
	testkit.Expect(t, p.request("DELETE", "/admin/auth/providers/"+p1["publicID"].(string), p.admin, nil), 200, "")
}

func (e *testEnv) linkTo(userID, providerID uint, subject string) *UserLink {
	e.t.Helper()
	l := UserLink{UserID: userID, ProviderID: providerID, ProviderSubject: subject, ProviderType: "oauth2", LinkedAt: e.Clock.Now()}
	if err := e.DB.Create(&l).Error; err != nil {
		e.t.Fatal(err)
	}
	return &l
}

func TestRecreatingProviderPurgesSoftDeletedSlug(t *testing.T) {
	p := newProviderEnv(t)
	first := p.create(nil)
	var row Provider
	p.DB.Where("public_id = ?", first["publicID"]).First(&row)
	if err := p.DB.Delete(&row).Error; err != nil { // soft delete
		t.Fatal(err)
	}
	p.create(nil)
}

// ---- the bridge -----------------------------------------------------------------------------------------

var (
	challengeSeed = strings.Repeat("c", 43)
	stateSeed     = strings.Repeat("s", 43)
	verifierSeed  = strings.Repeat("v", 50)
)

func authorizeBody(over map[string]any) map[string]any {
	b := map[string]any{
		"clientID": "riochat-web", "redirectURI": webRedir,
		"codeChallenge": pkceChallenge(verifierSeed), "clientState": stateSeed,
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

// flow runs authorize -> callback and returns the redirect query.
func (p *providerEnv) flow(slug, bearer string, over map[string]any, code string) (url.Values, *httptest.ResponseRecorder) {
	p.t.Helper()
	path := "/auth/providers/" + slug + "/authorize"
	if bearer != "" {
		path = "/me/identities/providers/" + slug + "/authorize"
	}
	w := p.request("POST", path, bearer, authorizeBody(over))
	testkit.Expect(p.t, w, 200, "")
	if w.Header().Get("Cache-Control") != "no-store" {
		p.t.Fatalf("authorize must not be cached")
	}
	authURL, err := url.Parse(dataMap(p.t, w)["authorizationURL"].(string))
	if err != nil {
		p.t.Fatal(err)
	}
	state := authURL.Query().Get("state")
	cb := p.request("GET", "/auth/providers/"+slug+"/callback?code="+code+"&state="+url.QueryEscape(state), "", nil)
	if cb.Code != http.StatusFound {
		p.t.Fatalf("callback: %d %s", cb.Code, cb.Body)
	}
	loc, err := url.Parse(cb.Header().Get("Location"))
	if err != nil {
		p.t.Fatal(err)
	}
	return loc.Query(), cb
}

func (p *providerEnv) exchange(slug, bearer, grant, verifier string) *httptest.ResponseRecorder {
	path := "/auth/providers/" + slug + "/exchange"
	if bearer != "" {
		path = "/me/identities/providers/" + slug + "/exchange"
	}
	return p.request("POST", path, bearer, map[string]any{"clientID": "riochat-web", "grant": grant, "codeVerifier": verifier})
}

func TestBridgeAuthorizeBuildsProviderURL(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	w := p.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(map[string]any{"next": "/chat/42"}))
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["expiresAt"] == "" {
		t.Fatalf("no expiry: %v", m)
	}
	u, err := url.Parse(m["authorizationURL"].(string))
	if err != nil || !strings.HasPrefix(u.String(), p.up.URL+"/auth?") {
		t.Fatalf("auth url %v %v", u, err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != "cid" ||
		q.Get("redirect_uri") != apiBase+"/api/v1/auth/providers/acme/callback" || q.Get("scope") != "profile email" ||
		q.Get("state") == "" || len(q.Get("code_challenge")) != 43 {
		t.Fatalf("query: %v", q)
	}
	if q.Get("code_challenge") == pkceChallenge(verifierSeed) {
		t.Fatalf("the provider leg must use its own PKCE pair, not the client's")
	}
}

func TestBridgeAuthorizeValidation(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	p.create(map[string]any{"slug": "nologin", "name": "NoLogin", "loginEnabled": false, "registrationEnabled": false})
	p.create(map[string]any{"slug": "noreg", "name": "NoReg", "registrationEnabled": false})
	post := func(slug string, over map[string]any) *httptest.ResponseRecorder {
		b := authorizeBody(map[string]any{"redirectURI": "http://localhost:3000/auth/callback?provider=" + slug})
		for k, v := range over {
			b[k] = v
		}
		return p.request("POST", "/auth/providers/"+slug+"/authorize", "", b)
	}
	cases := []struct {
		name string
		slug string
		over map[string]any
		code string
	}{
		{"web wrong path", "acme", map[string]any{"redirectURI": "http://localhost:3000/oauth/cb?provider=acme"}, "auth.invalid_redirect_uri"},
		{"web wrong provider param", "acme", map[string]any{"redirectURI": "http://localhost:3000/auth/callback?provider=other"}, "auth.invalid_redirect_uri"},
		{"web foreign origin", "acme", map[string]any{"redirectURI": "https://evil.example/auth/callback?provider=acme"}, "auth.invalid_redirect_uri"},
		{"web wildcard never matches", "acme", map[string]any{"redirectURI": "https://anything.example/auth/callback?provider=acme"}, "auth.invalid_redirect_uri"},
		{"web javascript", "acme", map[string]any{"redirectURI": "javascript:alert(1)"}, "auth.invalid_redirect_uri"},
		{"native wrong redirect", "acme", map[string]any{"clientID": "com.riochat.native", "redirectURI": "com.riochat:/other"}, "auth.invalid_redirect_uri"},
		{"desktop https", "acme", map[string]any{"clientID": "com.riochat.desktop", "redirectURI": "https://127.0.0.1:5000/oauth/callback"}, "auth.invalid_redirect_uri"},
		{"desktop no port", "acme", map[string]any{"clientID": "com.riochat.desktop", "redirectURI": "http://127.0.0.1/oauth/callback"}, "auth.invalid_redirect_uri"},
		{"desktop non loopback", "acme", map[string]any{"clientID": "com.riochat.desktop", "redirectURI": "http://example.com:5000/oauth/callback"}, "auth.invalid_redirect_uri"},
		{"desktop query", "acme", map[string]any{"clientID": "com.riochat.desktop", "redirectURI": "http://127.0.0.1:5000/oauth/callback?x=1"}, "auth.invalid_redirect_uri"},
		{"unknown client", "acme", map[string]any{"clientID": "mystery"}, "auth.provider_invalid"},
		{"short challenge", "acme", map[string]any{"codeChallenge": "short"}, "request.invalid_body"},
		{"challenge with space", "acme", map[string]any{"codeChallenge": strings.Repeat("a", 42) + " "}, "auth.invalid_pkce"},
		{"state with symbol", "acme", map[string]any{"clientState": strings.Repeat("a", 42) + "+"}, "auth.invalid_pkce"},
		{"unknown provider", "ghost", nil, "request.invalid"},
		{"login disabled", "nologin", nil, "auth.provider_login_disabled"},
		{"register disabled", "noreg", map[string]any{"intent": "register"}, "auth.provider_registration_disabled"},
		{"bad intent", "acme", map[string]any{"intent": "bind"}, "request.invalid_body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { testkit.Expect(t, post(c.slug, c.over), 400, c.code) })
	}
	testkit.Expect(t, post("acme", map[string]any{"clientID": "com.riochat.native", "redirectURI": "com.riochat:/oauth/callback"}), 200, "")
	testkit.Expect(t, post("acme", map[string]any{"clientID": "com.riochat.desktop", "redirectURI": "http://127.0.0.1:5000/oauth/callback"}), 200, "")
	testkit.Expect(t, post("acme", map[string]any{"redirectURI": "https://app.example.com/auth/callback?provider=acme"}), 200, "")
	testkit.Expect(t, post("noreg", nil), 200, "")

	p.Settings.set(func(s *Settings) { s.ThirdPartyLoginEnabled = false })
	testkit.Expect(t, post("acme", nil), 400, "auth.provider_login_disabled")
}

func TestBridgeAuthorizeRespectsEnvironmentAndConfig(t *testing.T) {
	up := newUpstream(t)
	// production: loopback redirect targets are not acceptable, base URL missing breaks the callback
	e := newEnv(t, withCfg(func(c *configT) {
		c.App.Env = "prod"
		c.Server.PublicAPIBaseURL = apiBase
		c.Server.PublicWebBaseURL = "https://app.example.com"
		c.Server.CORSAllowOrigin = "https://app.example.com"
		c.Security.JWTSecret = "a-production-grade-secret-0123456789"
	}), withHooks(func(h *Hooks) { h.HTTPClient = up.Client() }))
	_, admin := e.session(userSpec{Username: "root-admin", Role: RoleSuperAdmin, Onboarded: true})
	body := map[string]any{"type": "oauth2", "name": "Acme", "slug": "acme", "clientID": "cid", "clientSecret": "s",
		"authURL": up.URL + "/auth", "tokenURL": up.URL + "/token", "userinfoURL": up.URL + "/userinfo"}
	testkit.Expect(t, e.request("POST", "/admin/auth/providers", admin, body), 200, "")
	w := e.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(nil))
	testkit.Expect(t, w, 400, "auth.invalid_redirect_uri")
	if testkit.Envelope(t, w).ErrorMsg != "redirect uri origin is not allowed" {
		t.Fatalf("message: %s", w.Body)
	}
	testkit.Expect(t, e.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(map[string]any{
		"redirectURI": "https://app.example.com/auth/callback?provider=acme"})), 200, "")
}

func TestBridgeMisconfiguredCallbackBase(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	// a base URL carrying a query is unusable
	cfg := p.Deps.Config
	cfg.Update(func(c *configT) { c.Server.PublicAPIBaseURL = "https://api.example.com/?x=1" })
	testkit.Expect(t, p.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(nil)), 400, "auth.provider_callback_misconfigured")
}

func TestBridgeLoginCreatesAccountAndSession(t *testing.T) {
	p := newProviderEnv(t)
	p.create(map[string]any{"defaultRole": "admin"})
	q, _ := p.flow("acme", "", map[string]any{"next": "/chat/9"}, "the-code")
	if q.Get("provider") != "acme" || q.Get("state") != stateSeed || q.Get("intent") != "login" || q.Get("next") != "/chat/9" || len(q.Get("grant")) < 43 {
		t.Fatalf("redirect query: %v", q)
	}
	// the provider-side exchange saw the right parameters
	form := p.up.tokenForms[0]
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "the-code" || form.Get("client_id") != "cid" ||
		form.Get("client_secret") != "s3cret" || form.Get("redirect_uri") != apiBase+"/api/v1/auth/providers/acme/callback" || len(form.Get("code_verifier")) < 43 {
		t.Fatalf("token form: %v", form)
	}
	if p.up.userAuth[0] != "Bearer at-123" {
		t.Fatalf("userinfo auth %q", p.up.userAuth[0])
	}

	// a wrong verifier cannot find the grant, and does not burn it
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), strings.Repeat("w", 50)), 400, "auth.oauth_state_expired")
	w := p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange must not be cached")
	}
	d := decodeLogin(t, w)
	if d.AccessToken == "" || refreshCookie(w) == nil {
		t.Fatalf("session expected: %+v", d)
	}
	user := d.User
	if user["email"] != "alice@example.com" || user["role"] != "admin" || user["emailSource"] != "provider_verified" ||
		user["emailVerifiedAt"] == nil || user["passwordEnabled"] != false || user["passwordOrigin"] != "sso_placeholder" ||
		user["displayName"] != "Alice A" || user["avatarURL"] != "https://img.example/a.png" || user["onboardingCompletedAt"] != nil {
		t.Fatalf("created user: %v", user)
	}
	if name := user["username"].(string); !strings.HasPrefix(name, "acme-") || len(name) > 16 {
		t.Fatalf("username %q", name)
	}
	var links int64
	p.DB.Model(&UserLink{}).Where("provider_subject = ?", "subject-1").Count(&links)
	if links != 1 {
		t.Fatalf("link rows: %d", links)
	}
	// used grant
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.oauth_state_expired")

	// second login reuses the account
	q2, _ := p.flow("acme", "", nil, "again")
	w = p.exchange("acme", "", q2.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	if decodeLogin(t, w).User["id"] != user["id"] {
		t.Fatalf("same identity must map to the same account")
	}
	var users int64
	p.DB.Model(&User{}).Where("email = ?", "alice@example.com").Count(&users)
	if users != 1 || p.countEvents("provider_login", "success") != 2 {
		t.Fatalf("users=%d events=%d", users, p.countEvents("provider_login", "success"))
	}
}

func TestBridgeNextPathIsSanitised(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	for _, c := range []struct{ in, want string }{
		{"//evil.example", "/chat"}, {"https://evil.example", "/chat"}, {"", "/chat"}, {"/a/b?c=d", "/a/b?c=d"},
	} {
		q, _ := p.flow("acme", "", map[string]any{"next": c.in}, "c")
		if q.Get("next") != c.want {
			t.Errorf("next %q -> %q, want %q", c.in, q.Get("next"), c.want)
		}
	}
}

func TestBridgeCallbackStateChecks(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	p.create(map[string]any{"slug": "other", "name": "Other"})
	w := p.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(nil))
	state := mustURL(t, dataMap(t, w)["authorizationURL"].(string)).Query().Get("state")
	cb := func(slug, st string) *httptest.ResponseRecorder {
		return p.request("GET", "/auth/providers/"+slug+"/callback?code=c&state="+url.QueryEscape(st), "", nil)
	}
	testkit.Expect(t, cb("acme", ""), 400, "auth.oauth_state_invalid")
	testkit.Expect(t, cb("acme", "garbage"), 400, "auth.oauth_state_invalid")
	testkit.Expect(t, cb("acme", state[:len(state)-3]+"AAA"), 400, "auth.oauth_state_invalid")
	testkit.Expect(t, cb("other", state), 400, "auth.oauth_state_invalid") // slug mismatch
	if r := cb("acme", state); r.Code != http.StatusFound {
		t.Fatalf("valid state: %d %s", r.Code, r.Body)
	}
	testkit.Expect(t, cb("acme", state), 400, "auth.oauth_state_expired") // replay

	w = p.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(nil))
	state = mustURL(t, dataMap(t, w)["authorizationURL"].(string)).Query().Get("state")
	p.Clock.Advance(11 * 60 * 1e9)
	testkit.Expect(t, cb("acme", state), 400, "auth.oauth_state_expired")
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestBridgeProviderErrorsBecomeGrantErrors(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	// denial by the user
	w := p.request("POST", "/auth/providers/acme/authorize", "", authorizeBody(nil))
	state := mustURL(t, dataMap(t, w)["authorizationURL"].(string)).Query().Get("state")
	cb := p.request("GET", "/auth/providers/acme/callback?error=access_denied&state="+url.QueryEscape(state), "", nil)
	if cb.Code != http.StatusFound {
		t.Fatalf("callback: %d", cb.Code)
	}
	q := mustURL(t, cb.Header().Get("Location")).Query()
	w = p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 400, "auth.provider_invalid")
	if testkit.Envelope(t, w).ErrorMsg != "provider authentication failed" {
		t.Fatalf("message %s", w.Body)
	}
	// the grant was consumed even though it carried an error
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.oauth_state_expired")

	// missing code
	q, _ = p.flow("acme", "", nil, "")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.provider_invalid")

	// token endpoint failure
	p.up.tokenStatus = 500
	q, _ = p.flow("acme", "", nil, "x")
	w = p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 400, "auth.provider_invalid")
	p.up.tokenStatus = 0

	// no subject
	p.up.setProfile(map[string]any{"email": "a@example.com"})
	q, _ = p.flow("acme", "", nil, "x")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.provider_subject_missing")
	// malformed email
	p.up.setProfile(map[string]any{"sub": "1", "email": "no at sign"})
	q, _ = p.flow("acme", "", nil, "x")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.invalid_email")
}

func TestBridgeExchangeChecksClientAndSlug(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	p.create(map[string]any{"slug": "other", "name": "Other"})
	q, _ := p.flow("acme", "", nil, "c")
	g := q.Get("grant")
	w := p.request("POST", "/auth/providers/acme/exchange", "", map[string]any{"clientID": "com.riochat.native", "grant": g, "codeVerifier": verifierSeed})
	testkit.Expect(t, w, 400, "auth.oauth_state_invalid")
	// consumed by the failed attempt
	testkit.Expect(t, p.exchange("acme", "", g, verifierSeed), 400, "auth.oauth_state_expired")

	q, _ = p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("other", "", q.Get("grant"), verifierSeed), 400, "auth.oauth_state_invalid")

	testkit.Expect(t, p.request("POST", "/auth/providers/acme/exchange", "", map[string]any{"clientID": "riochat-web", "grant": "short", "codeVerifier": verifierSeed}), 400, "request.invalid_body")
	testkit.Expect(t, p.request("POST", "/auth/providers/acme/exchange", "", map[string]any{"clientID": "riochat-web", "grant": strings.Repeat("g", 43) + " ", "codeVerifier": verifierSeed}), 400, "auth.invalid_pkce")
}

func TestBridgeGrantExpires(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	q, _ := p.flow("acme", "", nil, "c")
	p.Clock.Advance(100 * 1e9)
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.oauth_state_expired")
}

func TestBridgeAutoLinksVerifiedEmail(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	existing := p.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	q, _ := p.flow("acme", "", nil, "c")
	w := p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	if decodeLogin(t, w).User["id"] != float64(existing.ID) {
		t.Fatalf("must sign in as the existing account")
	}
	var link UserLink
	if err := p.DB.Where("provider_subject = ?", "subject-1").First(&link).Error; err != nil || link.UserID != existing.ID {
		t.Fatalf("link: %+v %v", link, err)
	}
	var users int64
	p.DB.Model(&User{}).Where("email = ?", "alice@example.com").Count(&users)
	if users != 1 {
		t.Fatalf("no second account may be created")
	}
}

func TestBridgeEmailConflictWithoutAutoLink(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	p.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	p.Settings.set(func(s *Settings) { s.AutoLinkVerifiedEmail = false })
	q, _ := p.flow("acme", "", nil, "c")
	w := p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 409, "auth.provider_email_conflict")
	d := details(t, w)
	if d["providerSlug"] != "acme" || d["email"] != "alice@example.com" || d["action"] != "sign_in_then_bind" {
		t.Fatalf("details: %v", d)
	}
	if strings.Contains(w.Body.String(), "correct-horse") || strings.Contains(w.Body.String(), `"username"`) {
		t.Fatalf("must not reveal account data: %s", w.Body)
	}

	// an unverified provider email conflicts even with auto-link on
	p.Settings.set(func(s *Settings) { s.AutoLinkVerifiedEmail = true })
	p.up.setProfile(map[string]any{"sub": "subject-2", "email": "alice@example.com", "email_verified": false})
	q, _ = p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 409, "auth.provider_email_conflict")
}

func TestBridgeRegistrationDisabledOnProvider(t *testing.T) {
	p := newProviderEnv(t)
	p.create(map[string]any{"registrationEnabled": false})
	q, _ := p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.provider_account_not_registered")
	var n int64
	p.DB.Model(&User{}).Where("email = ?", "alice@example.com").Count(&n)
	if n != 0 {
		t.Fatalf("no account may be created")
	}
	// an already linked identity still signs in
	var row Provider
	p.DB.Where("slug = ?", "acme").First(&row)
	u := p.seedUser(userSpec{Username: "linked", Onboarded: true})
	p.linkTo(u.ID, row.ID, "subject-1")
	q, _ = p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 200, "")
}

func TestBridgeLoginStatusAndTwoFactor(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	var row Provider
	p.DB.Where("slug = ?", "acme").First(&row)

	locked := p.seedUser(userSpec{Username: "locked", Status: StatusLocked, Onboarded: true})
	p.linkTo(locked.ID, row.ID, "subject-1")
	q, _ := p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 423, "auth.account_locked")

	p.DB.Model(&User{}).Where("id = ?", locked.ID).Update("status", StatusSuspended)
	q, _ = p.flow("acme", "", nil, "c")
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.invalid_credentials")

	// TOTP users get the challenge instead of a session
	p.DB.Model(&User{}).Where("id = ?", locked.ID).Update("status", StatusActive)
	p.DB.Model(&UserLink{}).Where("user_id = ?", locked.ID).Update("provider_subject", "subject-1")
	d, _ := p.signInAs(locked)
	secret, _ := p.enableTOTP(d)
	_ = secret
	q, _ = p.flow("acme", "", nil, "c")
	w := p.exchange("acme", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	body := decodeLogin(t, w)
	if !body.TwoFactorRequired || body.TwoFactorChallengeToken == "" || body.AccessToken != "" || refreshCookie(w) != nil {
		t.Fatalf("challenge expected: %+v", body)
	}
	if p.countEvents("provider_login", "challenge") != 1 {
		t.Fatalf("challenge event missing")
	}
}

// signInAs gives a seeded passwordless user a bearer token without a password.
func (e *testEnv) signInAs(u *User) (token string, sessionID uint) {
	e.t.Helper()
	sess := Session{SessionID: newPublicID(), UserID: u.ID, IssuedAt: e.Clock.Now(), ExpiresAt: e.Clock.Now().Add(24 * 3600 * 1e9)}
	if err := e.DB.Create(&sess).Error; err != nil {
		e.t.Fatal(err)
	}
	access, _, _, _, _, err := e.Svc.issueTokens(u, &sess)
	if err != nil {
		e.t.Fatal(err)
	}
	return access, sess.ID
}

func TestBridgeBindFlow(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	u, tok := p.session(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	_, other := p.session(userSpec{Username: "bob", Password: "correct-horse-9", Onboarded: true})

	q, _ := p.flow("acme", tok, nil, "c")
	if q.Get("intent") != "bind" {
		t.Fatalf("intent %q", q.Get("intent"))
	}
	// a different user cannot redeem it
	testkit.Expect(t, p.exchange("acme", other, q.Get("grant"), verifierSeed), 400, "auth.oauth_state_invalid")
	q, _ = p.flow("acme", tok, nil, "c")
	// the public exchange refuses bind grants
	testkit.Expect(t, p.exchange("acme", "", q.Get("grant"), verifierSeed), 400, "auth.oauth_state_invalid")

	q, _ = p.flow("acme", tok, nil, "c")
	w := p.exchange("acme", tok, q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	ident := dataMap(t, w)["identity"].(map[string]any)
	if ident["providerSlug"] != "acme" || ident["providerName"] != "Acme" || ident["email"] != "alice@example.com" || ident["emailVerified"] != true || ident["providerDisplayName"] != "Alice A" {
		t.Fatalf("identity: %v", ident)
	}
	if p.countEvents("provider_bind", "success") != 1 {
		t.Fatalf("bind event missing")
	}

	// listing
	w = p.request("GET", "/me/identities", tok, nil)
	testkit.Expect(t, w, 200, "")
	list := dataMap(t, w)["results"].([]any)
	if len(list) != 1 {
		t.Fatalf("identities: %v", list)
	}

	// binding the same identity again is idempotent
	q, _ = p.flow("acme", tok, nil, "c")
	testkit.Expect(t, p.exchange("acme", tok, q.Get("grant"), verifierSeed), 200, "")
	var n int64
	p.DB.Model(&UserLink{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 1 {
		t.Fatalf("links %d", n)
	}

	// another account cannot take the same identity
	q, _ = p.flow("acme", other, nil, "c")
	testkit.Expect(t, p.exchange("acme", other, q.Get("grant"), verifierSeed), 400, "auth.provider_identity_conflict")

	// the same provider with a new subject: already bound
	p.up.setProfile(map[string]any{"sub": "subject-9", "email": "zzz@example.com", "email_verified": true})
	q, _ = p.flow("acme", tok, nil, "c")
	testkit.Expect(t, p.exchange("acme", tok, q.Get("grant"), verifierSeed), 400, "auth.provider_already_bound")

	// an email owned by somebody else
	p.up.setProfile(map[string]any{"sub": "subject-10", "email": "alice@example.com", "email_verified": true})
	q, _ = p.flow("acme", other, nil, "c")
	w = p.exchange("acme", other, q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 400, "auth.provider_email_conflict")
	if testkit.Envelope(t, w).Details != nil {
		t.Fatalf("bind conflicts carry no details: %s", w.Body)
	}
}

func TestBindRebindAfterSoftDelete(t *testing.T) {
	p := newProviderEnv(t)
	p.create(nil)
	u, tok := p.session(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	var row Provider
	p.DB.Where("slug = ?", "acme").First(&row)
	old := p.linkTo(u.ID, row.ID, "subject-1")
	if err := p.DB.Delete(old).Error; err != nil { // soft delete
		t.Fatal(err)
	}
	q, _ := p.flow("acme", tok, nil, "c")
	testkit.Expect(t, p.exchange("acme", tok, q.Get("grant"), verifierSeed), 200, "")
}

func TestUnbindIdentityRules(t *testing.T) {
	p := newProviderEnv(t)
	prov := p.create(nil)
	var row Provider
	p.DB.Where("public_id = ?", prov["publicID"]).First(&row)

	sso := p.seedUser(userSpec{Username: "sso-user", Onboarded: true})
	link := p.linkTo(sso.ID, row.ID, "s1")
	tok, _ := p.signInAs(sso)

	url := func(id any) string { return fmt.Sprintf("/me/identities/%v", id) }
	testkit.Expect(t, p.request("DELETE", url("abc"), tok, nil), 400, "identity.invalid_id")
	testkit.Expect(t, p.request("DELETE", url(0), tok, nil), 400, "identity.invalid_id")
	testkit.Expect(t, p.request("DELETE", url(-3), tok, nil), 400, "identity.invalid_id")
	testkit.Expect(t, p.request("DELETE", url(link.ID+100), tok, nil), 404, "identity.not_found")
	// somebody else's identity looks the same as a missing one
	_, bobTok := p.session(userSpec{Username: "bob", Password: "correct-horse-9", Onboarded: true})
	testkit.Expect(t, p.request("DELETE", url(link.ID), bobTok, nil), 404, "identity.not_found")
	// sole login method
	testkit.Expect(t, p.request("DELETE", url(link.ID), tok, nil), 400, "auth.last_login_method_required")

	// with a second identity the first can go
	other := p.create(map[string]any{"slug": "second", "name": "Second"})
	var row2 Provider
	p.DB.Where("public_id = ?", other["publicID"]).First(&row2)
	p.linkTo(sso.ID, row2.ID, "s2")
	testkit.Expect(t, p.request("DELETE", url(link.ID), tok, nil), 200, "")

	// a password-enabled account may drop its only identity
	pwUser := p.seedUser(userSpec{Username: "pw-user", Password: "correct-horse-9", Onboarded: true})
	l := p.linkTo(pwUser.ID, row.ID, "s3")
	tok2, _ := p.signInAs(pwUser)
	testkit.Expect(t, p.request("DELETE", url(l.ID), tok2, nil), 200, "")
}

func TestGitHubVerifiedEmailLookup(t *testing.T) {
	p := newProviderEnv(t)
	p.up.setProfile(map[string]any{"id": 42, "login": "octo", "name": "Octo Cat", "email": nil, "avatar_url": "https://img.example/o.png"})
	p.up.emails = []map[string]any{
		{"email": "second@example.com", "primary": false, "verified": true},
		{"email": "Primary@Example.com", "primary": true, "verified": true},
		{"email": "unverified@example.com", "primary": false, "verified": false},
	}
	p.create(map[string]any{"slug": "github", "name": "GitHub", "subjectField": "id", "avatarField": "avatar_url"})
	q, _ := p.flow("github", "", map[string]any{"redirectURI": "http://localhost:3000/auth/callback?provider=github"}, "c")
	w := p.exchange("github", "", q.Get("grant"), verifierSeed)
	testkit.Expect(t, w, 200, "")
	user := decodeLogin(t, w).User
	if user["email"] != "primary@example.com" || user["emailVerifiedAt"] == nil || user["displayName"] != "Octo Cat" {
		t.Fatalf("user: %v", user)
	}
}

func TestClaimMapping(t *testing.T) {
	prov := &Provider{SubjectField: "data.id", EmailField: "data.mail", EmailVerifiedField: "data.ok", NameField: "data.profile.name", AvatarField: "pic", Slug: "x", Name: "X"}
	c, err := claimsFromProfile(prov, "https://idp.example/me", map[string]any{
		"data": map[string]any{"id": json.Number("77"), "mail": "Bob@Example.COM", "ok": "yes", "profile": map[string]any{"name": " Bob "}},
	})
	if err != nil || c.Subject != "77" || c.Email != "bob@example.com" || !c.EmailVerified || c.Name != "Bob" {
		t.Fatalf("claims: %+v %v", c, err)
	}
	if _, err := claimsFromProfile(prov, "", map[string]any{"data": map[string]any{}}); err == nil {
		t.Fatalf("missing subject must fail")
	}
	// a generic "verified" is ignored unless the provider is Discord
	prov = &Provider{SubjectField: "sub", EmailField: "email", EmailVerifiedField: "email_verified", NameField: "name", Slug: "x", Name: "X"}
	c, _ = claimsFromProfile(prov, "https://idp.example/me", map[string]any{"sub": "1", "email": "a@b.co", "verified": true})
	if c.EmailVerified {
		t.Fatalf("generic verified must not count")
	}
	c, _ = claimsFromProfile(prov, "https://idp.example/me", map[string]any{"sub": "1", "email": "a@b.co", "verified_email": "1"})
	if !c.EmailVerified {
		t.Fatalf("verified_email counts")
	}
	prov.Slug = "discord"
	c, _ = claimsFromProfile(prov, "https://discord.com/api/users/@me", map[string]any{"sub": "1", "email": "a@b.co", "verified": true})
	if !c.EmailVerified {
		t.Fatalf("discord honours verified")
	}
	// display name falls back to email, then subject
	c, _ = claimsFromProfile(prov, "", map[string]any{"sub": "9", "email": "a@b.co"})
	if c.DisplayName != "a@b.co" {
		t.Fatalf("display %q", c.DisplayName)
	}
	c, _ = claimsFromProfile(prov, "", map[string]any{"sub": "9"})
	if c.DisplayName != "9" {
		t.Fatalf("display %q", c.DisplayName)
	}
}

func TestOIDCDiscoveryFillsEndpoints(t *testing.T) {
	p := newProviderEnv(t)
	mux := http.NewServeMux()
	disco := httptest.NewServer(mux)
	defer disco.Close()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"authorization_endpoint":%q,"token_endpoint":%q,"userinfo_endpoint":%q}`,
			p.up.URL+"/auth", p.up.URL+"/token", p.up.URL+"/userinfo")
	})
	p.Hooks.HTTPClient = http.DefaultClient
	p.Svc.h.HTTPClient = http.DefaultClient
	p.create(map[string]any{"type": "oidc", "name": "Oidc", "slug": "oidc", "authURL": "", "tokenURL": "", "userinfoURL": "", "issuerURL": disco.URL})
	w := p.request("POST", "/auth/providers/oidc/authorize", "", authorizeBody(map[string]any{"redirectURI": "http://localhost:3000/auth/callback?provider=oidc"}))
	testkit.Expect(t, w, 200, "")
	if !strings.HasPrefix(dataMap(t, w)["authorizationURL"].(string), p.up.URL+"/auth?") {
		t.Fatalf("discovery not used: %s", w.Body)
	}
	if !strings.Contains(dataMap(t, w)["authorizationURL"].(string), "scope=openid+profile+email") {
		t.Fatalf("oidc scopes: %s", w.Body)
	}
	disco.Close()
	w = p.request("POST", "/auth/providers/oidc/authorize", "", authorizeBody(map[string]any{"redirectURI": "http://localhost:3000/auth/callback?provider=oidc"}))
	testkit.Expect(t, w, 400, "auth.provider_upstream_failed")
}

func TestProvidersAreGatedByCapability(t *testing.T) {
	p := newProviderEnv(t)
	// local mode closes the identity-provider gate
	local := newEnv(t, withCfg(func(c *configT) { c.Local = localConfig() }))
	_, tok := local.session(userSpec{Username: "local-me", Onboarded: true, Role: RoleSuperAdmin})
	w := local.request("GET", "/me/identities", tok, nil)
	testkit.Expect(t, w, 404, "feature.disabled")
	if details(t, w)["feature"] != "identityProviders" {
		t.Fatalf("details: %s", w.Body)
	}
	testkit.Expect(t, local.request("POST", "/auth/providers/x/authorize", "", authorizeBody(nil)), 404, "feature.disabled")
	testkit.Expect(t, local.request("GET", "/admin/auth/providers", tok, nil), 404, "feature.disabled")
	_ = p
}

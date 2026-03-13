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

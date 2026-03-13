package auth

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- outbound policy ----------------------------------------------------------------------------

func TestEgressPolicyBlocksInternalTargetsUnlessTrusted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	e := newEnv(t)

	get := func(client *http.Client, target string) (int, error) {
		req, _ := http.NewRequest("GET", target, nil)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}
	if _, err := get(e.Svc.httpClient(), srv.URL); err == nil {
		t.Fatalf("loopback must be blocked for an untrusted origin")
	}
	if code, err := get(e.Svc.httpClient(srv.URL+"/some/path"), srv.URL); err != nil || code != 200 {
		t.Fatalf("a configured origin is reachable: %d %v", code, err)
	}
	if _, err := get(e.Svc.httpClient(srv.URL), "http://169.254.169.254/latest/meta-data"); err == nil {
		t.Fatalf("metadata addresses are never reachable")
	}
	if _, err := get(e.Svc.httpClient(), "http://10.0.0.5:81/"); err == nil {
		t.Fatalf("private ranges are blocked")
	}
}

func TestEgressPolicyRedirectsStayOnTrustedOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "elsewhere") }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL, http.StatusFound)
		case "/home":
			http.Redirect(w, r, "/landing", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "landed")
		}
	}))
	defer srv.Close()
	e := newEnv(t)
	client := e.Svc.httpClient(srv.URL)
	if _, err := client.Get(srv.URL + "/away"); err == nil {
		t.Fatalf("redirect to another origin must fail")
	}
	resp, err := client.Get(srv.URL + "/home")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "landed" {
		t.Fatalf("body %q", body)
	}
}

func TestEgressAllowListsFromConfiguration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	e := newEnv(t, withCfg(func(c *configT) { c.Security.SSRFAllowedCIDRs = "127.0.0.0/8" }))
	resp, err := e.Svc.httpClient().Get(srv.URL)
	if err != nil {
		t.Fatalf("allowed network: %v", err)
	}
	resp.Body.Close()
}

func TestResponseBodyIsCapped(t *testing.T) {
	big := strings.Repeat("x", maxBodyBytes+10)
	if _, err := readCapped(strings.NewReader(big)); err != errBodyTooLarge {
		t.Fatalf("err %v", err)
	}
	if b, err := readCapped(strings.NewReader("small")); err != nil || string(b) != "small" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestEndpointURLValidation(t *testing.T) {
	good := []string{"https://idp.example.com/auth", "http://localhost:9000/x", "http://10.0.0.1/x", "http://[::1]:80/x"}
	bad := []string{"", "ftp://x/y", "https:///nohost", "https://user:pw@x.example/a", "http://169.254.169.254/", "http://[fe80::1]/", "http://metadata.google.internal/", "javascript:alert(1)"}
	for _, g := range good {
		if err := validateEndpointURL(g); err != nil {
			t.Errorf("%q rejected: %v", g, err)
		}
	}
	for _, b := range bad {
		if err := validateEndpointURL(b); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

// ---- SMTP ---------------------------------------------------------------------------------------------

type smtpCapture struct {
	mu       sync.Mutex
	from     string
	rcpt     []string
	data     string
	authLine string
}

// fakeSMTP is a tiny SMTP server; withAuth controls whether AUTH is offered.
func fakeSMTP(t *testing.T, withAuth bool) (port int, cap *smtpCapture) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cap = &smtpCapture{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				say := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
				say("220 fake ESMTP")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					up := strings.ToUpper(line)
					switch {
					case strings.HasPrefix(up, "EHLO"):
						if withAuth {
							say("250-fake")
							say("250 AUTH PLAIN")
						} else {
							say("250 fake")
						}
					case strings.HasPrefix(up, "AUTH PLAIN"):
						cap.mu.Lock()
						cap.authLine = line
						cap.mu.Unlock()
						say("235 ok")
					case strings.HasPrefix(up, "MAIL FROM:"):
						cap.mu.Lock()
						cap.from = line
						cap.mu.Unlock()
						say("250 ok")
					case strings.HasPrefix(up, "RCPT TO:"):
						cap.mu.Lock()
						cap.rcpt = append(cap.rcpt, line)
						cap.mu.Unlock()
						say("250 ok")
					case up == "DATA":
						say("354 go")
						var sb strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							sb.WriteString(l)
						}
						cap.mu.Lock()
						cap.data = sb.String()
						cap.mu.Unlock()
						say("250 queued")
					case up == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, cap
}

func TestSMTPDelivery(t *testing.T) {
	port, cap := fakeSMTP(t, true)
	e := newEnv(t, withHooks(func(h *Hooks) { h.Mailer = nil }))
	e.Settings.set(func(s *Settings) {
		s.EmailVerificationEnabled = true
		s.SMTPHost, s.SMTPPort, s.SMTPUsername, s.SMTPPassword = "127.0.0.1", port, "bot@example.com", "pw"
		s.SMTPFrom = "Riochat <noreply@example.com>"
	})
	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if !strings.Contains(cap.from, "<noreply@example.com>") || len(cap.rcpt) != 1 || !strings.Contains(cap.rcpt[0], "<new@example.com>") {
		t.Fatalf("envelope: from=%q rcpt=%v", cap.from, cap.rcpt)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(cap.authLine, "AUTH PLAIN "))
	if string(raw) != "\x00bot@example.com\x00pw" {
		t.Fatalf("auth %q", raw)
	}
	msg, err := mail.ReadMessage(strings.NewReader(strings.ReplaceAll(cap.data, "\r\n", "\n")))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if subj != "Your Riochat verification code" || mt != "multipart/alternative" || params["boundary"] == "" {
		t.Fatalf("headers: subject=%q type=%q", subj, mt)
	}
	body, _ := io.ReadAll(msg.Body)
	if strings.Count(string(body), "Content-Type: text/") != 2 || !strings.Contains(string(body), "text/plain") || !strings.Contains(string(body), "text/html") {
		t.Fatalf("both alternatives expected: %s", body)
	}
}

func TestSMTPErrorsAreOpaqueAndTyped(t *testing.T) {
	e := newEnv(t, withHooks(func(h *Hooks) { h.Mailer = nil }))
	e.Settings.set(func(s *Settings) { s.EmailVerificationEnabled = true })
	// not configured at all
	w := e.registerStart("new@example.com")
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	if testkit.Envelope(t, w).ErrorMsg != "smtp is not configured" {
		t.Fatalf("message: %s", w.Body)
	}
	// unparseable sender
	e.Settings.set(func(s *Settings) { s.SMTPHost, s.SMTPFrom = "127.0.0.1", "not an address <<" })
	w = e.registerStart("new@example.com")
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	if testkit.Envelope(t, w).ErrorMsg != "smtp from is invalid" {
		t.Fatalf("message: %s", w.Body)
	}
	// server without AUTH when credentials are configured
	port, _ := fakeSMTP(t, false)
	e.Settings.set(func(s *Settings) {
		s.SMTPHost, s.SMTPPort, s.SMTPFrom, s.SMTPUsername, s.SMTPPassword = "127.0.0.1", port, "noreply@example.com", "bot", "pw"
	})
	w = e.registerStart("new@example.com")
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	if testkit.Envelope(t, w).ErrorMsg != "smtp auth is not supported" {
		t.Fatalf("message: %s", w.Body)
	}
	// transport failure: generic, no detail
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	e.Settings.set(func(s *Settings) { s.SMTPPort = closedPort })
	w = e.registerStart("new@example.com")
	testkit.Expect(t, w, 400, "request.invalid")
	if strings.Contains(w.Body.String(), "refused") || strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Fatalf("transport detail leaked: %s", w.Body)
	}
	// a failed send leaves nothing pending, so the retry is immediate
	var pending int64
	e.DB.Model(&ContactVerification{}).Where("status = ?", "pending").Count(&pending)
	if pending != 0 {
		t.Fatalf("pending records: %d", pending)
	}
}

func TestVerificationMailHTMLEscapesAndLogo(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) { c.Server.PublicWebBaseURL = `https://web.example.com/"><script>` }))
	m := e.Svc.buildVerificationMail("a@b.co", PurposeRegister, "123456")
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "123456") || !strings.Contains(m.HTML, "/logo.svg") {
		t.Fatalf("html: %s", m.HTML)
	}
	e2 := newEnv(t)
	m = e2.Svc.buildVerificationMail("a@b.co", PurposeEmailChangeNew, "654321")
	if strings.Contains(m.HTML, "<img") || !strings.Contains(m.Text, "Email verification") || !strings.Contains(m.Text, "10 minutes") {
		t.Fatalf("mail: %+v", m)
	}
	for purpose, title := range map[string]string{PurposeRegister: "Create your account", PurposePasswordChange: "Confirm password change", PurposePasswordReset: "Reset your password",
		PurposeLogin: "Email verification", PurposeAccountDelete: "Confirm account deletion", PurposeEmailVerifyCurrent: "Email verification"} {
		if got := purposeTitle(purpose); got != title {
			t.Errorf("%s -> %s", purpose, got)
		}
	}
}

// ---- wiring --------------------------------------------------------------------------------------------------

type fakeBilling struct{}

func (fakeBilling) SubscriptionFor(_ context.Context, userID uint) (Subscription, bool) {
	id := uint(7)
	return Subscription{Tier: "pro", PlanID: &id, PlanName: "Pro Plan", Status: "active"}, true
}

func TestSubscriptionFieldsComeFromBilling(t *testing.T) {
	e := newEnv(t, withHooks(func(h *Hooks) { h.Subscriptions = fakeBilling{} }))
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	u := dataMap(t, e.request("GET", "/me", tok, nil))["user"].(map[string]any)
	if u["subscriptionTier"] != "pro" || u["subscriptionPlanID"] != float64(7) || u["subscriptionPlanName"] != "Pro Plan" || u["subscriptionStatus"] != "active" {
		t.Fatalf("subscription: %v", u)
	}
}

func TestMountWithoutCollaborators(t *testing.T) {
	env := testkit.New(t, []features.Feature{{Name: "auth", Migrations: Migrations, Mount: func(d deps.Deps) error { return Mount(d) }}})
	// the default settings allow username login and email registration
	w := env.Do("GET", "/api/v1/auth/login-options", nil, nil)
	testkit.Expect(t, w, 200, "")
	// the bootstrap hook created the administrator and logged its password once
	var n int64
	env.DB.Model(&User{}).Where("role = ?", RoleSuperAdmin).Count(&n)
	if n != 1 || strings.Count(env.Logs.String(), "bootstrap_admin_created") != 1 {
		t.Fatalf("admins=%d logs=%s", n, env.Logs)
	}
	// the session hook is registered: a platform token without a session row is rejected
	w = env.Do("GET", "/api/v1/me", env.Bearer(testkit.User(99)), nil)
	testkit.Expect(t, w, 401, "auth.session_invalid")
}

func TestStaticSettingsSource(t *testing.T) {
	s := DefaultSettings()
	s.LoginMaxFailures = 2
	if got := (StaticSettings(s)).AuthSettings(context.Background()); got.LoginMaxFailures != 2 {
		t.Fatalf("static settings: %+v", got)
	}
}

// ---- small helpers --------------------------------------------------------------------------------------------

func TestPickGitHubEmail(t *testing.T) {
	list := `[{"email":"a@x.io","primary":false,"verified":true},{"email":"P@x.io","primary":true,"verified":true},{"email":"u@x.io","primary":false,"verified":false}]`
	if got, ok := pickGitHubEmail([]byte(list), "a@x.io"); !ok || got != "a@x.io" {
		t.Errorf("profile email wins: %q %v", got, ok)
	}
	if got, ok := pickGitHubEmail([]byte(list), "u@x.io"); !ok || got != "p@x.io" {
		t.Errorf("unverified profile email falls back to primary: %q %v", got, ok)
	}
	if got, ok := pickGitHubEmail([]byte(`[{"email":"z@x.io","verified":true}]`), ""); !ok || got != "z@x.io" {
		t.Errorf("first verified: %q %v", got, ok)
	}
	if _, ok := pickGitHubEmail([]byte(`[{"email":"z@x.io","verified":false}]`), ""); ok {
		t.Errorf("nothing verified")
	}
	if _, ok := pickGitHubEmail([]byte(`not json`), ""); ok {
		t.Errorf("garbage")
	}
}

func TestBooleanClaimParsing(t *testing.T) {
	for in, want := range map[any]bool{true: true, false: false, "true": true, "TRUE": true, "1": true, "yes": true, "no": false, "0": false} {
		got, ok := asBool(in)
		if !ok || got != want {
			t.Errorf("asBool(%v)=%v,%v", in, got, ok)
		}
	}
	if _, ok := asBool("maybe"); ok {
		t.Errorf("unknown text is not a boolean")
	}
	if _, ok := asBool(nil); ok {
		t.Errorf("nil is not a boolean")
	}
}

func TestRetryAfterRounding(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if got := retryAfterSeconds(nil, now); got != 0 {
		t.Errorf("no unlock time: %d", got)
	}
	later := now.Add(1500 * 1e6)
	if got := retryAfterSeconds(&later, now); got != 2 {
		t.Errorf("1.5s rounds up to 2, got %d", got)
	}
	past := now.Add(-5 * 1e9)
	if got := retryAfterSeconds(&past, now); got != 0 {
		t.Errorf("past: %d", got)
	}
}

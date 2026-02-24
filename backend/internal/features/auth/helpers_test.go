package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func init() { bcryptCost = bcrypt.MinCost }

// fakeMailer records outgoing mail instead of speaking SMTP.
type fakeMailer struct {
	mu   sync.Mutex
	sent []Mail
	fail error
}

func (m *fakeMailer) Send(_ context.Context, msg Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

var codeRe = regexp.MustCompile(`Your verification code: (\d{6})`)

// lastCode returns the code of the newest mail to addr.
func (m *fakeMailer) lastCode(t *testing.T, addr string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if m.sent[i].To == addr {
			if sub := codeRe.FindStringSubmatch(m.sent[i].Text); sub != nil {
				return sub[1]
			}
		}
	}
	t.Fatalf("no mail with a code sent to %s", addr)
	return ""
}

// liveSettings is a SettingsSource tests can change while the app runs.
type liveSettings struct {
	mu sync.Mutex
	s  Settings
}

func (l *liveSettings) AuthSettings(context.Context) Settings {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.s
}

func (l *liveSettings) set(fn func(*Settings)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fn(&l.s)
}

type testEnv struct {
	*testkit.Env
	t        *testing.T
	Settings *liveSettings
	Mail     *fakeMailer
	Hooks    *Hooks
	Svc      *Service
}

type envOpt struct {
	cfg   []testkit.Option
	hooks func(*Hooks)
}

func withCfg(fn func(*config.Config)) func(*envOpt) {
	return func(o *envOpt) { o.cfg = append(o.cfg, testkit.WithConfig(fn)) }
}

func withHooks(fn func(*Hooks)) func(*envOpt) { return func(o *envOpt) { o.hooks = fn } }

func newEnv(t *testing.T, opts ...func(*envOpt)) *testEnv {
	t.Helper()
	var o envOpt
	for _, f := range opts {
		f(&o)
	}
	te := &testEnv{t: t, Settings: &liveSettings{s: DefaultSettings()}, Mail: &fakeMailer{}}
	te.Hooks = &Hooks{Settings: te.Settings, Mailer: te.Mail}
	if o.hooks != nil {
		o.hooks(te.Hooks)
	}
	feat := features.Feature{Name: "auth", Migrations: Migrations, Mount: func(d deps.Deps) error {
		te.Svc = newService(d, *te.Hooks)
		return MountWith(d, *te.Hooks)
	}}
	te.Env = testkit.New(t, []features.Feature{feat}, o.cfg...)
	return te
}

// request performs an API call. token may be empty; extra headers override.
func (e *testEnv) request(method, path, token string, body any, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	h := map[string]string{}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	for i := 0; i+1 < len(headers); i += 2 {
		h[headers[i]] = headers[i+1]
	}
	return e.Do(method, "/api/v1"+path, h, body)
}

type userSpec struct {
	Username      string
	Password      string // "" gives a user without password login
	Email         string
	EmailVerified bool
	Role          string
	Status        string
	Onboarded     bool
}

// seedUser inserts a user and credential directly.
func (e *testEnv) seedUser(s userSpec) *User {
	e.t.Helper()
	now := e.Clock.Now().UTC()
	if s.Role == "" {
		s.Role = RoleUser
	}
	if s.Status == "" {
		s.Status = StatusActive
	}
	u := User{
		PublicID: newPublicID(), Username: s.Username, DisplayName: normalizeDisplayName(s.Username),
		Email: s.Email, Role: s.Role, Status: s.Status, Timezone: "Etc/UTC", Locale: "en-US",
		EmailSource: EmailSourceUserSet,
	}
	if s.EmailVerified {
		u.EmailVerifiedAt = &now
	}
	if s.Onboarded {
		u.OnboardingCompletedAt = &now
	}
	if err := e.DB.Create(&u).Error; err != nil {
		e.t.Fatalf("seed user: %v", err)
	}
	hash, _ := hashPassword(s.Password)
	if s.Password == "" {
		hash, _ = hashPassword("unused-random-password")
	}
	cred := Credential{
		UserID: u.ID, PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: s.Password != "",
		PasswordOrigin: PasswordOriginUserSet, PasswordSetAt: &now, PasswordUpdatedAt: &now,
	}
	if err := e.DB.Create(&cred).Error; err != nil {
		e.t.Fatalf("seed credential: %v", err)
	}
	return &u
}

func (e *testEnv) reloadUser(id uint) *User {
	e.t.Helper()
	var u User
	if err := e.DB.First(&u, id).Error; err != nil {
		e.t.Fatalf("reload user %d: %v", id, err)
	}
	return &u
}

func (e *testEnv) credential(userID uint) *Credential {
	e.t.Helper()
	var c Credential
	if err := e.DB.Where("user_id = ?", userID).First(&c).Error; err != nil {
		e.t.Fatalf("credential %d: %v", userID, err)
	}
	return &c
}

// loginData is the success body of a sign-in.
type loginData struct {
	AccessToken             string         `json:"accessToken"`
	SessionID               string         `json:"sessionID"`
	ExpiresAt               time.Time      `json:"expiresAt"`
	RefreshExpiresAt        time.Time      `json:"refreshExpiresAt"`
	User                    map[string]any `json:"user"`
	TwoFactorRequired       bool           `json:"twoFactorRequired"`
	TwoFactorChallengeToken string         `json:"twoFactorChallengeToken"`
	VerificationMethods     []string       `json:"verificationMethods"`
	RefreshToken            string         `json:"refreshToken"`
}

func decodeLogin(t *testing.T, w *httptest.ResponseRecorder) loginData {
	t.Helper()
	var d loginData
	testkit.Decode(t, w, &d)
	return d
}

// signIn logs in and returns the parsed body, failing unless it is a 200.
func (e *testEnv) signIn(username, password string, headers ...string) (loginData, *httptest.ResponseRecorder) {
	e.t.Helper()
	w := e.request("POST", "/auth/login", "", map[string]string{"username": username, "password": password}, headers...)
	testkit.Expect(e.t, w, 200, "")
	return decodeLogin(e.t, w), w
}

// session seeds a user and logs them in, returning the user and access token.
func (e *testEnv) session(s userSpec) (*User, string) {
	e.t.Helper()
	if s.Password == "" {
		s.Password = "correct-horse-9"
	}
	u := e.seedUser(s)
	d, _ := e.signIn(s.Username, s.Password)
	return u, d.AccessToken
}

func refreshCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName {
			return c
		}
	}
	return nil
}

func cookieHeader(c *http.Cookie) string { return c.Name + "=" + c.Value }

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return testkit.Envelope(t, w).ErrorCode
}

func dataMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	testkit.Decode(t, w, &m)
	return m
}

func (e *testEnv) countEvents(eventType, result string) int64 {
	var n int64
	e.DB.Model(&AuthEvent{}).Where("event_type = ? AND result = ?", eventType, result).Count(&n)
	return n
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

type configT = config.Config

// ctxForTest returns a plain context for direct service calls.
func (s *Service) ctxForTest() context.Context { return context.Background() }

func localConfig() config.LocalConfig {
	return config.LocalConfig{Enabled: true, DataDir: "/tmp/riochat-local-test"}
}

func details(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	m, _ := testkit.Envelope(t, w).Details.(map[string]any)
	return m
}

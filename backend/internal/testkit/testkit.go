// Package testkit builds a complete in-process application for tests: SQLite
// in a temporary directory, the memory cache, a controllable clock, migrations
// and the feature areas under test mounted on the real router.
//
// A feature engineer tests an area in isolation with:
//
//	env := testkit.New(t, features.Feature{Name: "myarea", Migrations: Migrations, Mount: Mount})
//	w := env.Do("GET", "/api/v1/things", env.Bearer(testkit.User(7)), nil)
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/app"
	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

// Env is a running test application.
type Env struct {
	T      testing.TB
	App    *app.App
	Deps   deps.Deps
	DB     *gorm.DB
	Clock  *clock.Fake
	Logs   *bytes.Buffer
	Engine *gin.Engine
}

// Option customizes the configuration before the app is built.
type Option func(*config.Config)

// WithConfig applies fn to the configuration.
func WithConfig(fn func(*config.Config)) Option { return Option(fn) }

// New builds the application with the given feature areas. It fails the test on
// any startup error and shuts the app down when the test ends.
func New(t testing.TB, list []features.Feature, opts ...Option) *Env {
	t.Helper()
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.Database.Driver = config.DriverSQLite
	cfg.Database.SQLite.Path = filepath.Join(t.TempDir(), "test.db")
	cfg.Cache.Driver = config.CacheMemory
	cfg.Storage.Local.RootDir = filepath.Join(t.TempDir(), "storage")
	cfg.Server.FrontendDistDir = ""
	cfg.Security.JWTSecret = "testkit-jwt-secret-0123456789abcdef"
	cfg.Security.DataEncryptionKey = "testkit-data-encryption-key-0123456789abcdef"
	for _, o := range opts {
		o(&cfg)
	}

	clk := clock.NewFake(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC))
	logs := &bytes.Buffer{}
	a, err := app.Build(context.Background(), app.Options{
		Config: &cfg, Features: list, LogWriter: logs, Clock: clk,
	})
	if err != nil {
		t.Fatalf("testkit: build app: %v\nlogs:\n%s", err, logs)
	}
	t.Cleanup(a.Close)
	if err := a.Lifecycle.RunStartHooks(context.Background()); err != nil {
		t.Fatalf("testkit: start hooks: %v", err)
	}
	return &Env{T: t, App: a, Deps: a.Deps, DB: a.DB, Clock: clk, Logs: logs, Engine: a.Server.Engine}
}

// Claims describes a caller for Bearer.
type Claims = token.Claims

// User returns claims for a regular user.
func User(id uint) Claims { return Claims{UserID: id, Username: "user", Role: "user", SessionID: id} }

// Admin returns claims for an administrator.
func Admin(id uint) Claims {
	return Claims{UserID: id, Username: "admin", Role: "admin", SessionID: id}
}

// Bearer returns an Authorization header map for the claims.
func (e *Env) Bearer(c Claims) map[string]string {
	e.T.Helper()
	raw, err := e.Deps.Tokens.Issue(c, time.Hour)
	if err != nil {
		e.T.Fatalf("testkit: issue token: %v", err)
	}
	return map[string]string{"Authorization": "Bearer " + raw}
}

// Do performs a request against the router. body may be nil, a string, []byte,
// io.Reader or any value (encoded as JSON).
func (e *Env) Do(method, target string, headers map[string]string, body any) *httptest.ResponseRecorder {
	e.T.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	case []byte:
		rd = bytes.NewReader(b)
	case io.Reader:
		rd = b
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.T.Fatalf("testkit: encode body: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rd)
	req.RemoteAddr = "203.0.113.5:4321"
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.Engine.ServeHTTP(w, req)
	return w
}

// Envelope decodes a response body as the standard envelope.
func Envelope(t testing.TB, w *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("testkit: response is not an envelope (%d): %v\n%s", w.Code, err, w.Body.String())
	}
	return env
}

// Decode unmarshals the envelope's data field into out.
func Decode(t testing.TB, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("testkit: decode envelope: %v\n%s", err, w.Body.String())
	}
	if err := json.Unmarshal(raw.Data, out); err != nil {
		t.Fatalf("testkit: decode data: %v\n%s", err, raw.Data)
	}
}

// Expect fails the test unless the response has the wanted status and, when
// code is non-empty, the wanted error code.
func Expect(t testing.TB, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if code != "" {
		if got := Envelope(t, w).ErrorCode; got != code {
			t.Fatalf("errorCode %q, want %q: %s", got, code, w.Body.String())
		}
	}
}

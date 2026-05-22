package platformllm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/extract"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func feature() []features.Feature {
	return []features.Feature{{Name: "platform_llm", Migrations: Migrations, Mount: Mount}}
}

func TestMountPublishesPlatform(t *testing.T) {
	if Migrations() != nil {
		t.Fatal("the area owns no tables")
	}
	env := testkit.New(t, feature(), testkit.WithConfig(func(c *config.Config) {
		c.Storage.Local.RootDir = filepath.Join(t.TempDir(), "objects")
	}))
	p, ok := Current()
	if !ok || p.LLM == nil || p.HTTP == nil || p.Embeddings == nil || p.Media == nil || p.Storage == nil || p.Jobs == nil || p.Tracer == nil {
		t.Fatalf("%+v", p)
	}
	if p.Trusted.Enforced() || p.Strict.Enforced() {
		t.Fatal("dev environments do not enforce the SSRF policy")
	}
	// Object storage follows the live configuration.
	ctx := context.Background()
	if _, err := p.Storage.Put(ctx, "a/b.txt", strings.NewReader("hi"), 2, "text/plain"); err != nil {
		t.Fatal(err)
	}
	root := env.Deps.Config.Load().Storage.Local.RootDir
	if b, err := os.ReadFile(filepath.Join(root, "a", "b.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("%v", err)
	}
	other := t.TempDir()
	env.Deps.Config.Update(func(c *config.Config) { c.Storage.Local.RootDir = other })
	if _, _, err := p.Storage.Open(ctx, "a/b.txt"); err == nil {
		t.Fatal("after a configuration change the new root is used")
	}
	if !strings.Contains(env.Logs.String(), "outbound_proxy_") {
		t.Fatalf("startup logs the proxy state:\n%s", env.Logs)
	}
}

func TestEndToEndThroughThePlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c1","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	holder := config.NewHolder(&config.Config{})
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	holder.Store(&cfg)
	p, err := New(Params{Config: holder, Getenv: func(string) string { return "" }, Proxy: outbound.NoProxy})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.LLM.Generate(context.Background(), llm.Route{Protocol: "openai_chat_completions", BaseURL: srv.URL, Model: "m"},
		&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "ping"}}})
	if err != nil || res.Text != "pong" {
		t.Fatalf("%v", err)
	}
	e, err := p.Extractor(context.Background(), "builtin", extract.Config{})
	if err != nil || e.Name() != "builtin" {
		t.Fatalf("%v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProductionEnforcesPolicyAndRejectsLoopbackModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cfg := config.Default()
	cfg.App.Env = config.EnvProd
	cfg.Security.SSRFProtectionEnabled = true
	cfg.Security.SSRFAllowedHosts = "models.internal.example"
	cfg.Security.SSRFAllowedCIDRs = "10.0.0.0/8"
	p, err := New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }, Proxy: outbound.NoProxy})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Trusted.Enforced() || !p.Strict.Enforced() || len(p.Trusted.Hosts()) != 1 || len(p.Strict.Hosts()) != 0 {
		t.Fatal("trusted carries the allowlist, strict does not")
	}
	if err := p.Trusted.ValidateURL("http://10.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := p.Strict.ValidateURL("http://10.2.3.4"); err == nil {
		t.Fatal("strict has no allowlist")
	}
	// A user-supplied endpoint on loopback never leaves the process.
	_, err = p.LLM.Generate(context.Background(), llm.Route{Protocol: "openai_chat_completions", BaseURL: srv.URL, Model: "m", Untrusted: true},
		&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("blocked")
	}
	// An administrator-configured endpoint is trusted exactly.
	if _, err := p.LLM.Generate(context.Background(), llm.Route{Protocol: "openai_chat_completions", BaseURL: srv.URL, Model: "m"},
		&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil && strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("the configured endpoint is trusted: %v", err)
	}
}

func TestInvalidAllowlistFallsBackToStrict(t *testing.T) {
	cfg := config.Default()
	cfg.App.Env = config.EnvProd
	cfg.Security.SSRFProtectionEnabled = true
	cfg.Security.SSRFAllowedHosts = "bad host!"
	p, err := New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if p.Trusted != p.Strict || !p.Strict.Enforced() {
		t.Fatal("an invalid allowlist degrades to the strict policy")
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestTracingConfiguration(t *testing.T) {
	enabled := true
	cfg := config.Default()
	cfg.Tracing.Enabled = &enabled
	if _, err := New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }}); err == nil || !strings.Contains(err.Error(), "otel exporter endpoint is required when tracing is enabled") {
		t.Fatalf("enabled without an endpoint fails startup: %v", err)
	}
	if env := func() error {
		_, err := New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }})
		return err
	}(); env == nil {
		t.Fatal("still failing")
	}
	var buf lockedBuf
	cfg.Tracing.Endpoint = "http://127.0.0.1:1"
	cfg.Tracing.Protocol = "grpc"
	p, err := New(Params{Config: config.NewHolder(&cfg), Logger: logx.New(&buf, logx.LevelDebug), Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "otel_grpc_exporter_unavailable") {
		t.Fatalf("grpc is exported over HTTP and says so: %s", buf.String())
	}
	p.Shutdown(context.Background())
}

func TestProxyEnvironmentIsReported(t *testing.T) {
	var buf lockedBuf
	cfg := config.Default()
	_, err := New(Params{Config: config.NewHolder(&cfg), Logger: logx.New(&buf, logx.LevelDebug),
		Getenv: func(k string) string {
			if k == "HTTPS_PROXY" {
				return "http://user:secret@proxy.example:3128"
			}
			return ""
		}})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "outbound_proxy_enabled") || !strings.Contains(out, "https://") && !strings.Contains(out, "http://proxy.example:3128") || strings.Contains(out, "secret") {
		t.Fatalf("%s", out)
	}
}

func TestGeoIPIsBuiltFromConfiguration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"country_code":"NL","city":"Amsterdam","region":"NH","timezone":{"id":"Europe/Amsterdam"}}`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.GeoIP.Provider = "ipwhois"
	cfg.GeoIP.BaseURL = srv.URL
	p, err := New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }, Proxy: outbound.NoProxy})
	if err != nil || p.GeoIP == nil || p.Docker == nil {
		t.Fatalf("%v", err)
	}
	loc, err := p.GeoIP.Lookup(context.Background(), "8.8.8.8")
	if err != nil || loc.CountryCode != "NL" || loc.City != "Amsterdam" {
		t.Fatalf("%v %+v", err, loc)
	}

	cfg.GeoIP.Provider = "none"
	p, _ = New(Params{Config: config.NewHolder(&cfg), Getenv: func(string) string { return "" }})
	if p.GeoIP != nil {
		t.Fatal("a disabled provider builds no resolver")
	}
	var buf lockedBuf
	cfg.GeoIP.Provider = "bogus"
	p, err = New(Params{Config: config.NewHolder(&cfg), Logger: logx.New(&buf, logx.LevelDebug), Getenv: func(string) string { return "" }})
	if err != nil || p.GeoIP != nil || !strings.Contains(buf.String(), "geoip_disabled") {
		t.Fatalf("GeoIP problems never block startup: %v %s", err, buf.String())
	}
}

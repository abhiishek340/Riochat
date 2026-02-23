package outbound

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, fmt.Errorf("no such host %s", host)
	}
	var out []netip.Addr
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func mustPolicy(t *testing.T, enforce bool, hosts, cidrs []string) *Policy {
	t.Helper()
	p, err := NewPolicy(enforce, hosts, cidrs)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnforcedPolicyRejectsInternalTargets(t *testing.T) {
	p := mustPolicy(t, true, nil, nil)
	for _, u := range []string{
		"http://127.0.0.1", "http://localhost", "http://10.1.2.3", "http://169.254.169.254",
		"http://[fe80::1]", "http://metadata.google.internal", "http://foo.localhost/x",
		"http://[::1]:8080", "http://127.1", "http://0x7f000001", "http://2130706433",
		"http://[::ffff:127.0.0.1]", "http://100.100.100.200", "http://[fd00:ec2::254]",
		"http://0.0.0.0", "http://224.0.0.1",
	} {
		err := p.ValidateURL(u)
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: want ErrBlocked, got %v", u, err)
		}
	}
	for _, u := range []string{"https://api.openai.com/v1", "http://example.com:8080", "https://8.8.8.8"} {
		if err := p.ValidateURL(u); err != nil {
			t.Errorf("%s: unexpected %v", u, err)
		}
	}
}

func TestSyntaxRulesAlwaysApply(t *testing.T) {
	off := &Policy{}
	for _, u := range []string{"ftp://example.com", "http://user:pw@example.com", "example.com", "http://", "://x"} {
		if err := off.ValidateURL(u); err == nil {
			t.Errorf("%q should be rejected even with enforcement off", u)
		}
	}
	// With enforcement off, private targets are the operator's business.
	if err := off.ValidateURL("http://127.0.0.1:11434"); err != nil {
		t.Fatal(err)
	}
}

func TestAllowlistedCIDRAndHost(t *testing.T) {
	p := mustPolicy(t, true, []string{"ollama.localhost", "Intranet.Example"}, []string{"10.0.0.0/8", "10.0.0.0/8"})
	if err := p.ValidateURL("http://10.1.2.3"); err != nil {
		t.Fatalf("allowlisted cidr: %v", err)
	}
	if err := p.ValidateURL("http://169.254.169.254"); err == nil {
		t.Fatal("metadata address must stay blocked")
	}
	if err := p.ValidateURL("http://ollama.localhost:11434"); err != nil {
		t.Fatalf("allowlisted local name: %v", err)
	}
	if err := p.ValidateURL("http://11.0.0.1"); err != nil {
		t.Fatalf("public ip: %v", err)
	}
	if err := p.ValidateURL("http://172.16.0.1"); err == nil {
		t.Fatal("private ip outside the allowlist must be blocked")
	}
	if got := p.Hosts(); len(got) != 2 || got[0] != "intranet.example" {
		t.Fatalf("hosts: %v", got)
	}
}

func TestAllowlistValidation(t *testing.T) {
	bad := [][]string{{"1.2.3.4"}, {"host:80"}, {"a/b"}, {"metadata.google.internal"}, {strings.Repeat("a", 64) + ".com"}, {"a b"}, {"a..b"}}
	for _, h := range bad {
		if _, err := NewPolicy(true, h, nil); err == nil {
			t.Errorf("host %v should be invalid", h)
		}
	}
	for _, c := range []string{"nonsense", "::ffff:10.0.0.0/104", "10.0.0.0/33"} {
		if _, err := NewPolicy(true, nil, []string{c}); err == nil {
			t.Errorf("cidr %q should be invalid", c)
		}
	}
	trusted, strict := FromConfig(true, "bad host", "")
	if trusted != strict {
		t.Fatal("invalid allowlist must fall back to the strict policy")
	}
}

func TestPublicOnlyPolicy(t *testing.T) {
	p := PublicOnly()
	for _, u := range []string{"http://100.64.0.1", "http://[2002::1]", "http://198.18.0.5", "http://192.0.2.1", "http://240.0.0.1", "http://10.0.0.1", "http://127.0.0.1", "http://[64:ff9b::1]"} {
		if err := p.ValidateURL(u); err == nil {
			t.Errorf("%s should be rejected in public-only mode", u)
		}
	}
	if err := p.ValidateURL("https://1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	// A host name that resolves into the fake-IP pool is acceptable.
	if err := p.CheckResolved("x.example", []netip.Addr{netip.MustParseAddr("198.18.0.5")}); err != nil {
		t.Fatalf("fake-ip resolution: %v", err)
	}
	if err := p.CheckResolved("x.example", []netip.Addr{netip.MustParseAddr("100.64.0.1")}); err == nil {
		t.Fatal("cgnat resolution must be rejected")
	}
	// Trust never relaxes public-only.
	if err := p.Trust("http://10.0.0.1").ValidateURL("http://10.0.0.1"); err == nil {
		t.Fatal("public-only policy must not be relaxable")
	}
}

func TestTrustDerivedPolicy(t *testing.T) {
	base := mustPolicy(t, true, nil, nil)
	tp := base.Trust("http://10.9.9.9:8080/v1")
	if err := tp.ValidateURL("http://10.9.9.9:8080/v1/chat"); err != nil {
		t.Fatal(err)
	}
	if err := tp.ValidateURL("http://10.9.9.8"); err == nil {
		t.Fatal("trust must be exact")
	}
	if err := base.ValidateURL("http://10.9.9.9"); err == nil {
		t.Fatal("derivation must not mutate the base policy")
	}
	if err := base.Trust("http://169.254.169.254").ValidateURL("http://169.254.169.254"); err == nil {
		t.Fatal("link-local can never be trusted")
	}
	if err := base.Trust("http://metadata.google.internal").ValidateURL("http://metadata.google.internal"); err == nil {
		t.Fatal("metadata host can never be trusted")
	}
	if err := base.Trust("http://llm.localhost:9").ValidateURL("http://llm.localhost:9"); err != nil {
		t.Fatalf("trusted local name: %v", err)
	}
}

func TestOrigin(t *testing.T) {
	cases := map[string]string{
		"HTTPS://API.Example.com:443/v1?x=1#f": "https://api.example.com",
		"http://example.com:80":                "http://example.com",
		"http://example.com:8080/a":            "http://example.com:8080",
		"http://[::1]:3000":                    "http://[::1]:3000",
	}
	for in, want := range cases {
		got, err := Origin(in)
		if err != nil || got != want {
			t.Errorf("Origin(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	if _, err := Origin("nonsense"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRedactHeadersJSON(t *testing.T) {
	in := `{"Authorization":"Bearer x","X_Api_Key":"k","Cookie":"a","Set-Cookie":"b","X-Token":"t","My-Secret":"s","X-Trace":"keep","apikey":"z"}`
	out := RedactHeadersJSON(in)
	for _, k := range []string{"Authorization", "X_Api_Key", "Cookie", "Set-Cookie", "X-Token", "My-Secret", "apikey"} {
		if !strings.Contains(out, `"`+k+`":"********"`) {
			t.Errorf("%s not masked in %s", k, out)
		}
	}
	if !strings.Contains(out, `"X-Trace":"keep"`) {
		t.Errorf("benign header changed: %s", out)
	}
	if RedactHeadersJSON("{bad") != "{}" || RedactHeadersJSON("") != "" {
		t.Fatal("invalid JSON yields {} and empty stays empty")
	}
}

func TestDialRejectsRebindingAndAllowsListedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	res := fakeResolver{"evil.example": {"10.0.0.5"}, "ollama.internal": {"127.0.0.1"}, "mixed.example": {"8.8.8.8", "127.0.0.1"}}

	pool := NewPool(PoolOptions{Trusted: mustPolicy(t, true, []string{"ollama.internal"}, nil), Resolver: res, Proxy: NoProxy})
	do := func(host string) error {
		req, _ := http.NewRequest("GET", "http://"+host+":"+u.Port()+"/", nil)
		resp, err := pool.Do(context.Background(), Endpoint{}, req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := do("evil.example"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebinding to a private address must be blocked at dial time, got %v", err)
	}
	if err := do("mixed.example"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("any private answer taints the lookup, got %v", err)
	}
	if err := do("ollama.internal"); err != nil {
		t.Fatalf("allowlisted name resolving privately: %v", err)
	}
}

func TestTrustedEndpointOwnPrivateHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer srv.Close()
	pool := NewPool(PoolOptions{Trusted: mustPolicy(t, true, nil, nil), Proxy: NoProxy})
	req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
	// Without a configured endpoint the strict base policy blocks loopback.
	if _, err := pool.Do(context.Background(), Endpoint{}, req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	resp, err := pool.Do(context.Background(), Endpoint{URL: srv.URL}, req)
	if err != nil {
		t.Fatalf("exact-origin trust: %v", err)
	}
	resp.Body.Close()
}

func TestOriginMismatchRefused(t *testing.T) {
	pool := NewPool(PoolOptions{Proxy: NoProxy})
	req, _ := http.NewRequest("GET", "http://other.example/x", nil)
	_, err := pool.Do(context.Background(), Endpoint{URL: "http://configured.example"}, req)
	if !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "differs from the configured origin") {
		t.Fatalf("message: %v", err)
	}
}

func TestPoolEvictsLeastRecentlyUsed(t *testing.T) {
	pool := NewPool(PoolOptions{Proxy: NoProxy})
	ep := func(i int) Endpoint { return Endpoint{URL: fmt.Sprintf("http://h%d.example", i)} }
	for i := 0; i < 64; i++ {
		if _, err := pool.Client(ep(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Client(ep(0)); err != nil { // touch the oldest
		t.Fatal(err)
	}
	if _, err := pool.Client(ep(64)); err != nil { // 65th distinct origin
		t.Fatal(err)
	}
	if pool.Len() != 64 {
		t.Fatalf("len=%d", pool.Len())
	}
	if !pool.Has(ep(0)) || pool.Has(ep(1)) {
		t.Fatal("the least recently used client (h1) should have been evicted")
	}
	// Same origin, different variant: separate clients.
	a, _ := pool.Client(Endpoint{URL: "http://v.example", ConnectTimeout: time.Second})
	b, _ := pool.Client(Endpoint{URL: "http://v.example", ConnectTimeout: 2 * time.Second})
	if a == b {
		t.Fatal("variants must not share a client")
	}
}

func TestRedirects(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "target") }))
	defer target.Close()
	var hops int
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			fmt.Fprint(w, "final")
		case "/cross":
			http.Redirect(w, r, target.URL+"/", http.StatusFound)
		case "/loop":
			hops++
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer origin.Close()
	pool := NewPool(PoolOptions{Trusted: mustPolicy(t, true, nil, nil), Proxy: NoProxy})
	get := func(ep Endpoint, path string) error {
		req, _ := http.NewRequest("GET", origin.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := pool.Do(context.Background(), ep, req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	ep := Endpoint{URL: origin.URL, Integration: "model endpoint"}
	if err := get(ep, "/same"); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if err := get(ep, "/cross"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("cross-origin redirect to a private host must be refused, got %v", err)
	}
	err := get(ep, "/loop")
	if err == nil || !strings.Contains(err.Error(), "model endpoint") || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("loop error: %v", err)
	}
	ep.Redirects = RedirectNone
	if err := get(ep, "/same"); !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("RedirectNone: %v", err)
	}
	ep.Redirects = RedirectSameOrigin
	if err := get(ep, "/same"); err != nil {
		t.Fatalf("same-origin allowed: %v", err)
	}
}

func TestUserSuppliedEndpointsRefuseRedirects(t *testing.T) {
	pool := NewPool(PoolOptions{Proxy: NoProxy})
	check := pool.redirectPolicy(Endpoint{URL: "https://user.example", Kind: KindUserSupplied})
	req, _ := http.NewRequest("GET", "https://user.example/next", nil)
	via, _ := http.NewRequest("GET", "https://user.example/", nil)
	err := check(req, []*http.Request{via})
	if !errors.Is(err, ErrRedirectRefused) || !strings.Contains(err.Error(), "user-configured") {
		t.Fatalf("got %v", err)
	}
	// And user endpoints go through the public-only policy at request time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	r2, _ := http.NewRequest("GET", srv.URL, nil)
	if _, err := pool.Do(context.Background(), Endpoint{URL: srv.URL, Kind: KindUserSupplied}, r2); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback user endpoint: %v", err)
	}
}

func TestCrossOriginRedirectDropsCredentials(t *testing.T) {
	pool := NewPool(PoolOptions{Proxy: NoProxy})
	check := pool.redirectPolicy(Endpoint{URL: "https://a.example"})
	req, _ := http.NewRequest("GET", "https://b.example/x", nil)
	req.Header.Set("X-Goog-Api-Key", "k")
	req.Header.Set("Authorization", "Bearer t")
	via, _ := http.NewRequest("GET", "https://a.example/", nil)
	if err := check(req, []*http.Request{via}); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Goog-Api-Key") != "" || req.Header.Get("Authorization") != "" {
		t.Fatal("credentials must be dropped on cross-origin hops")
	}
}

func TestProxyDestinationValidation(t *testing.T) {
	proxyURL, _ := url.Parse("http://proxy.internal:3128")
	res := fakeResolver{"evil.example": {"10.0.0.9"}, "good.example": {"93.184.216.34"}}
	pool := NewPool(PoolOptions{
		Trusted:  mustPolicy(t, true, nil, nil),
		Resolver: res,
		Proxy:    func(*http.Request) (*url.URL, error) { return proxyURL, nil },
	})
	pf := pool.proxyFunc(pool.opts.Trusted)
	mk := func(raw string) *http.Request { r, _ := http.NewRequest("GET", raw, nil); return r }
	if _, err := pf(mk("http://evil.example/")); !errors.Is(err, ErrBlocked) {
		t.Fatalf("pre-resolved private destination: %v", err)
	}
	if _, err := pf(mk("http://0x7f000001/")); !errors.Is(err, ErrBlocked) {
		t.Fatalf("lenient numeric host: %v", err)
	}
	if u, err := pf(mk("http://good.example/")); err != nil || u == nil {
		t.Fatalf("public destination: %v", err)
	}
	if u, err := pf(mk("http://unresolvable.example/")); err != nil || u == nil {
		t.Fatalf("local resolution failure must not block: %v", err)
	}
	if _, ok := pool.proxies.Load("proxy.internal:3128"); !ok {
		t.Fatal("the configured proxy address must be dialable")
	}
}

func TestReportProxy(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	r := ReportProxy(env(map[string]string{"HTTP_PROXY": "http://user:pw@proxy:3128/path", "NO_PROXY": "localhost"}), true)
	if r.Event != "outbound_proxy_enabled" || r.Fields["http_proxy"] != "http://proxy:3128" || r.Fields["ssrf_protection"] != "true" || r.Fields["no_proxy"] != "localhost" {
		t.Fatalf("%+v", r)
	}
	if r := ReportProxy(env(nil), false); r.Event != "outbound_proxy_disabled" || r.Warn {
		t.Fatalf("%+v", r)
	}
	if r := ReportProxy(env(map[string]string{"ALL_PROXY": "socks5://x"}), false); r.Event != "outbound_proxy_ignored" || !r.Warn {
		t.Fatalf("%+v", r)
	}
}

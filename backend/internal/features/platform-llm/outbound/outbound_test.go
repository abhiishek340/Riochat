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

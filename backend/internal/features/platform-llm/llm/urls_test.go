package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildURLVersionHandling(t *testing.T) {
	cases := []struct{ base, ver, ep, want string }{
		{"https://api.example.com/v1", "v1", "/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com", "v1", "/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com/", "v1", "/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"https://x/openai/v1beta", "v1", "/chat/completions", "https://x/openai/v1beta/chat/completions"},
		{"https://x/openai/v1beta2", "v1", "/models", "https://x/openai/v1beta2/models"},
		{"https://x/api/v2preview", "v1", "/models", "https://x/api/v2preview/models"},
		{"https://x/api/v3alpha1/", "v1", "/models", "https://x/api/v3alpha1/models"},
		{"https://x/openai", "v1", "/models", "https://x/openai/v1/models"},
		{"https://x/v", "v1", "/models", "https://x/v/v1/models"},
		{"https://x/version", "v1", "/models", "https://x/version/v1/models"},
	}
	for _, c := range cases {
		got, err := buildURL(c.base, c.ver, c.ep)
		if err != nil || got != c.want {
			t.Errorf("buildURL(%q): got %q,%v want %q", c.base, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "   ", "not a url", "ftp"} {
		if _, err := buildURL(bad, "v1", "/x"); !errors.Is(err, ErrInvalidBaseURL) || err.Error() != "invalid base url" {
			t.Errorf("base %q: %v", bad, err)
		}
	}
}

func TestGoogleURLRules(t *testing.T) {
	if got := googleBase("https://proxy.example/v1"); got != "https://proxy.example" {
		t.Fatal(got)
	}
	if got := googleBase(""); got != "https://generativelanguage.googleapis.com" {
		t.Fatal(got)
	}
	u, _ := buildURL(googleBase("https://proxy.example/v1"), "v1beta", "/"+googleModelPath("gemini-x")+":generateContent")
	if u != "https://proxy.example/v1beta/models/gemini-x:generateContent" {
		t.Fatal(u)
	}
	if googleModelPath("") != "models/gemini-2.0-flash" || googleModelPath("models/foo") != "models/foo" || googleModelPath("tunedModels/a") != "tunedModels/a" {
		t.Fatal("model path rules")
	}
}

func TestHeaderTemplates(t *testing.T) {
	vars := map[string]string{"RIOCHAT_CONVERSATION_ID": "conv-1", "RIOCHAT_SESSION_ID": "", "RIOCHAT_REQUEST_ID": "req-9"}
	got := customHeaders(`{"X-Trace":"${RIOCHAT_CONVERSATION_ID}","X-Sess":"${RIOCHAT_SESSION_ID}","X-Mixed":"a-${RIOCHAT_REQUEST_ID}-b","":"x","Blank":"  ","X-Lit":"${UNKNOWN}"}`, vars)
	m := map[string]string{}
	for _, p := range got {
		m[p.Name] = p.Value
	}
	if m["X-Trace"] != "conv-1" || m["X-Mixed"] != "a-req-9-b" || m["X-Lit"] != "${UNKNOWN}" {
		t.Fatalf("%v", m)
	}
	if _, ok := m["X-Sess"]; ok {
		t.Fatal("a header whose template cannot be filled must be omitted")
	}
	if _, ok := m["Blank"]; ok || len(m) != 3 {
		t.Fatalf("blank names/values are skipped: %v", m)
	}
	if customHeaders("{not json", vars) != nil {
		t.Fatal("invalid JSON is ignored")
	}
}

func TestUpstreamRequestIDIsFresh(t *testing.T) {
	a := headerVars(&Request{})["RIOCHAT_UPSTREAM_REQUEST_ID"]
	b := headerVars(&Request{})["RIOCHAT_UPSTREAM_REQUEST_ID"]
	if a == "" || a == b || !strings.Contains(a, "-") {
		t.Fatalf("%q %q", a, b)
	}
}

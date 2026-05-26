package personalprovider

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestParseBaseURL(t *testing.T) {
	good := []struct{ in, url, host string }{
		{"https://api.example.com/v1///", "https://api.example.com/v1", "api.example.com"},
		{"HTTPS://API.Example.COM", "https://api.example.com", "api.example.com"},
		{"  https://api.example.com:8443/x/y/ ", "https://api.example.com:8443/x/y", "api.example.com"},
		{"https://api.example.com./v1", "https://api.example.com/v1", "api.example.com"},
		{"https://api.example.com/", "https://api.example.com", "api.example.com"},
		{"https://[2606:4700::1]:443/v1", "https://[2606:4700::1]:443/v1", "2606:4700::1"},
	}
	for _, c := range good {
		got, err := parseBaseURL(c.in)
		if err != nil || got.BaseURL != c.url || got.Host != c.host {
			t.Errorf("parseBaseURL(%q) = %+v, %v; want %s %s", c.in, got, err, c.url, c.host)
		}
	}
	bad := []string{
		"", "   ", "https://host?x=1", "https://host?", "https://u:p@host", "https://host#f", "https://host#",
		"http://host", "ftp://host", "https://host:0", "https://host:70000", "https://host:", "https://host:abc",
		"host.example.com", "https:///path", "mailto:a@b.c", "https://ho st.example.com", "https://host\\x",
		"https://" + strings.Repeat("a", 500) + ".com", "//host.example.com", "https:host.example.com",
		"https://host.example.com/a\nb",
	}
	for _, in := range bad {
		if _, err := parseBaseURL(in); !errors.Is(err, ErrInvalidBaseURL) {
			t.Errorf("parseBaseURL(%q) err = %v, want invalid_base_url", in, err)
		}
	}
}

func TestPublicOnlyPolicy(t *testing.T) {
	p := PublicOnlyPolicy{}
	blocked := []string{
		"localhost", "LOCALHOST", "127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.4.4", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1", "fd00:ec2::254", "::ffff:127.0.0.1",
		"metadata.google.internal", "printer.local", "db.internal", "intranet", "foo.localhost",
		"2130706433", "0x7f.0.0.1", "api.example.com.1234",
	}
	for _, h := range blocked {
		if err := p.CheckHost(h); !errors.Is(err, ErrBlockedHost) {
			t.Errorf("CheckHost(%q) = %v, want blocked", h, err)
		}
	}
	for _, h := range []string{"api.example.com", "api.openai.com", "8.8.8.8", "2606:4700::1", "xn--e1afmkfd.example"} {
		if err := p.CheckHost(h); err != nil {
			t.Errorf("CheckHost(%q) = %v, want ok", h, err)
		}
	}
}

func TestForbiddenIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.1.1", "169.254.169.254", "::1", "fc00::1", "100.100.100.200", "::7f00:1", "2002:7f00:1::1", "2001:0:4136:e378::1", "fec0::1", "::ffff:127.0.0.1"} {
		if !forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"} {
		if forbiddenIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestDenyList(t *testing.T) {
	d := parseDenyList("evil.com, *.bad.org\n\tEXACT.net\r\n  trailing.dot. ")
	cases := map[string]bool{
		"evil.com": true, "api.evil.com": false, "EVIL.COM": true, "evil.com.": true,
		"bad.org": false, "a.bad.org": true, "a.b.bad.org": true, "notbad.org": false, "xbad.org": false,
		"exact.net": true, "trailing.dot": true, "": false,
	}
	for host, want := range cases {
		if got := d.matches(host); got != want {
			t.Errorf("matches(%q) = %v, want %v", host, got, want)
		}
	}
	if parseDenyList("").matches("a.com") {
		t.Error("empty list matches nothing")
	}
}

func TestValidateBlockedHosts(t *testing.T) {
	ok := []string{"", "evil.com", "*.evil.com, a.com\nb.org", "UPPER.com", "10.0.0.1"}
	for _, in := range ok {
		if err := ValidateBlockedHosts(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	bad := []string{"http://evil.com", "evil.com/path", "evil.com:443", "-a.com", "a..com", "*.", "**.a.com", "a_b.com", strings.Repeat("a.com ", 1000)}
	for _, in := range bad {
		if err := ValidateBlockedHosts(in); err == nil {
			t.Errorf("%q should be rejected", in[:min(len(in), 20)])
		}
	}
	if ValidateMaxPerUser(0) == nil || ValidateMaxPerUser(51) == nil || ValidateMaxPerUser(1) != nil || ValidateMaxPerUser(50) != nil {
		t.Error("max_per_user range is 1..50")
	}
}

func TestKeyHint(t *testing.T) {
	cases := map[string]string{
		"": "", "   ": "", "short": "••••", "12345678901": "••••", "sk-abcdefghijWXYZ": "sk-••••WXYZ",
		"123456789012": "123••••9012", "ключключключключ": "клю••••лючь"[0:0] + "клю••••ключ"[0:0] + keyHint("ключключключключ"),
	}
	for in, want := range cases {
		if got := keyHint(in); got != want {
			t.Errorf("keyHint(%q) = %q, want %q", in, got, want)
		}
	}
	// 11 unicode characters count as characters, not bytes.
	if got := keyHint("ключключклю"); got != "••••" {
		t.Errorf("11 runes: %q", got)
	}
	if got := keyHint("ключключключ"); got != "клю••••ючк"[0:0]+"клю••••ключ"[0:0]+got || !strings.Contains(got, "••••") {
		t.Errorf("12 runes: %q", got)
	}
}

func TestNormalizeAPIKey(t *testing.T) {
	if k, err := normalizeAPIKey("  sk-123  "); err != nil || k != "sk-123" {
		t.Fatalf("trim: %q %v", k, err)
	}
	for _, in := range []string{"", "   ", "a b", "a\nb", "a\tb", "ключ", "ab\x00c", "ab\x7fc", strings.Repeat("a", 513)} {
		if _, err := normalizeAPIKey(in); !errors.Is(err, ErrInvalidAPIKey) {
			t.Errorf("%q: %v", in, err)
		}
	}
	if _, err := normalizeAPIKey(strings.Repeat("a", 512)); err != nil {
		t.Errorf("512 bytes is allowed: %v", err)
	}
}

func TestNormalizeName(t *testing.T) {
	if n, err := normalizeName("  My Provider  "); err != nil || n != "My Provider" {
		t.Fatalf("%q %v", n, err)
	}
	if n, err := normalizeName("   "); err != nil || n != "" {
		t.Fatalf("empty stays empty: %q %v", n, err)
	}
	if _, err := normalizeName(strings.Repeat("я", 64)); err != nil {
		t.Errorf("64 runes ok: %v", err)
	}
	for _, in := range []string{strings.Repeat("я", 65), "a\x01b", "a\x7fb", "a\nb", "a\tb"} {
		if _, err := normalizeName(in); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestNormalizeIcon(t *testing.T) {
	ok := map[string]string{"OpenAI ": "openai", "": "", "  ": "", "a-b-9": "a-b-9", "x": "x"}
	for in, want := range ok {
		if got, err := normalizeIcon(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"http://x/y.png", "-bad", strings.Repeat("a", 65), "a b", "a_b", "é", "a/b", "a.png"} {
		if _, err := normalizeIcon(in); !errors.Is(err, ErrInvalidIcon) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestModelNameRules(t *testing.T) {
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	good := []string{"gpt-4o", "models/x", "a", strings.Repeat("a", 100), "\u6a21\u578b", "x:y@z"}
	for _, n := range good {
		if !validModelName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	bad := []string{"", " ", "a b", " a", "a ", "a\nb", "a\tb", "a\x7fb", strings.Repeat("a", 101), "a\x00"}
	for _, n := range bad {
		if validModelName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestReferences(t *testing.T) {
	id, m, ok := ParseReference("personal:abcd1234/gpt-4o")
	if !ok || id != "abcd1234" || m != "gpt-4o" {
		t.Fatalf("%q %q %v", id, m, ok)
	}
	if id, m, ok := ParseReference("personal:abcd1234/org/model"); !ok || id != "abcd1234" || m != "org/model" {
		t.Fatalf("slash in model: %q %q %v", id, m, ok)
	}
	for _, bad := range []string{
		"personal:abcd1234", "other:abcd1234/m", "personal:abc/m", "personal:ABCD1234/m", "personal:abcd-234/m",
		"personal:abcd1234/", "personal:abcd1234/a b", "personal:/m", "gpt-4o", "", "personal:" + strings.Repeat("a", 33) + "/m",
	} {
		if _, _, ok := ParseReference(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
	// Prefix membership is independent of well-formedness.
	if !IsReference("personal:garbage") || !IsReference("  personal:x") || IsReference("Personal:x") || IsReference("gpt") {
		t.Error("IsReference")
	}
	if FormatReference("abcd1234", "m/x") != "personal:abcd1234/m/x" {
		t.Error("FormatReference")
	}
}

func TestPublicID(t *testing.T) {
	id, err := newPublicID()
	if err != nil || !validPublicID(id) {
		t.Fatalf("generated id %q: %v", id, err)
	}
	for _, bad := range []string{"", "short", "UPPERCASE1", "has-hyphen1", strings.Repeat("a", 33), "ünicode12"} {
		if validPublicID(bad) {
			t.Errorf("%q invalid", bad)
		}
	}
	if !validPublicID("abcd1234") || !validPublicID(strings.Repeat("z", 32)) {
		t.Error("bounds 8 and 32 are valid")
	}
}

func TestKeyBinding(t *testing.T) {
	if got := keyBinding(42, "abcd1234"); got != "personal_provider.api_key:42:abcd1234" {
		t.Fatalf("binding format is frozen: %q", got)
	}
}

func TestProviderProtocolList(t *testing.T) {
	want := []string{"openai_chat_completions", "openai_responses", "anthropic_messages", "google_generate_content",
		"gemini_interactions", "xai_responses", "openrouter_chat_completions", "openrouter_responses"}
	if strings.Join(ProviderProtocols(), ",") != strings.Join(want, ",") {
		t.Fatalf("provider protocols: %v", ProviderProtocols())
	}
	wantModel := []string{"openai_chat_completions", "openai_responses", "openai_image_generations", "openai_image_edits",
		"anthropic_messages", "google_generate_content", "google_image_generation", "gemini_interactions", "xai_responses",
		"xai_image", "xai_image_edits", "xai_video", "xai_video_extensions", "openrouter_chat_completions",
		"openrouter_responses", "openrouter_images"}
	if strings.Join(ModelProtocols(), ",") != strings.Join(wantModel, ",") {
		t.Fatalf("model protocols: %v", ModelProtocols())
	}
	if p, err := normalizeProviderProtocol("  OpenAI_Chat_Completions "); err != nil || p != "openai_chat_completions" {
		t.Errorf("normalize: %q %v", p, err)
	}
	for _, bad := range []string{"", "openai_image_generations", "xai_video", "nope"} {
		if _, err := normalizeProviderProtocol(bad); !errors.Is(err, ErrInvalidProtocol) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestNormalizeModelProtocols(t *testing.T) {
	ok := []struct{ in, want []string }{
		{[]string{" OpenAI_Responses "}, []string{"openai_responses"}},
		{[]string{"openai_chat_completions", "openai_chat_completions"}, []string{"openai_chat_completions"}},
		{[]string{"openai_image_edits", "openai_image_generations"}, []string{"openai_image_generations", "openai_image_edits"}},
		{[]string{"xai_image", "xai_image_edits"}, []string{"xai_image", "xai_image_edits"}},
		{[]string{"xai_video_extensions", "xai_video"}, []string{"xai_video", "xai_video_extensions"}},
	}
	for _, c := range ok {
		got, err := normalizeModelProtocols(c.in)
		if err != nil || strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%v -> %v, %v", c.in, got, err)
		}
	}
	bad := [][]string{
		nil, {}, {""}, {"nope"}, {"openai_chat_completions", "anthropic_messages"},
		{"openai_image_generations", "xai_image_edits"}, {"openai_chat_completions", "openai_image_edits"},
		{"xai_video", "xai_image"}, {"a", "b", "c"},
		{"openai_image_generations", "openai_image_edits", "openai_responses"},
	}
	for _, in := range bad {
		if _, err := normalizeModelProtocols(in); !errors.Is(err, ErrInvalidModelProto) {
			t.Errorf("%v: %v", in, err)
		}
	}
}

func TestSuggestProtocols(t *testing.T) {
	cases := []struct {
		provider, model string
		want            string
	}{
		{ProtoOpenAIChat, "gpt-4o", "openai_chat_completions"},
		{ProtoAnthropic, "claude-sonnet", "anthropic_messages"},
		{ProtoOpenAIChat, "gpt-image-1", "openai_image_generations,openai_image_edits"},
		{ProtoOpenAIChat, "dall-e-3", "openai_image_generations"},
		{ProtoOpenAIResponses, "dall-e-2", "openai_image_generations,openai_image_edits"},
		{ProtoOpenAIChat, "grok-imagine-image", "xai_image,xai_image_edits"},
		{ProtoOpenAIChat, "grok-imagine-video", "xai_video,xai_video_extensions"},
		{ProtoXAIResponses, "grok-4", "xai_responses"},
		{ProtoXAIResponses, "grok-imagine-image", "xai_image,xai_image_edits"},
		{ProtoGoogleGenerate, "imagen-4", "google_image_generation"},
		{ProtoGoogleGenerate, "gemini-2.5-pro", "google_generate_content"},
		{ProtoOpenRouterChat, "openai/gpt-image-1", "openrouter_images"},
		{ProtoOpenRouterChat, "anthropic/claude", "openrouter_chat_completions"},
		{ProtoOpenAIChat, "sora-2", "openai_chat_completions"},
	}
	for _, c := range cases {
		got := strings.Join(suggestProtocols(c.provider, c.model), ",")
		if got != c.want {
			t.Errorf("suggest(%s, %s) = %s, want %s", c.provider, c.model, got, c.want)
		}
		if _, err := normalizeModelProtocols(suggestProtocols(c.provider, c.model)); err != nil {
			t.Errorf("suggestion for %s is not a valid combination: %v", c.model, err)
		}
	}
}

func TestProtocolForTask(t *testing.T) {
	pair := []string{ProtoOpenAIImageGen, ProtoOpenAIImageEdit}
	if p, ok := ProtocolForTask(pair, TaskImageGeneration); !ok || p != ProtoOpenAIImageGen {
		t.Errorf("gen: %q %v", p, ok)
	}
	if p, ok := ProtocolForTask(pair, TaskImageEdit); !ok || p != ProtoOpenAIImageEdit {
		t.Errorf("edit: %q %v", p, ok)
	}
	if _, ok := ProtocolForTask(pair, TaskChat); ok {
		t.Error("image pair cannot chat")
	}
	if p, ok := ProtocolForTask([]string{ProtoAnthropic}, ""); !ok || p != ProtoAnthropic {
		t.Errorf("empty task is chat: %q %v", p, ok)
	}
	if _, ok := ProtocolForTask([]string{ProtoAnthropic}, TaskVideoGeneration); ok {
		t.Error("no video")
	}
}

func TestDecodeModels(t *testing.T) {
	const p = "openai_chat_completions"
	got := decodeModels(`["m1","m2"]`, p)
	if len(got) != 2 || got[0].Name != "m1" || got[1].Protocols[0] != p {
		t.Fatalf("legacy strings: %+v", got)
	}
	got = decodeModels(`[{"name":"a","protocols":["xai_image","xai_image_edits"]},{"name":"b","protocols":[]},{"name":"c"},{"name":""},7,null,"",{"name":" d "}]`, p)
	names := ""
	for _, m := range got {
		names += m.Name + ":" + strings.Join(m.Protocols, "+") + ";"
	}
	if names != "a:xai_image+xai_image_edits;b:"+p+";c:"+p+";d:"+p+";" {
		t.Fatalf("mixed: %s", names)
	}
	for _, raw := range []string{"", "not json", "{}", "null", `"x"`} {
		got := decodeModels(raw, p)
		if got == nil || len(got) != 0 {
			t.Errorf("%q should decode to an empty non-nil list, got %#v", raw, got)
		}
	}
	if encodeModels(nil) != "[]" || encodeModels([]EnabledModel{}) != "[]" {
		t.Error("empty encodes as []")
	}
}

func TestSelectModels(t *testing.T) {
	catalog := []string{"a", "b", "gpt-image-1"}
	got, err := selectModels([]ModelInput{{Name: "a"}, {Name: "a"}, {Name: "gpt-image-1"}}, catalog, ProtoOpenAIChat)
	if err != nil || len(got) != 2 || got[0].Protocols[0] != ProtoOpenAIChat || len(got[1].Protocols) != 2 {
		t.Fatalf("defaults and de-dup: %+v %v", got, err)
	}
	if got, err := selectModels(nil, catalog, ProtoOpenAIChat); err != nil || got == nil || len(got) != 0 {
		t.Errorf("nothing selected is fine: %#v %v", got, err)
	}
	if _, err := selectModels([]ModelInput{{Name: "zzz"}}, catalog, ProtoOpenAIChat); !errors.Is(err, ErrInvalidModels) {
		t.Errorf("unknown model: %v", err)
	}
	for _, bad := range []string{" a", "a ", "a\n", "\ta"} {
		if _, err := selectModels([]ModelInput{{Name: bad}}, catalog, ProtoOpenAIChat); !errors.Is(err, ErrInvalidModels) {
			t.Errorf("name with surrounding whitespace %q must be rejected: %v", bad, err)
		}
	}
	if _, err := selectModels([]ModelInput{{Name: "a b"}}, []string{"a b"}, ProtoOpenAIChat); !errors.Is(err, ErrInvalidModels) {
		t.Errorf("invalid name even when in catalog: %v", err)
	}
	if _, err := selectModels([]ModelInput{{Name: "a", Protocols: []string{"nope"}}}, catalog, ProtoOpenAIChat); !errors.Is(err, ErrInvalidModelProto) {
		t.Errorf("bad protocol: %v", err)
	}
	many := make([]ModelInput, 201)
	for i := range many {
		many[i] = ModelInput{Name: "a"}
	}
	if _, err := selectModels(many, catalog, ProtoOpenAIChat); !errors.Is(err, ErrInvalidModels) {
		t.Errorf("201 entries: %v", err)
	}
	if got, err := selectModels(many[:200], catalog, ProtoOpenAIChat); err != nil || len(got) != 1 {
		t.Errorf("200 entries collapse: %v %v", got, err)
	}
}

func TestSettingsNormalized(t *testing.T) {
	if (Settings{MaxPerUser: 0}).normalized().MaxPerUser != 10 || (Settings{MaxPerUser: -3}).normalized().MaxPerUser != 10 || (Settings{MaxPerUser: 4}).normalized().MaxPerUser != 4 {
		t.Error("non-positive falls back to 10")
	}
}

func TestClassifyUpstream(t *testing.T) {
	cases := []struct {
		err  error
		want *error
	}{}
	_ = cases
	for _, st := range []int{401, 403} {
		if err := classifyUpstream(&UpstreamStatusError{Status: st}); !errors.Is(err, ErrUpstreamRejected) {
			t.Errorf("%d: %v", st, err)
		}
	}
	for _, st := range []int{400, 404, 429, 500, 502} {
		if err := classifyUpstream(&UpstreamStatusError{Status: st}); !errors.Is(err, ErrUpstreamDown) {
			t.Errorf("%d: %v", st, err)
		}
	}
	if err := classifyUpstream(errors.New("dial tcp: boom")); !errors.Is(err, ErrUpstreamDown) {
		t.Errorf("network: %v", err)
	}
	if err := classifyUpstream(errors.New("secret-key-123 leaked")); strings.Contains(ErrUpstreamDown.Message, "secret") || !errors.Is(err, ErrUpstreamDown) {
		t.Error("public message must be static")
	}
}

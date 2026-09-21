package contentmoderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- probe ----------------------------------------------------------------------------

type probeData struct {
	Text  ProbeTarget `json:"text"`
	Image ProbeTarget `json:"image"`
}

func (h *harness) probe() (probeData, int) {
	h.t.Helper()
	w := h.do("POST", "/probe", nil)
	var d probeData
	if w.Code == 200 {
		testkit.Decode(h.t, w, &d)
	}
	return d, w.Code
}

func imageCapableResp(model string) *Response {
	r := cleanResp(model)
	r.Results[0].AppliedInputTypes = map[string][]string{"violence": {"text", "image"}, "hate": {"text"}}
	return r
}

func TestProbeBothTargetsValid(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	var seen []string
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		seen = append(seen, "text:"+text)
		if len(scope.Selected) != 0 {
			t.Errorf("probe must not filter categories: %v", scope.Selected)
		}
		h.env.Clock.Advance(25 * time.Millisecond)
		return imageCapableResp("provider-model"), nil
	}
	h.prov.image = func(images []Image, scope Scope) (*Response, error) {
		seen = append(seen, "image")
		if len(images) != 1 {
			t.Errorf("one probe image expected, got %d", len(images))
		}
		if _, err := png.Decode(bytes.NewReader(images[0].Data)); err != nil || images[0].MIME != "image/png" {
			t.Errorf("probe image is not a PNG: %v", err)
		}
		cfg, _ := png.DecodeConfig(bytes.NewReader(images[0].Data))
		if cfg.Width != 1 || cfg.Height != 1 {
			t.Errorf("probe image is %dx%d", cfg.Width, cfg.Height)
		}
		return imageCapableResp(""), nil
	}
	d, code := h.probe()
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if !d.Text.Valid || d.Text.Model != "provider-model" || d.Text.LatencyMS != 25 || d.Text.Error != "" {
		t.Fatalf("text %+v", d.Text)
	}
	if !d.Image.Valid || d.Image.Model != "m-test" {
		t.Fatalf("image (model falls back to the configured one) %+v", d.Image)
	}
	if strings.Join(seen, ",") != "text:hello,image" {
		t.Fatalf("calls %v", seen)
	}
}

func TestProbeUsesSavedConfigNotRequestBody(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	var gotURL, gotAuth string
	ports := Ports{HTTP: handlerDoer{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL, gotAuth = r.URL.String(), r.Header.Get("Authorization")
		body := okBody("wire-model")
		body["results"].([]any)[0].(map[string]any)["category_applied_input_types"] = map[string][]string{"violence": {"text", "image"}}
		writeJSON(w, body)
	})}}
	feature := features.Feature{Name: "contentmoderation", Migrations: Migrations, Mount: func(d deps.Deps) error {
		_, err := MountWith(d, ports)
		return err
	}}
	env := testkit.New(t, []features.Feature{feature})
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		return env.Do(method, "/api/v1/admin/content-moderation"+path, env.Bearer(superClaims), body)
	}
	testkit.Expect(t, do("PUT", "/config", map[string]any{
		"baseUrl": "https://wire.example", "apiKey": "sk-wire-key-9999", "model": "wire", "enabled": true,
		"policy": map[string]any{"inputTextCategories": []string{"hate"}},
	}), 200, "")
	w := do("POST", "/probe", map[string]any{"baseUrl": "https://ignored.example", "apiKey": "other"})
	testkit.Expect(t, w, 200, "")
	var d probeData
	testkit.Decode(t, w, &d)
	if !d.Text.Valid || !d.Image.Valid || d.Text.Model != "wire-model" {
		t.Fatalf("%+v", d)
	}
	if gotURL != "https://wire.example/v1/moderations" || gotAuth != "Bearer sk-wire-key-9999" {
		t.Fatalf("url %q auth %q", gotURL, gotAuth)
	}
}

func TestProbeImageNeedsDeclaredImageSupport(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(string, Scope) (*Response, error) { return imageCapableResp("m"), nil }
	h.prov.image = func([]Image, Scope) (*Response, error) {
		r := cleanResp("m")
		r.Results[0].AppliedInputTypes = map[string][]string{"hate": {"text"}}
		return r, nil
	}
	d, code := h.probe()
	if code != 200 || !d.Text.Valid || d.Image.Valid || d.Image.Error != "moderation response missing image category_applied_input_types" {
		t.Fatalf("%d %+v", code, d)
	}
	// Case-insensitive match of "image".
	h.prov.image = func([]Image, Scope) (*Response, error) {
		r := cleanResp("m")
		r.Results[0].AppliedInputTypes = map[string][]string{"violence": {"IMAGE"}}
		return r, nil
	}
	if d, _ = h.probe(); !d.Image.Valid {
		t.Fatalf("%+v", d.Image)
	}
	// No applied-input-types at all.
	h.prov.image = func([]Image, Scope) (*Response, error) { return cleanResp("m"), nil }
	if d, _ = h.probe(); d.Image.Valid {
		t.Fatalf("%+v", d.Image)
	}
}

func TestProbeErrorTexts(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	cases := map[error]string{
		&ProviderError{Code: CodeTimeout}:         "content moderation timed out",
		&ProviderError{Code: CodeRateLimited}:     "content moderation rate limited",
		&ProviderError{Code: CodeInvalidResponse}: "content moderation invalid response",
		&ProviderError{Code: CodeNetworkError}:    "content moderation network error",
		&ProviderError{Code: CodeServiceError}:    "content moderation service error",
		context.DeadlineExceeded:                  "content moderation timed out",
	}
	for perr, want := range cases {
		perr := perr
		h.prov.text = func(string, Scope) (*Response, error) { return nil, perr }
		h.prov.image = func([]Image, Scope) (*Response, error) { return nil, perr }
		d, code := h.probe()
		if code != 200 || d.Text.Valid || d.Image.Valid || d.Text.Error != want || d.Image.Error != want {
			t.Errorf("%v: %d %+v", perr, code, d)
		}
	}
	h.prov.text = func(string, Scope) (*Response, error) { return nil, errUnknown }
	if d, _ := h.probe(); d.Text.Error != "content moderation probe failed" {
		t.Fatalf("unknown error text %q", d.Text.Error)
	}
}

var errUnknown = &net.DNSError{Err: "secret-host sk-leak", Name: "x"}

func TestProbeNeverEchoesProviderDetails(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	ports := Ports{HTTP: handlerDoer{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream says: bad key sk-test-key-123456", 500)
	})}}
	real := newHTTPProvider(ports.HTTP, h.env.Clock.Now)
	real.sleep = func(ctx context.Context, _ time.Duration) error { return nil }
	h.svc.provider = real
	res, err := h.svc.Probe(context.Background(), Actor{UserID: 1, Role: "superadmin"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text.Valid || res.Text.Error != "content moderation service error" || strings.Contains(res.Text.Error, "sk-") {
		t.Fatalf("%+v", res.Text)
	}
}

func TestProbeWithoutProviderIsAnInternalError(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.provider = nil
	testkitExpect(t, h.do("POST", "/probe", nil), 500, "internal.error")
}

func TestProbeImageConstantIsAValidPNG(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(probePNG)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 || img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatal("probe image must be a 1x1 transparent PNG")
	}
}

func TestProbeConfigErrorIsInternal(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "api_key"}).Update("value", "enc:garbage")
	testkitExpect(t, h.do("POST", "/probe", nil), 500, "internal.error")
}

// ---- mounting ---------------------------------------------------------------------------

func TestPlainMountWorksWithoutPorts(t *testing.T) {
	feature := features.Feature{Name: "contentmoderation", Migrations: Migrations, Mount: Mount}
	env := testkit.New(t, []features.Feature{feature})
	if Shared() == nil {
		t.Fatal("Shared must be set by Mount")
	}
	w := env.Do("GET", "/api/v1/admin/content-moderation/config", env.Bearer(superClaims), nil)
	testkit.Expect(t, w, 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/content-moderation/events", env.Bearer(superClaims), nil), 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/content-moderation/stats", env.Bearer(superClaims), nil), 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/content-moderation/events/cme_x/images/0", env.Bearer(superClaims), nil), 404, "content_moderation.event_not_found")

	// Without optional ports a run still records events and stats.
	svc := Shared()
	svc.provider = &fakeProvider{}
	testkit.Expect(t, env.Do("PUT", "/api/v1/admin/content-moderation/config", env.Bearer(superClaims), map[string]any{
		"enabled": true, "apiKey": "sk-1234567890", "policy": map[string]any{"outputTextCategories": []string{"hate"}},
	}), 200, "")
	run := svc.BeginRun(context.Background(), RunInfo{RunID: "plain"}, RunOptions{})
	if out := run.Finish(context.Background(), Output{AssistantText: "hello"}); out.State != StatePassed {
		t.Fatalf("%+v", out)
	}
	var n int64
	env.DB.Model(&Event{}).Count(&n)
	if n != 1 {
		t.Fatalf("events %d", n)
	}
}

func TestStartupAndShutdownHooks(t *testing.T) {
	h := newHarness(t)
	h.insertEvent(Event{Direction: "input", Modality: "text", Result: "hit", EncryptedText: "c", ContentSummary: SummaryInputTextHit,
		ContentExpiresAt: h.env.Clock.Now().Add(-time.Hour)})
	// The startup hook performs a full maintenance pass.
	h.svc.RunMaintenance(context.Background(), true)
	var e Event
	h.env.DB.First(&e)
	if e.EncryptedText != "" {
		t.Fatal("startup cleanup did not run")
	}
	h.svc.Close()
	h.svc.Close() // idempotent
	run := h.begin("after-close", nil)
	if run.Active() {
		t.Fatal("no moderation after shutdown")
	}
}

func TestShutdownResolvesWaitingChecksAsWorkerLost(t *testing.T) {
	h := newHarness(t)
	h.enable(map[string]any{"maxConcurrency": 1, "queueCapacity": 5})
	started := make(chan struct{}, 4)
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		started <- struct{}{}
		time.Sleep(50 * time.Millisecond)
		return cleanResp("m"), nil
	}
	run := h.begin("r", nil)
	run.CheckInputText("first")
	<-started
	run.CheckInputText("second") // waits behind the first
	h.svc.Close()
	out := run.Finish(context.Background(), Output{})
	var lost int
	for _, e := range h.eventsFor("r") {
		if e.ErrorCode == CodeWorkerLost {
			lost++
		}
	}
	if lost != 1 || out.State != StateFailedOpen {
		t.Fatalf("lost %d outcome %+v", lost, out)
	}
}

// ---- image preparation -------------------------------------------------------------------

func TestPrepareImage(t *testing.T) {
	small := pngBytes(t, 100, 50)
	out, mime, err := prepareImage(small, 1024)
	if err != nil || !bytes.Equal(out, small) || mime != "image/png" {
		t.Fatalf("small images pass through unchanged: %v", err)
	}
	big := pngBytes(t, 3000, 1500)
	out, mime, err = prepareImage(big, 1024)
	if err != nil || mime != "image/png" {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 1024 || cfg.Height != 512 {
		t.Fatalf("resized to %dx%d (%v)", cfg.Width, cfg.Height, err)
	}
	tall := pngBytes(t, 400, 2000)
	out, _, _ = prepareImage(tall, 500)
	cfg, _ = png.DecodeConfig(bytes.NewReader(out))
	if cfg.Height != 500 || cfg.Width != 100 {
		t.Fatalf("portrait resized to %dx%d", cfg.Width, cfg.Height)
	}
	// Zero max falls back to the default.
	out, _, _ = prepareImage(big, 0)
	cfg, _ = png.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != defaultImageMaxPx {
		t.Fatalf("default dimension: %d", cfg.Width)
	}

	if _, _, err := prepareImage(nil, 1024); err != ErrFileEmpty {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := prepareImage([]byte("%PDF-1.7 hello"), 1024); err == nil {
		t.Fatal("pdf accepted")
	}
	if _, _, err := prepareImage([]byte("\x89PNG\r\n\x1a\nnot really"), 1024); err == nil {
		t.Fatal("corrupt png accepted")
	}
	webp := append([]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
	if out, mime, err := prepareImage(webp, 1024); err != nil || mime != "image/webp" || !bytes.Equal(out, webp) {
		t.Fatalf("webp passthrough: %v %s", err, mime)
	}
	huge := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxImageBytes)...)
	if _, _, err := prepareImage(huge, 1024); err != errImageTooLarge {
		t.Fatalf("oversize: %v", err)
	}
}

func TestSniffMIMEStripsParameters(t *testing.T) {
	if got := sniffMIME([]byte("<html><body>hi</body></html>")); got != "text/html" {
		t.Fatalf("%q", got)
	}
}

// ---- outbound policy ------------------------------------------------------------------------

func TestGuardedDialRefusesInternalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	host, _, _ := net.SplitHostPort(addr)

	try := func(sec config.SecurityConfig) error {
		client := newOutboundClient(func() config.SecurityConfig { return sec })
		resp, err := client.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := try(config.SecurityConfig{SSRFProtectionEnabled: true}); err == nil || !strings.Contains(err.Error(), "outbound policy") {
		t.Fatalf("loopback must be refused: %v", err)
	}
	if err := try(config.SecurityConfig{SSRFProtectionEnabled: false}); err != nil {
		t.Fatalf("protection off: %v", err)
	}
	if err := try(config.SecurityConfig{SSRFProtectionEnabled: true, SSRFAllowedHosts: "other, " + strings.ToUpper(host)}); err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	if err := try(config.SecurityConfig{SSRFProtectionEnabled: true, SSRFAllowedCIDRs: "10.0.0.0/8, 127.0.0.0/8"}); err != nil {
		t.Fatalf("allowed cidr: %v", err)
	}
	if err := try(config.SecurityConfig{SSRFProtectionEnabled: true, SSRFAllowedCIDRs: "10.0.0.0/8,bad"}); err == nil {
		t.Fatal("unrelated cidr must not allow")
	}
}

func TestRedirectsStayOnTheConfiguredOrigin(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("elsewhere")) }))
	defer other.Close()
	var self *httptest.Server
	self = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL, http.StatusFound)
		case "/within":
			http.Redirect(w, r, self.URL+"/final", http.StatusFound)
		default:
			_, _ = w.Write([]byte("final"))
		}
	}))
	defer self.Close()
	client := newOutboundClient(func() config.SecurityConfig { return config.SecurityConfig{} })
	if resp, err := client.Get(self.URL + "/within"); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	} else {
		resp.Body.Close()
	}
	if resp, err := client.Get(self.URL + "/away"); err == nil {
		resp.Body.Close()
		t.Fatal("cross-origin redirect must be refused")
	}
}

func TestPrivateAddressClassification(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "192.168.0.9": true, "172.16.5.5": true, "169.254.169.254": true,
		"0.0.0.0": true, "::1": true, "fe80::1": true, "fc00::1": true, "8.8.8.8": false, "2606:4700::1111": false,
	} {
		if got := isInternalIP(net.ParseIP(ip)); got != want {
			t.Errorf("isInternalIP(%s) = %v", ip, got)
		}
	}
}

// ---- message helpers ------------------------------------------------------------------------

func TestBuildMessageModeration(t *testing.T) {
	if m := BuildMessageModeration("", "done", "", true, nil, nil); m != nil {
		t.Fatalf("no event and no state must omit the object: %+v", m)
	}
	if m := BuildMessageModeration("not_required", "done", "", false, nil, nil); m != nil {
		t.Fatalf("not_required is not shown: %+v", m)
	}
	m := BuildMessageModeration("passed", "done", "", false, nil, nil)
	if m == nil || m.State != "passed" || m.Direction != "" || m.EventID != "" || m.Categories != nil {
		t.Fatalf("%+v", m)
	}
	m = BuildMessageModeration("blocked", "blocked", "cme_1", true, []string{"hate"}, []string{"violence"})
	if m.State != "blocked" || m.Direction != "input" || m.EventID != "cme_1" || m.Categories[0] != "hate" {
		t.Fatalf("user message: %+v", m)
	}
	m = BuildMessageModeration("blocked", "blocked", "cme_1", false, nil, []string{"violence"})
	if m.Direction != "output" || m.Categories[0] != "violence" {
		t.Fatalf("assistant message falls back to the run's categories: %+v", m)
	}
	// A blocked message forces the state even when the run says otherwise.
	m = BuildMessageModeration("moderating", "blocked", "", false, nil, nil)
	if m.State != "blocked" {
		t.Fatalf("%+v", m)
	}
	// An event id alone is enough to show the object.
	if m = BuildMessageModeration("", "done", "cme_9", false, nil, nil); m == nil || m.EventID != "cme_9" {
		t.Fatalf("%+v", m)
	}
}

func TestBlockedPlaceholderAndContextExclusion(t *testing.T) {
	if got := BlockedPlaceholder("cme_7"); got != "[blocked by content moderation; event cme_7]" {
		t.Fatal(got)
	}
	if got := BlockedPlaceholder("  "); got != "[blocked by content moderation]" {
		t.Fatal(got)
	}
	if !ExcludeFromModelContext("blocked") || ExcludeFromModelContext("done") {
		t.Fatal("only blocked messages are excluded")
	}
	if BlockedErrorCode != "content_moderation.blocked" || BlockedErrorMessage != "content blocked by moderation" {
		t.Fatal("stored error contract")
	}
}

func TestCategoryHelpers(t *testing.T) {
	if len(TextCategories()) != 13 || len(ImageCategories()) != 6 {
		t.Fatal("catalog sizes")
	}
	c := TextCategories()
	c[0] = "mutated"
	if TextCategories()[0] == "mutated" {
		t.Fatal("catalog must be a copy")
	}
	for _, textOnly := range []string{"hate", "hate/threatening", "harassment", "harassment/threatening", "sexual/minors", "illicit", "illicit/violent"} {
		if IsImageCategory(textOnly) || !IsKnownCategory(textOnly) {
			t.Errorf("%s classification", textOnly)
		}
	}
	for _, both := range ImageCategories() {
		if !IsKnownCategory(both) {
			t.Errorf("%s is not a text category", both)
		}
	}
	got, err := normalizeCategories([]string{" violence ", "", "hate", "violence"}, ModalityText)
	if err != nil || strings.Join(got, ",") != "hate,violence" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := normalizeCategories([]string{"hate"}, ModalityImage); err == nil {
		t.Fatal("hate is not an image category")
	}
	if !(Policy{}).nonNil().sameCategories(Policy{InputTextCategories: []string{}}) {
		t.Fatal("nil and empty lists are equal")
	}
	if (Policy{}).AnySelected() || !(Policy{OutputImageCategories: []string{"sexual"}}).AnySelected() {
		t.Fatal("AnySelected")
	}
}

func TestSummaryAndFieldMapping(t *testing.T) {
	cases := []struct{ dir, mod, res, summary string }{
		{"input", "text", "passed", "input_text_pass"}, {"output", "text", "passed", "output_text_pass"},
		{"input", "image", "passed", "image_pass"}, {"output", "image", "passed", "image_pass"},
		{"input", "text", "hit", "input_text_hit"}, {"output", "text", "hit", "output_text_hit"},
		{"input", "image", "hit", "image_hit"}, {"output", "image", "hit", "image_hit"},
		{"input", "text", "failed_open", ""},
	}
	for _, c := range cases {
		if got := summaryFor(c.dir, c.mod, c.res); got != c.summary {
			t.Errorf("summaryFor(%v) = %q", c, got)
		}
	}
	for _, c := range []struct{ dir, mod, field string }{
		{"input", "text", "user_message"}, {"input", "image", "user_attachments"},
		{"output", "text", "assistant_message"}, {"output", "image", "assistant_images"},
	} {
		if got := fieldFor(c.dir, c.mod); got != c.field {
			t.Errorf("fieldFor(%v) = %q", c, got)
		}
	}
	if id := newPublicID(); !strings.HasPrefix(id, "cme_") || len(id) != 28 || id == newPublicID() {
		t.Fatalf("public id %q", id)
	}
}

func TestComposeOutputText(t *testing.T) {
	got := composeOutputText(Output{AssistantText: " answer ", ReasoningText: "think", ThinkingText: "answer"})
	if got != "answer\n\nthink" {
		t.Fatalf("%q", got)
	}
	if composeOutputText(Output{AssistantText: "  ", ReasoningText: "\n"}) != "" {
		t.Fatal("blank parts must be dropped")
	}
}

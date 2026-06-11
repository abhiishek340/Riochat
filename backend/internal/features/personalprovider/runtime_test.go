package personalprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/secretbox"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func (h *harness) resolve(uid uint, ref string) (*Route, error) {
	return h.svc.Resolve(context.Background(), ResolveRequest{UserID: uid, Reference: ref})
}

func TestResolveSuccess(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}, "icon": "openai", "name": "Mine"})
	ref := "personal:" + p.ID + "/a"
	r, err := h.resolve(1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if r.APIKey != "sk-abcdefghijWXYZ" || r.UpstreamModel != "a" || r.Protocol != "openai_chat_completions" ||
		r.BaseURL != "https://api.example.com/v1" || !r.UserSupplied || r.ProviderName != "Mine" || r.ProviderIcon != "openai" {
		t.Fatalf("%+v", r)
	}
	for _, s := range []string{fmt.Sprint(r), fmt.Sprintf("%v", *r), fmt.Sprintf("%+v", r), fmt.Sprintf("%#v", *r)} {
		if strings.Contains(s, "abcdefghij") {
			t.Fatalf("route printed its key: %s", s)
		}
	}
}

func TestResolveFailures(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	ref := FormatReference(p.ID, "a")

	cases := map[string]struct {
		uid uint
		ref string
	}{
		"other user":     {2, ref},
		"user zero":      {0, ref},
		"model disabled": {1, FormatReference(p.ID, "b")},
		"unknown id":     {1, "personal:abcd1234/a"},
		"no slash":       {1, "personal:" + p.ID},
		"wrong prefix":   {1, "other:" + p.ID + "/a"},
		"short id":       {1, "personal:abc/a"},
		"upper id":       {1, "personal:ABCD1234/a"},
		"empty model":    {1, "personal:" + p.ID + "/"},
		"space model":    {1, "personal:" + p.ID + "/a b"},
		"empty":          {1, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.resolve(tc.uid, tc.ref); !errors.Is(err, ErrModelUnavailable) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestResolveExcludedRoutes(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	_, err := h.svc.Resolve(context.Background(), ResolveRequest{UserID: 1, Reference: FormatReference(p.ID, "a"), ExcludedRoutes: true})
	if !errors.Is(err, ErrAllRoutesUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveTaskSelection(t *testing.T) {
	h := newHarness(t).enable()
	h.lister.set(func(ListRequest) ([]string, error) { return []string{"gpt-image-1", "gpt-4o"}, nil })
	p := h.create(1, map[string]any{"models": []string{"gpt-image-1", "gpt-4o"}})
	r, err := h.svc.Resolve(context.Background(), ResolveRequest{UserID: 1, Reference: FormatReference(p.ID, "gpt-image-1"), Task: TaskImageGeneration})
	if err != nil || r.Protocol != "openai_image_generations" {
		t.Fatalf("%v %+v", err, r)
	}
	_, err = h.svc.Resolve(context.Background(), ResolveRequest{UserID: 1, Reference: FormatReference(p.ID, "gpt-image-1")})
	if !errors.Is(err, ErrNoRouteForTask) {
		t.Fatalf("chat on an image model: %v", err)
	}
	_, err = h.svc.Resolve(context.Background(), ResolveRequest{UserID: 1, Reference: FormatReference(p.ID, "gpt-4o"), Task: TaskImageGeneration})
	if !errors.Is(err, ErrNoRouteForTask) {
		t.Fatalf("image on a chat model: %v", err)
	}
}

func TestResolveFeatureSwitchedOffAndOn(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	ref := FormatReference(p.ID, "a")
	h.set(SettingEnabled, "false")
	if _, err := h.resolve(1, ref); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("%v", err)
	}
	h.set(SettingEnabled, "true")
	if _, err := h.resolve(1, ref); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFollowsStatus(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	ref := FormatReference(p.ID, "a")
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": false}), 200, "")
	if _, err := h.resolve(1, ref); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("disabled: %v", err)
	}
	if got := h.activeNames(1); got != "" {
		t.Fatalf("disabled provider listed: %q", got)
	}
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"enabled": true}), 200, "")
	if _, err := h.resolve(1, ref); err != nil {
		t.Fatalf("re-enabled: %v", err)
	}
	h.do("POST", adm+"/suspend", h.admin(9), map[string]any{"ids": []string{p.ID}, "suspended": true})
	if _, err := h.resolve(1, ref); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("suspended: %v", err)
	}
}

func TestResolveDenyListChange(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	ref := FormatReference(p.ID, "a")
	h.set(SettingBlockedHosts, "*.api.example.com")
	if _, err := h.resolve(1, ref); err != nil {
		t.Fatalf("wildcard does not cover the apex: %v", err)
	}
	h.set(SettingBlockedHosts, "api.example.com")
	if _, err := h.resolve(1, ref); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("%v", err)
	}
	if got := h.activeNames(1); got != "" {
		t.Fatalf("catalog must hide deny-listed provider: %q", got)
	}
	testkit.Expect(t, h.do("PATCH", mp+"/"+p.ID, h.user(1), map[string]any{"models": []string{"a"}}), 400, "personal_provider.blocked_host")
}

func TestResolveCiphertextMovedFails(t *testing.T) {
	h := newHarness(t).enable()
	a := h.create(1, map[string]any{"models": []string{"a"}})
	b := h.create(1, map[string]any{"models": []string{"a"}, "apiKey": "sk-another-key-value"})
	c := h.create(2, map[string]any{"models": []string{"a"}})
	var rowA Provider
	h.env.DB.Where("public_id = ?", a.ID).First(&rowA)
	// Copy A's ciphertext into B (same owner) and into C (other owner).
	h.env.DB.Model(&Provider{}).Where("public_id = ?", b.ID).Update("api_key_encrypted", rowA.APIKeyEncrypted)
	h.env.DB.Model(&Provider{}).Where("public_id = ?", c.ID).Update("api_key_encrypted", rowA.APIKeyEncrypted)
	if _, err := h.resolve(1, FormatReference(a.ID, "a")); err != nil {
		t.Fatalf("original still works: %v", err)
	}
	if _, err := h.resolve(1, FormatReference(b.ID, "a")); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("same owner: %v", err)
	}
	if _, err := h.resolve(2, FormatReference(c.ID, "a")); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("other owner: %v", err)
	}
	// Garbage ciphertext and an empty key are "unavailable", not errors.
	h.env.DB.Model(&Provider{}).Where("public_id = ?", a.ID).Update("api_key_encrypted", "garbage")
	if _, err := h.resolve(1, FormatReference(a.ID, "a")); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("garbage: %v", err)
	}
	// Model fetch maps it the same way.
	testkit.Expect(t, h.do("GET", mp+"/"+a.ID+"/models", h.user(1), nil), 503, "personal_provider.model_unavailable")
}

func TestResolveWithoutKeyringIsUnavailable(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, map[string]any{"models": []string{"a"}})
	h.svc.secrets = nil
	if _, err := h.resolve(1, FormatReference(p.ID, "a")); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("%v", err)
	}
}

func (h *harness) activeNames(uid uint) string {
	h.t.Helper()
	ms, err := h.svc.ActiveModels(context.Background(), uid)
	if err != nil {
		h.t.Fatal(err)
	}
	var n []string
	for _, m := range ms {
		n = append(n, m.Reference)
	}
	return strings.Join(n, ",")
}

func TestActiveModels(t *testing.T) {
	h := newHarness(t).enable()
	h.lister.set(func(ListRequest) ([]string, error) { return []string{"a", "b"}, nil })
	p1 := h.create(1, map[string]any{"name": "One", "icon": "openai", "models": []string{"b", "a"}})
	p2 := h.create(1, map[string]any{"name": "Two", "baseURL": "https://two.example.org", "models": []string{"a"}})
	h.create(2, map[string]any{"models": []string{"a"}})

	got := h.activeNames(1)
	want := "personal:" + p1.ID + "/b,personal:" + p1.ID + "/a,personal:" + p2.ID + "/a"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	ms, _ := h.svc.ActiveModels(context.Background(), 1)
	if ms[0].ProviderName != "One" || ms[0].ProviderIcon != "openai" || ms[0].ProviderHost != "api.example.com" ||
		ms[0].UpstreamName != "b" || ms[0].ProviderProtocol != "openai_chat_completions" || len(ms[0].Protocols) != 1 {
		t.Fatalf("%+v", ms[0])
	}
	if h.activeNames(0) != "" || h.activeNames(3) != "" {
		t.Fatal("unknown users have no personal models")
	}
	h.set(SettingEnabled, "false")
	if h.activeNames(1) != "" {
		t.Fatal("feature off hides personal models")
	}
}

func TestLegacyModelsColumn(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	h.env.DB.Model(&Provider{}).Where("public_id = ?", p.ID).Update("enabled_models", `["m1","m2"]`)
	w := h.do("GET", mp, h.user(1), nil)
	var out struct{ Providers []providerJSON }
	testkit.Decode(t, w, &out)
	ms := out.Providers[0].Models
	if len(ms) != 2 || ms[0].Name != "m1" || ms[1].Protocols[0] != "openai_chat_completions" {
		t.Fatalf("%+v", ms)
	}
	h.env.DB.Model(&Provider{}).Where("public_id = ?", p.ID).Update("enabled_models", `not json`)
	w = h.do("GET", mp, h.user(1), nil)
	if !strings.Contains(w.Body.String(), `"models":[]`) {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestReencryptKeys(t *testing.T) {
	h := newHarness(t).enable()
	a := h.create(1, map[string]any{"models": []string{"a"}})
	h.create(2, nil)

	oldRing := h.svc.secrets
	newRing, err := secretbox.NewKeyRing("a-brand-new-active-key-0123456789abcdef", []string{"testkit-data-encryption-key-0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.secrets = newRing
	// Rows are readable through the previous key before rotation.
	if _, err := h.resolve(1, FormatReference(a.ID, "a")); err != nil {
		t.Fatalf("before rotation: %v", err)
	}
	n, err := h.svc.ReencryptKeys(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, r := range h.rows() {
		if _, active, err := newRing.DecryptBound(r.APIKeyEncrypted, keyBinding(r.UserID, r.PublicID)); err != nil || !active {
			t.Fatalf("not rotated: %v %v", active, err)
		}
		if _, _, err := oldRing.DecryptBound(r.APIKeyEncrypted, keyBinding(r.UserID, r.PublicID)); err == nil {
			t.Fatal("old key must no longer decrypt")
		}
	}
	if n, err := h.svc.ReencryptKeys(context.Background()); err != nil || n != 0 {
		t.Fatalf("second run n=%d err=%v", n, err)
	}
	if r, err := h.resolve(1, FormatReference(a.ID, "a")); err != nil || r.APIKey != "sk-abcdefghijWXYZ" {
		t.Fatalf("after rotation: %v", err)
	}
}

func TestReencryptKeysSkipsUnreadableRows(t *testing.T) {
	h := newHarness(t).enable()
	good := h.create(1, nil)
	bad := h.create(2, nil)
	// Damage the second row: a ciphertext no key can open.
	if err := h.svc.db.Model(&Provider{}).Where("public_id = ?", bad.ID).
		Update("api_key_encrypted", "not-a-ciphertext").Error; err != nil {
		t.Fatal(err)
	}
	newRing, err := secretbox.NewKeyRing("a-brand-new-active-key-0123456789abcdef", []string{"testkit-data-encryption-key-0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.secrets = newRing
	n, err := h.svc.ReencryptKeys(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("damaged row must not block the others: n=%d err=%v", n, err)
	}
	for _, r := range h.rows() {
		if r.PublicID != good.ID {
			continue
		}
		if _, active, err := newRing.DecryptBound(r.APIKeyEncrypted, keyBinding(r.UserID, r.PublicID)); err != nil || !active {
			t.Fatalf("good row not rotated: %v %v", active, err)
		}
	}
	h.svc.secrets = nil
	if _, err := h.svc.ReencryptKeys(context.Background()); err == nil {
		t.Fatal("missing keyring must abort")
	}
}

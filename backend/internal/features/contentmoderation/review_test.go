package contentmoderation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestExplicitEmptyAPIKeyIsStoredAndClearsKey(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	if !h.getConfig().Config.HasAPIKey {
		t.Fatal("setup: key expected")
	}
	// Whitespace-only is trimmed and stored: the key is gone, so enabling again fails.
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"enabled": false, "apiKey": "   "}), 200, "")
	if c := h.getConfig().Config; c.HasAPIKey || c.APIKeyMasked != "" {
		t.Fatalf("key should be cleared: %+v", c)
	}
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"enabled": true}), 400, "content_moderation.config_required")
}

func TestApiKeyIsTrimmedWhenStored(t *testing.T) {
	h := newHarness(t)
	testkit.Expect(t, h.do("PUT", "/config", map[string]any{"apiKey": "  sk-abcdefgh12345  "}), 200, "")
	if m := h.getConfig().Config.APIKeyMasked; m != "sk-a...2345" {
		t.Fatalf("masked %q", m)
	}
}

func TestWholeNumberLimitsAreAcceptedAsIntegers(t *testing.T) {
	h := newHarness(t)
	testkit.Expect(t, h.do("PUT", "/config", `{"timeoutSeconds":5.0,"maxConcurrency":3,"queueCapacity":7}`), 200, "")
	c := h.getConfig().Config
	if c.TimeoutSeconds != 5 || c.MaxConcurrency != 3 || c.QueueCapacity != 7 {
		t.Fatalf("%+v", c)
	}
	testkit.Expect(t, h.do("PUT", "/config", `{"timeoutSeconds":0.5}`), 400, "content_moderation.invalid_config")
	testkit.Expect(t, h.do("PUT", "/config", `{"timeoutSeconds":null}`), 200, "")
	if h.getConfig().Config.TimeoutSeconds != 5 {
		t.Fatal("null must leave the value unchanged")
	}
}

func TestStreamEventJSONShapes(t *testing.T) {
	b, _ := json.Marshal(StreamEvent{Type: EventTypeChecking})
	if string(b) != `{"type":"moderation_checking"}` {
		t.Fatalf("checking: %s", b)
	}
	b, _ = json.Marshal(StreamEvent{Type: EventTypeBlocked, Direction: DirectionOutput})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	cats, ok := m["categories"].([]any)
	if !ok || len(cats) != 0 {
		t.Fatalf("blocked must carry categories array: %s", b)
	}
	if _, ok := m["eventID"]; !ok {
		t.Fatalf("blocked must carry eventID: %s", b)
	}
	if _, ok := m["billedReason"]; ok {
		t.Fatalf("billedReason must be omitted when empty: %s", b)
	}
	b, _ = json.Marshal(StreamEvent{Type: EventTypeBlocked, EventID: "cme_x", Direction: DirectionInput,
		Categories: []string{"hate"}, BilledReason: BilledReasonUpstreamUsage})
	if string(b) != `{"type":"moderation_blocked","eventID":"cme_x","direction":"input","categories":["hate"],"billedReason":"moderation_blocked_upstream_usage"}` {
		t.Fatalf("blocked: %s", b)
	}
}

func TestBeginRunWithoutCapabilityProviderStillModerates(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.caps = nil
	h.prov.text = func(string, Scope) (*Response, error) { return hitResp("m", "hate"), nil }
	r := h.begin("nocaps", nil)
	if !r.Active() {
		t.Fatal("run should be active when no capability provider is wired")
	}
	r.CheckInputText("bad words")
	if out := r.Finish(context.Background(), Output{}); !out.Blocked {
		t.Fatalf("outcome %+v", out)
	}
}

func TestBeginRunSkippedWhenCapabilityOff(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.caps = offCaps{}
	if h.begin("capoff", nil).Active() {
		t.Fatal("capability off must disable moderation")
	}
}

type offCaps struct{}

func (offCaps) Enabled(string) bool       { return false }
func (offCaps) Features() map[string]bool { return nil }

func TestEventTimestampColumnsAreNotNull(t *testing.T) {
	h := newHarness(t)
	cols, err := h.env.DB.Migrator().ColumnTypes(&Event{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		if c.Name() == "content_expires_at" || c.Name() == "metadata_expires_at" {
			if nullable, ok := c.Nullable(); ok && nullable {
				t.Fatalf("%s must be NOT NULL", c.Name())
			}
		}
	}
}

func TestImageIndexBoundaries(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(7, pngBytes(t, 8, 8), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	out := h.begin("idx", nil).Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}}})
	base := "/events/" + out.EventID + "/images/"
	testkit.Expect(t, h.do("GET", base+"0", nil), 200, "")
	testkit.Expect(t, h.do("GET", base+"4294967296", nil), 404, "content_moderation.event_not_found")
	testkit.Expect(t, h.do("GET", base+"-0", nil), 400, "request.invalid_image_index")
}

func TestRedactionHelpers(t *testing.T) {
	if got := RedactedUserContent("cme_1"); got != "[blocked by content moderation; event cme_1]" {
		t.Fatal(got)
	}
	if got := RedactedUserContent(""); got != "[blocked by content moderation]" {
		t.Fatal(got)
	}
	if RedactedErrorMessage("boom", "cme_1") != "boom" || RedactedErrorMessage(" ", "cme_1") != BlockedPlaceholder("cme_1") {
		t.Fatal("error message redaction")
	}
}

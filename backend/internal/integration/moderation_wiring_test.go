package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// moderationUpstream answers the OpenAI-compatible moderation endpoint: image
// requests are flagged for "sexual" when flagImages is set, text is clean.
func moderationUpstream(t *testing.T, flagImages *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hit := flagImages != nil && *flagImages && bytes.Contains(body, []byte("image_url"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"m1","model":"omni-test","results":[{"flagged":%v,"categories":{"sexual":%v},"category_scores":{"sexual":0.9},"category_applied_input_types":{"sexual":["text","image"]}}]}`, hit, hit)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func enableModeration(t *testing.T, env *testkit.Env, super map[string]string, upstream string) {
	t.Helper()
	w := env.Do("PUT", "/api/v1/admin/content-moderation/config", super, map[string]any{
		"enabled": true, "baseUrl": upstream, "apiKey": "sk-moderation-key-123456", "model": "omni-test",
		"policy": map[string]any{
			"inputTextCategories": []string{"sexual"}, "outputTextCategories": []string{"sexual"},
			"inputImageCategories": []string{"sexual"}, "outputImageCategories": []string{"sexual"},
		},
	})
	testkit.Expect(t, w, 200, "")
}

func realPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadFile(t *testing.T, env *testkit.Env, hdr map[string]string, name, mimeType string, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="%s"`, name)},
		"Content-Type":        {mimeType},
	})
	_, _ = part.Write(data)
	_ = mw.Close()
	h := map[string]string{"Content-Type": mw.FormDataContentType()}
	for k, v := range hdr {
		h[k] = v
	}
	w := env.Do("POST", "/api/v1/files", h, buf.Bytes())
	testkit.Expect(t, w, 200, "")
	var out struct {
		Data struct {
			File struct {
				FileID string `json:"fileID"`
			} `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.File.FileID
}

type moderationEnv struct {
	env       *testkit.Env
	user      map[string]string
	super     map[string]string
	root      string
	conv      string
	flagImage *bool
}

func newModerationEnv(t *testing.T) *moderationEnv {
	t.Helper()
	root := t.TempDir()
	env := testkit.New(t, registry.Registered(), testkit.WithConfig(func(c *config.Config) {
		c.Security.SSRFProtectionEnabled = false
		c.Storage.Local.RootDir = root
	}))
	m := &moderationEnv{env: env, root: root, flagImage: new(bool)}
	m.user = session(t, env, "mod-user@example.com", "")
	admin := session(t, env, "mod-admin@example.com", "admin")
	m.super = session(t, env, "mod-super@example.com", "superadmin")
	chat := mockUpstream(t, 200, nil)
	addModel(t, env, admin, chat.URL+"/v1", "mock-chat")
	enableModeration(t, env, m.super, moderationUpstream(t, m.flagImage).URL)

	w := env.Do("POST", "/api/v1/conversations", m.user, map[string]string{"model": "mock-chat"})
	testkit.Expect(t, w, 200, "")
	var conv struct {
		Data struct{ PublicID string } `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	m.conv = conv.Data.PublicID
	return m
}

func (m *moderationEnv) stream(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	body["contentType"], body["model"] = "text", "mock-chat"
	w := m.env.Do("POST", "/api/v1/conversations/"+m.conv+"/messages/stream", m.user, body)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		if line == "" {
			continue
		}
		var l map[string]any
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatalf("bad line %q", line)
		}
		out = append(out, l)
	}
	return out
}

// Scenario 8 and 18 through the real engine, bridge and object store: an input
// image hit blocks both messages, keeps the user's file and stores an
// encrypted evidence copy under the documented folder that the administrator
// can read back.
func TestInputImageHitEndToEnd(t *testing.T) {
	m := newModerationEnv(t)
	orig := realPNG(t, 24, 16)
	fileID := uploadFile(t, m.env, m.user, "photo.png", "image/png", orig)
	*m.flagImage = true

	lines := m.stream(t, map[string]any{"content": "what is this?", "clientRunID": "img1", "fileIDs": []string{fileID}})
	last := lines[len(lines)-1]
	if last["type"] != "moderation_blocked" || last["direction"] != "input" || last["eventID"] == "" {
		t.Fatalf("terminal: %v", last)
	}
	terminals := 0
	for _, l := range lines {
		if l["type"] == "moderation_blocked" || l["type"] == "completed" || l["type"] == "error" {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("exactly one terminal event: %d", terminals)
	}
	eventID := last["eventID"].(string)

	// Both messages are withdrawn; the run is blocked.
	var statuses []string
	rows, err := m.env.DB.Raw("SELECT status FROM chat_messages ORDER BY id").Rows()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if strings.Join(statuses, ",") != "blocked,blocked" {
		t.Fatalf("messages: %v", statuses)
	}
	var runState, runStatus string
	m.env.DB.Raw("SELECT moderation_state, status FROM chat_runs WHERE run_id = ?", "run_img1").Row().Scan(&runState, &runStatus)
	if runState != "blocked" || runStatus != "blocked" {
		t.Fatalf("run: %s %s", runState, runStatus)
	}

	// The user's own file stays active and owned.
	var fileStatus string
	var owner uint
	m.env.DB.Raw("SELECT status, user_id FROM file_objects WHERE file_id = ?", fileID).Row().Scan(&fileStatus, &owner)
	if fileStatus != "active" || owner == 0 {
		t.Fatalf("user file: %s %d", fileStatus, owner)
	}

	// Evidence: encrypted object under the documented folder and key shape.
	matches, _ := filepath.Glob(filepath.Join(m.root, "moderation-isolated", eventID, "0_*.bin"))
	if len(matches) != 1 {
		t.Fatalf("evidence objects: %v", matches)
	}
	raw, _ := os.ReadFile(matches[0])
	if len(raw) == 0 || bytes.Contains(raw, orig) {
		t.Fatal("evidence must be stored encrypted")
	}
	w := m.env.Do("GET", "/api/v1/admin/content-moderation/events/"+eventID+"/images/0", m.super, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), orig) || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("admin image: %d %v", w.Code, w.Header())
	}
}

// A clean image passes and the chat file bytes are served again afterwards.
func TestCleanImageTurnCompletes(t *testing.T) {
	m := newModerationEnv(t)
	fileID := uploadFile(t, m.env, m.user, "photo.png", "image/png", realPNG(t, 8, 8))
	lines := m.stream(t, map[string]any{"content": "describe", "clientRunID": "ok1", "fileIDs": []string{fileID}})
	if lines[len(lines)-1]["type"] != "completed" {
		t.Fatalf("%v", lines[len(lines)-1])
	}
	var state string
	m.env.DB.Raw("SELECT moderation_state FROM chat_runs WHERE run_id = ?", "run_ok1").Row().Scan(&state)
	if state != "passed" {
		t.Fatalf("state %q", state)
	}
	// The PDF-only turn: no image task, no event, still passed.
	pdf := uploadFile(t, m.env, m.user, "d.pdf", "application/pdf", []byte("%PDF-1.4 minimal\n%%EOF"))
	before := countEvents(t, m.env, "image")
	lines = m.stream(t, map[string]any{"content": "summarise", "clientRunID": "pdf1", "fileIDs": []string{pdf}})
	if lines[len(lines)-1]["type"] != "completed" || countEvents(t, m.env, "image") != before {
		t.Fatalf("pdf turn: %v", lines[len(lines)-1])
	}
}

func countEvents(t *testing.T, env *testkit.Env, modality string) int64 {
	t.Helper()
	var n int64
	env.DB.Raw("SELECT COUNT(*) FROM content_moderation_events WHERE modality = ?", modality).Row().Scan(&n)
	return n
}

// Scenarios 11 and 12: a run left moderating by a dead process is settled by
// the recovery pass after two minutes, from the stored events alone.
func TestStaleRunRecoveryAfterRestart(t *testing.T) {
	m := newModerationEnv(t)
	for _, id := range []string{"crashhit", "crashnone"} {
		lines := m.stream(t, map[string]any{"content": "hello " + id, "clientRunID": id})
		if lines[len(lines)-1]["type"] != "completed" {
			t.Fatalf("%v", lines[len(lines)-1])
		}
	}
	// Simulate the crash: both runs are stuck moderating; one has a stored hit.
	m.env.DB.Exec("UPDATE chat_runs SET moderation_state = 'moderating' WHERE run_id IN ('run_crashhit','run_crashnone')")
	now := m.env.Clock.Now()
	hit := contentmoderation.Event{
		PublicID: "cme_crashhit0000000000000", RunID: "run_crashhit", Direction: "output", Modality: "text", Result: "hit",
		CategoriesJSON: `["sexual"]`, CategoryScoresJSON: "{}", ContentLocationJSON: "{}", ImageMetaJSON: "[]",
		ContentExpiresAt: now.Add(24 * time.Hour), MetadataExpiresAt: now.Add(48 * time.Hour),
	}
	if err := m.env.DB.Create(&hit).Error; err != nil {
		t.Fatal(err)
	}
	svc := contentmoderation.Shared()

	// Younger than two minutes: untouched.
	svc.RunMaintenance(context.Background(), false)
	var state string
	m.env.DB.Raw("SELECT moderation_state FROM chat_runs WHERE run_id = 'run_crashhit'").Row().Scan(&state)
	if state != "moderating" {
		t.Fatalf("a young run must be left alone: %q", state)
	}

	m.env.Clock.Advance(3 * time.Minute)
	m.env.DB.Exec("UPDATE chat_runs SET updated_at = ? WHERE run_id IN ('run_crashhit','run_crashnone')", m.env.Clock.Now().Add(-3*time.Minute))
	svc.RunMaintenance(context.Background(), false)

	var content, msgStatus, code string
	m.env.DB.Raw("SELECT content, status, error_code FROM chat_messages WHERE run_id = 'run_crashhit' AND role = 'assistant'").Row().Scan(&content, &msgStatus, &code)
	if content != "" || msgStatus != "blocked" || code != "content_moderation.blocked" {
		t.Fatalf("assistant after recovery: %q %q %q", content, msgStatus, code)
	}
	var runStatus, evt string
	m.env.DB.Raw("SELECT moderation_state, status, moderation_event_id FROM chat_runs WHERE run_id = 'run_crashhit'").Row().Scan(&state, &runStatus, &evt)
	if state != "blocked" || runStatus != "blocked" || evt != "cme_crashhit0000000000000" {
		t.Fatalf("run after recovery: %s %s %s", state, runStatus, evt)
	}
	var lost int64
	m.env.DB.Raw("SELECT COUNT(*) FROM content_moderation_events WHERE run_id = 'run_crashhit' AND error_code = 'worker_lost'").Row().Scan(&lost)
	if lost != 0 {
		t.Fatal("a run with a stored hit must not be failed open")
	}

	// No hit: failed_open with one worker_lost event, nothing counted in stats.
	m.env.DB.Raw("SELECT moderation_state FROM chat_runs WHERE run_id = 'run_crashnone'").Row().Scan(&state)
	if state != "failed_open" {
		t.Fatalf("no hit event -> failed_open, got %q", state)
	}
	m.env.DB.Raw("SELECT COUNT(*) FROM content_moderation_events WHERE run_id = 'run_crashnone' AND error_code = 'worker_lost' AND direction = 'output' AND modality = 'text' AND user_id = 0").Row().Scan(&lost)
	if lost != 1 {
		t.Fatalf("worker_lost events: %d", lost)
	}
	var failures int64
	m.env.DB.Raw("SELECT COALESCE(SUM(failure_count),0) FROM content_moderation_daily_stats WHERE result = 'failed_open'").Row().Scan(&failures)
	if failures != 0 {
		t.Fatalf("recovery must not touch the stats: %d", failures)
	}
	// A second pass changes nothing.
	svc.RunMaintenance(context.Background(), false)
	m.env.DB.Raw("SELECT COUNT(*) FROM content_moderation_events WHERE run_id = 'run_crashnone' AND error_code = 'worker_lost'").Row().Scan(&lost)
	if lost != 1 {
		t.Fatalf("recovery must be idempotent: %d", lost)
	}
}

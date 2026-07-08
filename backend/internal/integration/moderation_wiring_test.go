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

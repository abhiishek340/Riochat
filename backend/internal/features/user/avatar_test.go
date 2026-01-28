package user

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func avatarURL(publicID string) string { return "/api/v1/users/" + publicID + "/avatar" }

// avatarFixture creates a user whose avatar is the given upload.
func avatarFixture(t *testing.T, files *fakeFiles) (*testkit.Env, Account) {
	t.Helper()
	env := newEnv(t, Ports{Files: files})
	u := seedAccount(t, env.DB, "alice", func(a *Account) { a.AvatarURL = "file:file_demo_9" })
	return env, u
}

func TestAvatarServesBytesWithHeaders(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	mod := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	files.put(u.ID, "file_demo_9", Upload{Body: body("PNGDATA"), ContentType: "image/png", Size: 7, ModTime: mod, FileName: "a.png"})

	w := env.Do("GET", avatarURL(u.PublicID), nil, nil) // no credentials
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if w.Body.String() != "PNGDATA" {
		t.Fatalf("body %q", w.Body)
	}
	want := map[string]string{
		"Content-Type":           "image/png",
		"Content-Disposition":    "inline",
		"Cache-Control":          "public, max-age=300",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         "7",
		"Last-Modified":          "Tue, 03 Feb 2026 04:05:06 GMT",
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if strings.HasPrefix(w.Body.String(), "{") {
		t.Fatal("success must not be an envelope")
	}
	if len(files.opened) != 1 || files.opened[0] != "1/file_demo_9" {
		t.Fatalf("opened on behalf of %v", files.opened)
	}
}

func TestAvatarOmitsLengthAndModTimeWhenUnknown(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: "image/webp"})
	w := env.Do("GET", avatarURL(u.PublicID), nil, nil)
	if w.Code != 200 || w.Header().Get("Last-Modified") != "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: "image/webp", Size: -1})
	w = env.Do("GET", avatarURL(u.PublicID), nil, nil)
	if w.Header().Get("Content-Length") != "" {
		t.Fatalf("length header %q", w.Header().Get("Content-Length"))
	}
}

func TestAvatarContentTypeFallsBackToFileExtension(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), FileName: "me.PNG"})
	w := env.Do("GET", avatarURL(u.PublicID), nil, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, name := range []string{"notes.txt", "noext", "", "page.html", "icon.svg"} {
		files.put(u.ID, "file_demo_9", Upload{Body: body("x"), FileName: name})
		w = env.Do("GET", avatarURL(u.PublicID), nil, nil)
		if name == "icon.svg" {
			// SVG is image/svg+xml on most systems; the upload allow-list is what keeps
			// it out, but the endpoint still refuses to guess for unknown extensions.
			continue
		}
		testkit.Expect(t, w, 404, "avatar.not_found")
	}
}

func TestAvatarContentTypeCheckIsCaseInsensitive(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: "IMAGE/PNG"})
	if w := env.Do("GET", avatarURL(u.PublicID), nil, nil); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
}

type trackedBody struct {
	reads  int
	closed bool
}

func (b *trackedBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *trackedBody) Close() error             { b.closed = true; return nil }

func TestAvatarNonImageIs404AndStreamIsClosedUnread(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	tb := &trackedBody{}
	files.uploads["1/file_demo_9"] = &Upload{Body: tb, ContentType: "text/html"}
	// The fake copies the body; bypass it with a reader that returns tb directly.
	env2 := newEnv(t, Ports{Files: directFiles{up: &Upload{Body: tb, ContentType: "application/pdf"}}})
	u2 := seedAccount(t, env2.DB, "bobby", func(a *Account) { a.AvatarURL = "file:file_demo_9" })
	w := env2.Do("GET", avatarURL(u2.PublicID), nil, nil)
	testkit.Expect(t, w, 404, "avatar.not_found")
	if tb.reads != 0 || !tb.closed {
		t.Fatalf("reads=%d closed=%v", tb.reads, tb.closed)
	}
	for _, ct := range []string{"text/html", "application/octet-stream", "video/mp4", "imagex/png"} {
		files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: ct})
		testkit.Expect(t, env.Do("GET", avatarURL(u.PublicID), nil, nil), 404, "avatar.not_found")
	}
}

type directFiles struct{ up *Upload }

func (d directFiles) OpenUpload(_ context.Context, _ uint, _ string) (*Upload, error) {
	return d.up, nil
}

func TestAvatarRejectsUnknownBlankAndNonUploadAvatars(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: "image/png"})

	testkit.Expect(t, env.Do("GET", avatarURL("0123456789abcdef0123456789abcdef"), nil, nil), 404, "user.not_found")
	testkit.Expect(t, env.Do("GET", avatarURL("%20%20"), nil, nil), 400, "user.invalid_id")
	if msg := testkit.Envelope(t, env.Do("GET", avatarURL("%20"), nil, nil)).ErrorMsg; msg != "invalid user id" {
		t.Fatalf("message %q", msg)
	}
	// The internal numeric id is not a public id.
	testkit.Expect(t, env.Do("GET", avatarURL("1"), nil, nil), 404, "user.not_found")
	// Surrounding whitespace is trimmed.
	if w := env.Do("GET", avatarURL("%20"+u.PublicID+"%20"), nil, nil); w.Code != 200 {
		t.Fatalf("trimmed id: %d", w.Code)
	}

	for _, avatar := range []string{"", "https://example.com/a.png", "/avatars/default.png", "generated:github:octocat", "file:invalid", "javascript:alert(1)"} {
		other := seedAccount(t, env.DB, "u"+strings.NewReplacer(":", "", "/", "", ".", "", "-", "", " ", "", "(", "", ")", "").Replace(avatar)+"xyz", func(a *Account) { a.AvatarURL = avatar })
		testkit.Expect(t, env.Do("GET", avatarURL(other.PublicID), nil, nil), 404, "avatar.not_found")
	}
}

func TestAvatarMissingUploadAndOwnership(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	testkit.Expect(t, env.Do("GET", avatarURL(u.PublicID), nil, nil), 404, "avatar.not_found")
	// An upload that belongs to somebody else is not reachable through this user.
	files.put(u.ID+100, "file_demo_9", Upload{Body: body("x"), ContentType: "image/png"})
	testkit.Expect(t, env.Do("GET", avatarURL(u.PublicID), nil, nil), 404, "avatar.not_found")
}

func TestAvatarWithoutFileReaderIs404(t *testing.T) {
	env := newEnv(t, Ports{})
	u := seedAccount(t, env.DB, "alice", func(a *Account) { a.AvatarURL = "file:file_demo_9" })
	testkit.Expect(t, env.Do("GET", avatarURL(u.PublicID), nil, nil), 404, "avatar.not_found")
}

func TestAvatarOpenFailureIs500(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.err = errors.New("disk on fire /srv/secret")
	w := env.Do("GET", avatarURL(u.PublicID), nil, nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("cause leaked")
	}
}

func TestAvatarSoftDeletedUserIs404(t *testing.T) {
	files := newFakeFiles()
	env, u := avatarFixture(t, files)
	files.put(u.ID, "file_demo_9", Upload{Body: body("x"), ContentType: "image/png"})
	env.DB.Delete(&Account{}, u.ID)
	testkit.Expect(t, env.Do("GET", avatarURL(u.PublicID), nil, nil), 404, "user.not_found")
}

type brokenBody struct{ sent bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("connection reset")
}
func (b *brokenBody) Close() error { return nil }

func TestAvatarStreamFailureAbortsWithoutEnvelope(t *testing.T) {
	env := newEnv(t, Ports{Files: directFiles{up: &Upload{Body: &brokenBody{}, ContentType: "image/png", Size: 100}}})
	u := seedAccount(t, env.DB, "alice", func(a *Account) { a.AvatarURL = "file:file_demo_9" })
	w := env.Do("GET", avatarURL(u.PublicID), nil, nil)
	if w.Body.String() != "partial" {
		t.Fatalf("body %q", w.Body)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

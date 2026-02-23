package deps

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

func TestForAreaAddsLoggerField(t *testing.T) {
	var buf bytes.Buffer
	d := Deps{Logger: logx.New(&buf, logx.LevelInfo)}
	d.ForArea("billing").Logger.Info("hello")
	if !strings.Contains(buf.String(), "hello area billing") {
		t.Fatalf("area field missing: %s", buf.String())
	}
}

func TestGatedAndUnknownFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	g := e.Group("/api/v1")
	d := Deps{Routes: Routes{Public: g, Auth: g, Admin: g}, Capabilities: capability.Local()}

	d.Gated(capability.Sharing).Public.GET("/shared", func(c *gin.Context) { c.Status(200) })
	d.Gated(capability.UsageMetering).Public.GET("/usage", func(c *gin.Context) { c.Status(200) })

	for path, want := range map[string]int{"/api/v1/shared": http.StatusNotFound, "/api/v1/usage": http.StatusOK} {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s: %d want %d", path, w.Code, want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("unknown flag must panic")
		}
	}()
	d.Gated("sharng")
}

func TestLocalGrantHook(t *testing.T) {
	var h LocalGrantHook
	if _, ok, err := h.Issue(context.Background()); ok || err != nil {
		t.Fatalf("unset hook: %v %v", ok, err)
	}
	h.Set(func(context.Context) (string, error) { return "g1", nil })
	if g, ok, err := h.Issue(context.Background()); !ok || err != nil || g != "g1" {
		t.Fatalf("set hook: %q %v %v", g, ok, err)
	}
	boom := errors.New("boom")
	h.Set(func(context.Context) (string, error) { return "", boom })
	if _, ok, err := h.Issue(context.Background()); !ok || !errors.Is(err, boom) {
		t.Fatalf("error hook: %v %v", ok, err)
	}
	var nilHook *LocalGrantHook
	if _, ok, _ := nilHook.Issue(context.Background()); ok {
		t.Fatal("nil hook is unset")
	}
}

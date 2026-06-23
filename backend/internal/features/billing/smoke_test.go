package billing

import (
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestSmokeConfigAndSeed(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/billing/config", e.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Config map[string]any `json:"config"`
	}
	testkit.Decode(t, w, &out)
	if out.Config["mode"] != "self" {
		t.Fatalf("mode %v", out.Config["mode"])
	}
	var n int64
	e.DB.Model(&Plan{}).Count(&n)
	if n != 4 {
		t.Fatalf("plans %d", n)
	}
}

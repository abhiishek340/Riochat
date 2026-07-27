package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/mcp/mcptest"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

type fakeSettings struct {
	mu sync.Mutex
	s  RuntimeSettings
}

func (f *fakeSettings) MCPSettings(context.Context) RuntimeSettings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s
}

func (f *fakeSettings) update(fn func(*RuntimeSettings)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.s)
}

type fakeBilling struct {
	mode string
	err  error
}

func (b fakeBilling) BillingMode(context.Context) (string, error) { return b.mode, b.err }

type harness struct {
	t      *testing.T
	db     *gorm.DB
	svc    *Service
	set    *fakeSettings
	clk    *clock.Fake
	ring   *secretbox.KeyRing
	fake   *mcptest.Server
	secret string
}

const projectLinkDDL = "CREATE TABLE chat_conversation_project_mcp_tools (project_id integer NOT NULL, tool_id integer NOT NULL, sort_order integer NOT NULL DEFAULT 0, PRIMARY KEY (project_id, tool_id))"

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := dbtest.New(t)
	if err := migrate.Run(context.Background(), db, nil, migrate.Set{Area: "mcp", Steps: Migrations()}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Exec(projectLinkDDL).Error; err != nil {
		t.Fatalf("link table: %v", err)
	}
	ring, err := secretbox.NewKeyRing("test-active-key-0123456789", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, db: db, ring: ring, secret: "s", fake: mcptest.New(t),
		clk: clock.NewFake(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC))}
	h.set = &fakeSettings{s: DefaultRuntimeSettings()}
	h.set.s.Enabled = true
	h.svc = &Service{
		db: db, secrets: ring, clock: h.clk, settings: h.set, links: sqlProjectLinks{},
		client: NewHTTPClient(), signKey: func() string { return h.secret }, log: logx.Nop(), limiter: &callLimiter{},
	}
	t.Cleanup(h.svc.Close)
	return h
}

func ptr[T any](v T) *T { return &v }

// server registers a server pointing at the fake.
func (h *harness) server(name string, mut ...func(*ServerInput)) *ServerRow {
	h.t.Helper()
	in := ServerInput{Name: name, BaseURL: h.fake.URL}
	for _, m := range mut {
		m(&in)
	}
	row, err := h.svc.CreateServer(context.Background(), in)
	if err != nil {
		h.t.Fatalf("create server: %v", err)
	}
	return row
}

func (h *harness) sync(id uint, overwrite bool) []Tool {
	h.t.Helper()
	tools, err := h.svc.SyncServer(context.Background(), id, overwrite)
	if err != nil {
		h.t.Fatalf("sync: %v", err)
	}
	return tools
}

func (h *harness) toolByName(serverID uint, name string) Tool {
	h.t.Helper()
	var tool Tool
	if err := h.db.Where("server_id = ? AND name = ?", serverID, name).First(&tool).Error; err != nil {
		h.t.Fatalf("tool %q: %v", name, err)
	}
	return tool
}

func (h *harness) link(projectID, toolID uint) {
	h.t.Helper()
	if err := h.db.Exec("INSERT INTO chat_conversation_project_mcp_tools (project_id, tool_id, sort_order) VALUES (?, ?, 1)", projectID, toolID).Error; err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) linkCount() int64 {
	h.t.Helper()
	var n int64
	if err := h.db.Raw("SELECT COUNT(*) FROM chat_conversation_project_mcp_tools").Scan(&n).Error; err != nil {
		h.t.Fatal(err)
	}
	return n
}

var searchSchema = map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}

var imageSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"image":  map[string]any{"type": "string"},
		"prompt": map[string]any{"type": "string"},
	},
	"required": []any{"image"},
}

// toolOf builds a fake tools/list entry.
func toolOf(name, title, description string, schema any) map[string]any {
	return mcptest.Tool(name, title, description, schema)
}

func apperrFind(err error) (*apperr.Error, bool) { return apperr.Find(err) }

func migrateAgain(h *harness) error {
	return migrate.Run(context.Background(), h.db, nil, migrate.Set{Area: "mcp", Steps: Migrations()})
}

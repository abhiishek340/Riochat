package announcement_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/announcement"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestConcurrentEditsAreSerialized(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	const workers = 12
	var wg sync.WaitGroup
	codes := make([]int, workers)
	versions := make([]string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := s.env.Do("PATCH", fmt.Sprintf("%s/%d", adminPath, it.ID), s.adm(), map[string]any{"priority": i})
			codes[i] = w.Code
			var out struct {
				Data wrapped `json:"data"`
			}
			_ = jsonDecode(w.Body.Bytes(), &out)
			versions[i] = out.Data.Announcement.UpdatedAt
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range codes {
		if codes[i] != 200 {
			t.Fatalf("worker %d: status %d", i, codes[i])
		}
		if seen[versions[i]] {
			t.Fatalf("two edits produced the same version %s", versions[i])
		}
		seen[versions[i]] = true
	}
}

func TestConcurrentChoicesAndEditsNeverFail(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	var wg sync.WaitGroup
	results := make(chan int, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			w := s.act(uint(i%4+1), "dismiss-today", it.ID, it.UpdatedAt)
			results <- w.Code
		}(i)
		go func(i int) {
			defer wg.Done()
			w := s.env.Do("PATCH", fmt.Sprintf("%s/%d", adminPath, it.ID), s.adm(), map[string]any{"priority": i})
			results <- w.Code
		}(i)
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != 200 && code != 404 {
			t.Fatalf("unexpected status %d", code)
		}
	}
	// Whatever interleaving happened, at most one row per (user, version).
	var rows []announcement.UserState
	s.env.DB.Find(&rows)
	seen := map[string]bool{}
	for _, r := range rows {
		k := fmt.Sprint(r.UserID, "/", r.AnnouncementUpdatedAt.UnixNano())
		if seen[k] {
			t.Fatalf("duplicate state row %s", k)
		}
		seen[k] = true
	}
}

func TestConcurrentRepeatedDismissSingleRow(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := s.act(1, "close", it.ID, it.UpdatedAt); w.Code != 200 {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	if n := countStates(t, s.env, it.ID, 1); n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	db := dbtest.New(t)
	set := migrate.Set{Area: "announcement", Steps: announcement.Migrations()}
	for i := 0; i < 3; i++ {
		if err := migrate.Run(context.Background(), db, logx.Nop(), set); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var names []string
	db.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name IN ('system_announcements','announcement_user_states')").Scan(&names)
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{
		"idx_system_announcements_listing", "idx_announcement_user_states_version",
		"idx_announcement_user_states_dismissed", "idx_announcement_user_states_closed",
		"idx_system_announcements_status", "idx_system_announcements_type", "idx_system_announcements_pinned",
		"idx_system_announcements_priority", "idx_system_announcements_starts_at", "idx_system_announcements_expires_at",
		"idx_system_announcements_created_by_user_id", "idx_system_announcements_deleted_at",
	} {
		if !have[want] {
			t.Errorf("index %s missing; have %v", want, names)
		}
	}
}

func TestUpgradeOfLegacySchemaKeepsData(t *testing.T) {
	db := dbtest.New(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if err := db.Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A table as created before type, pinned and the user-state table existed.
	exec(`CREATE TABLE system_announcements (
		id integer primary key autoincrement, created_at datetime, updated_at datetime, deleted_at datetime,
		title varchar(120) not null default '', content_markdown text not null default '',
		status varchar(32) not null default 'active', priority integer not null default 0,
		starts_at datetime, expires_at datetime, created_by_user_id integer not null default 0)`)
	exec(`INSERT INTO system_announcements (created_at, updated_at, title, content_markdown, status, priority, created_by_user_id)
		VALUES ('2025-01-01 10:00:00+00:00', '2025-01-02 10:00:00.123456789+00:00', 'old', 'legacy body', 'active', 4, 9)`)

	set := migrate.Set{Area: "announcement", Steps: announcement.Migrations()}
	for i := 0; i < 2; i++ {
		if err := migrate.Run(context.Background(), db, logx.Nop(), set); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var row announcement.Announcement
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Title != "old" || row.ContentMarkdown != "legacy body" || row.Priority != 4 || row.CreatedByUserID != 9 {
		t.Fatalf("data lost: %+v", row)
	}
	if row.Type != "general" || row.Pinned {
		t.Fatalf("new columns need their defaults: type=%q pinned=%v", row.Type, row.Pinned)
	}
}

func TestLegacyRowWithNanosecondVersionWorksEndToEnd(t *testing.T) {
	s := newSuite(t)
	legacy := announcement.Announcement{Title: "legacy", ContentMarkdown: "x", Status: "active", Type: "info", CreatedByUserID: 3}
	legacy.CreatedAt = baseTime.Add(-time.Hour)
	legacy.UpdatedAt = time.Date(2026, 2, 28, 10, 0, 0, 123456789, time.FixedZone("x", 8*3600))
	if err := s.env.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	got := s.list(1, "")
	if len(got) != 1 {
		t.Fatalf("list: %+v", got)
	}
	// Whatever precision the row has, the client echoes the served value back.
	testkit.Expect(t, s.dismiss(1, got[0]), 200, "")
	testkit.Expect(t, s.closeIt(1, got[0]), 200, "")
	sameIDs(t, s.list(1, ""))
	if g := s.list(1, "?include_dismissed=true"); len(g) != 1 || g[0].ClosedAt == nil {
		t.Fatalf("after choices: %+v", g)
	}
}

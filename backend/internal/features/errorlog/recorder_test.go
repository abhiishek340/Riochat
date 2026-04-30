package errorlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

type harness struct {
	db   *gorm.DB
	rec  *recorder
	clk  *clock.Fake
	logs *bytes.Buffer
	wg   sync.WaitGroup
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := dbtest.New(t)
	if err := db.AutoMigrate(&Entry{}); err != nil {
		t.Fatal(err)
	}
	h := &harness{db: db, clk: clock.NewFake(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)), logs: &bytes.Buffer{}}
	h.rec = newRecorder(db, logx.New(h.logs, logx.LevelDebug), h.clk,
		func(ctx context.Context, timeout time.Duration, fn func(context.Context)) {
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				defer cancel()
				fn(dctx)
			}()
		})
	return h
}

func (h *harness) count(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := h.db.Model(&Entry{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordNormalizesAndSurvivesCancelledRequest(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.rec.RecordError(ctx, middleware.ErrorRecord{
		RequestID: " req-1 ",
		Status:    500,
		Cause:     strings.Repeat("c", 4196),
		UserAgent: strings.Repeat("u", 400),
	})
	cancel()
	h.wg.Wait()

	var rows []Entry
	h.db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows: %d", len(rows))
	}
	e := rows[0]
	if e.RequestID != "req-1" || len(e.Cause) != 4096 || len(e.UserAgent) != 255 {
		t.Fatalf("not normalized: id=%q cause=%d ua=%d", e.RequestID, len(e.Cause), len(e.UserAgent))
	}
	if e.CreatedAt.IsZero() || !e.CreatedAt.Equal(h.clk.Now()) {
		t.Fatalf("created_at should default to now, got %v", e.CreatedAt)
	}
}

func TestRecordSkipsBlankRequestID(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"", "   ", "\t\n"} {
		h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: id, Status: 500})
	}
	h.wg.Wait()
	if n := h.count(t); n != 0 {
		t.Fatalf("rows: %d", n)
	}
	if h.logs.Len() != 0 {
		t.Fatalf("dropping a blank id must be silent: %s", h.logs)
	}
}

func TestClipLimitsAndUTF8Boundaries(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"short", "abc", 10, "abc"},
		{"trims", "  abc \n", 10, "abc"},
		{"exact", "abcd", 4, "abcd"},
		{"ascii cut", "abcdef", 4, "abcd"},
		{"two byte straddle", "aé", 2, "a"},
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		{"three byte straddle", "ab\u4e16", 4, "ab"},
		{"three byte fits", "ab\u4e16", 5, "ab\u4e16"},
		{"four byte straddle", "😀😀", 6, "😀"},
		{"empty", "   ", 5, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clip(tc.in, tc.limit)
			if got != tc.want || !utf8.ValidString(got) {
				t.Fatalf("clip(%q,%d)=%q want %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}

func TestEveryFieldHasItsLimit(t *testing.T) {
	h := newHarness(t)
	long := strings.Repeat("é", 20000) // 40000 bytes, two-byte characters
	e, ok := h.rec.normalize(middleware.ErrorRecord{
		RequestID: long, TraceID: long, Method: long, Route: long, Path: long,
		ErrorCode: long, Cause: long, Detail: long, ClientIP: long, UserAgent: long,
	})
	if !ok {
		t.Fatal("record rejected")
	}
	limits := map[string]struct {
		got string
		max int
	}{
		"request": {e.RequestID, 64}, "trace": {e.TraceID, 64}, "method": {e.Method, 16},
		"route": {e.Route, 255}, "path": {e.Path, 512}, "code": {e.ErrorCode, 128},
		"cause": {e.Cause, 4096}, "detail": {e.Detail, 16384}, "ip": {e.ClientIP, 64},
		"ua": {e.UserAgent, 255},
	}
	for name, l := range limits {
		if len(l.got) > l.max || len(l.got) < l.max-3 || !utf8.ValidString(l.got) {
			t.Errorf("%s: len %d (max %d) valid=%v", name, len(l.got), l.max, utf8.ValidString(l.got))
		}
	}
}

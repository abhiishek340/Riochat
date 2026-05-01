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

func TestRecorderMasksSecretsAgain(t *testing.T) {
	h := newHarness(t)
	e, _ := h.rec.normalize(middleware.ErrorRecord{
		RequestID: "r",
		Cause:     "call failed password=hunter2",
		Detail:    `{"error":{"message":"model not found","api_key=sk-live-123"}}`,
	})
	if strings.Contains(e.Cause, "hunter2") || !strings.Contains(e.Cause, "password=REDACTED") {
		t.Fatalf("cause: %q", e.Cause)
	}
	if !strings.Contains(e.Detail, "model not found") || strings.Contains(e.Detail, "sk-live-123") {
		t.Fatalf("detail: %q", e.Detail)
	}
}

func TestExplicitOccurrenceTimeIsKept(t *testing.T) {
	h := newHarness(t)
	at := time.Date(2025, 12, 24, 10, 0, 0, 0, time.FixedZone("x", 3600))
	h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "r", Status: 500, At: at})
	h.wg.Wait()
	var e Entry
	h.db.First(&e)
	if !e.CreatedAt.Equal(at) {
		t.Fatalf("created_at %v want %v", e.CreatedAt, at)
	}
}

func TestSameRequestIDMayRepeat(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 2; i++ {
		h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "dup", Status: 500})
	}
	h.wg.Wait()
	if n := h.count(t); n != 2 {
		t.Fatalf("rows: %d", n)
	}
}

func TestConcurrencyCapDropsExcess(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	var started atomic.Int32
	real := h.rec.insert
	h.rec.insert = func(ctx context.Context, e *Entry) error {
		started.Add(1)
		<-release
		return real(ctx, e)
	}
	for i := 0; i < 37; i++ {
		h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: fmt.Sprintf("r-%d", i), Status: 500})
	}
	if got := h.rec.Dropped(); got != 5 {
		t.Fatalf("dropped %d want 5", got)
	}
	// While saturated, the next record is dropped too.
	h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "extra", Status: 500})
	if got := h.rec.Dropped(); got != 6 {
		t.Fatalf("dropped %d want 6", got)
	}
	close(release)
	h.wg.Wait()
	if n := h.count(t); n != 32 {
		t.Fatalf("rows %d want 32", n)
	}
	// Slots are free again.
	h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "after", Status: 500})
	h.wg.Wait()
	if n := h.count(t); n != 33 {
		t.Fatalf("rows after recovery %d want 33", n)
	}
	if got := strings.Count(h.logs.String(), "error_log_dropped"); got != 1 {
		t.Fatalf("expected a single drop warning (first drop only), got %d", got)
	}
	if !strings.Contains(h.logs.String(), "r-32") {
		t.Fatalf("drop warning should name the request: %s", h.logs)
	}
}

func TestDropWarningCadence(t *testing.T) {
	h := newHarness(t)
	block := make(chan struct{})
	h.rec.insert = func(ctx context.Context, e *Entry) error { <-block; return nil }
	for i := 0; i < maxInFlight+250; i++ {
		h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: fmt.Sprintf("r-%d", i), Status: 500})
	}
	close(block)
	h.wg.Wait()
	if h.rec.Dropped() != 250 {
		t.Fatalf("dropped %d", h.rec.Dropped())
	}
	// Drops 1, 100 and 200 warn.
	if got := strings.Count(h.logs.String(), "error_log_dropped"); got != 3 {
		t.Fatalf("warnings %d want 3\n%s", got, h.logs)
	}
	if !strings.Contains(h.logs.String(), "dropped_total 200") {
		t.Fatalf("missing cumulative count: %s", h.logs)
	}
}

func TestFailedWriteIsLoggedAndSlotFreed(t *testing.T) {
	h := newHarness(t)
	h.rec.insert = func(ctx context.Context, e *Entry) error { return errors.New("disk on fire") }
	for i := 0; i < maxInFlight*3; i++ {
		h.rec.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "bad", Status: 500})
		h.wg.Wait()
	}
	if h.rec.Dropped() != 0 {
		t.Fatalf("failed writes must release their slot; dropped %d", h.rec.Dropped())
	}
	if !strings.Contains(h.logs.String(), "error_log_write_failed") || !strings.Contains(h.logs.String(), "bad") {
		t.Fatalf("warning missing: %s", h.logs)
	}
}

func TestWriteHasFiveSecondTimeoutContext(t *testing.T) {
	var gotTimeout time.Duration
	r := newRecorder(nil, logx.Nop(), clock.System{}, func(ctx context.Context, timeout time.Duration, fn func(context.Context)) {
		gotTimeout = timeout
	})
	r.RecordError(context.Background(), middleware.ErrorRecord{RequestID: "r", Status: 500})
	if gotTimeout != 5*time.Second {
		t.Fatalf("timeout %v", gotTimeout)
	}
}

func TestPurgeBeforeIsStrict(t *testing.T) {
	h := newHarness(t)
	cut := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{cut.Add(-time.Hour), cut.Add(-time.Nanosecond), cut, cut.Add(time.Hour)} {
		h.db.Create(&Entry{RequestID: fmt.Sprint("r", i), CreatedAt: at})
	}
	n, err := PurgeBefore(context.Background(), h.db, cut.In(time.FixedZone("z", 7200)))
	if err != nil || n != 2 {
		t.Fatalf("purged %d err %v", n, err)
	}
	if left := h.count(t); left != 2 {
		t.Fatalf("left %d", left)
	}
	var older int64
	h.db.Model(&Entry{}).Where("created_at < ?", cut).Count(&older)
	if older != 0 {
		t.Fatal("older rows survived")
	}
}

func TestPathQueryIsMaskedByRecorder(t *testing.T) {
	h := newHarness(t)
	h.rec.RecordError(context.Background(), middleware.ErrorRecord{
		RequestID: "r-path", Status: 500,
		Path: "/api/x?Token=abc&a=1&flag&api_key=zzz",
	})
	h.wg.Wait()
	var e Entry
	if err := h.db.Where("request_id = ?", "r-path").First(&e).Error; err != nil {
		t.Fatal(err)
	}
	if e.Path != "/api/x?Token=REDACTED&a=1&flag&api_key=REDACTED" {
		t.Fatalf("path %q", e.Path)
	}
}

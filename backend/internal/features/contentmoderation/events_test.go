package contentmoderation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/secretbox"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func (h *harness) insertEvent(e Event) Event {
	h.t.Helper()
	if e.PublicID == "" {
		e.PublicID = newPublicID()
	}
	if e.CategoriesJSON == "" {
		e.CategoriesJSON = "[]"
	}
	if e.CategoryScoresJSON == "" {
		e.CategoryScoresJSON = "{}"
	}
	if e.ContentLocationJSON == "" {
		e.ContentLocationJSON = "{}"
	}
	if e.ImageMetaJSON == "" {
		e.ImageMetaJSON = "[]"
	}
	now := h.env.Clock.Now()
	if e.ContentExpiresAt.IsZero() {
		e.ContentExpiresAt = now.Add(30 * 24 * time.Hour)
	}
	if e.MetadataExpiresAt.IsZero() {
		e.MetadataExpiresAt = now.Add(90 * 24 * time.Hour)
	}
	if err := h.env.DB.Create(&e).Error; err != nil {
		h.t.Fatalf("insert event: %v", err)
	}
	return e
}

type listData struct {
	Items    []map[string]any `json:"items"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
}

func (h *harness) list(query string) listData {
	h.t.Helper()
	w := h.do("GET", "/events"+query, nil)
	testkit.Expect(h.t, w, 200, "")
	var d listData
	testkit.Decode(h.t, w, &d)
	return d
}

func ids(d listData) []string {
	out := []string{}
	for _, it := range d.Items {
		out = append(out, it["publicID"].(string))
	}
	return out
}

func seedEventList(h *harness) {
	h.users.labels[7] = UserLabel{Label: "Alice A.", Username: "alice"}
	h.users.names["alice"] = 7
	h.insertEvent(Event{PublicID: "cme_aaa", UserID: 7, ConversationID: 1, RunID: "run-1", MessagePublicID: "msg_1", Direction: "input",
		Modality: "text", ModelName: "m-a", Result: "hit", CategoriesJSON: `["hate","sexual"]`, ContentSummary: "input_text_hit",
		EncryptedText: "secret-cipher", CategoryScoresJSON: `{"sexual":0.9}`})
	h.insertEvent(Event{PublicID: "cme_bbb", UserID: 8, RunID: "run-2", Direction: "output", Modality: "text", ModelName: "m-b",
		Result: "hit", CategoriesJSON: `["sexual/minors"]`, ContentSummary: "output_text_hit"})
	h.insertEvent(Event{PublicID: "cme_ccc", UserID: 7, RunID: "run-3", Direction: "output", Modality: "image", ModelName: "m-b",
		Result: "failed_open", ErrorCode: "timeout", ErrorMessage: "content moderation timed out"})
	h.insertEvent(Event{PublicID: "cme_ddd", UserID: 9, RunID: "run-4", Direction: "input", Modality: "image", ModelName: "m-a",
		Result: "passed", ContentSummary: "image_pass"})
}

func TestEventListDefaultsAndShape(t *testing.T) {
	h := newHarness(t)
	seedEventList(h)
	d := h.list("")
	if d.Total != 4 || d.Page != 1 || d.PageSize != 20 || len(d.Items) != 4 {
		t.Fatalf("page %+v", d)
	}
	if got := ids(d); strings.Join(got, ",") != "cme_ddd,cme_ccc,cme_bbb,cme_aaa" {
		t.Fatalf("newest first expected: %v", got)
	}
	first := d.Items[3] // cme_aaa
	for _, k := range []string{"publicID", "userID", "conversationID", "runID", "messagePublicID", "direction", "modality", "model",
		"policyVersion", "result", "categories", "latencyMS", "errorCode", "errorMessage", "contentSummary", "createdAt"} {
		if _, ok := first[k]; !ok {
			t.Errorf("item lacks %q", k)
		}
	}
	if first["userLabel"] != "Alice A." || first["username"] != "alice" {
		t.Fatalf("labels %v / %v", first["userLabel"], first["username"])
	}
	if _, ok := d.Items[0]["userLabel"]; ok {
		t.Fatal("unresolved users must lack a label")
	}
	raw, _ := json.Marshal(d)
	for _, forbidden := range []string{"secret-cipher", "encryptedText", "categoryScores", "storagePath", "imageMeta", "0.9"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("list leaks %q", forbidden)
		}
	}
	cats, ok := d.Items[0]["categories"].([]any)
	if !ok || len(cats) != 0 {
		t.Fatalf("categories must be a non-null array: %v", d.Items[0]["categories"])
	}
}

func TestEventListPagination(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 7; i++ {
		h.insertEvent(Event{Direction: "input", Modality: "text", Result: "passed"})
	}
	d := h.list("?page=2&page_size=3")
	if d.Total != 7 || d.Page != 2 || d.PageSize != 3 || len(d.Items) != 3 {
		t.Fatalf("%+v", d)
	}
	if d = h.list("?page=9&page_size=3"); len(d.Items) != 0 || d.Items == nil {
		t.Fatalf("beyond the end: %+v", d)
	}
	if d = h.list("?page_size=5000"); d.PageSize != 1000 {
		t.Fatalf("clamp: %d", d.PageSize)
	}
	if d = h.list("?page_size=abc&page=-3"); d.PageSize != 20 || d.Page != 1 {
		t.Fatalf("defaults: %+v", d)
	}
}

func TestEventListQueryIsExactMatch(t *testing.T) {
	h := newHarness(t)
	seedEventList(h)
	cases := map[string][]string{
		"?query=cme_aaa":        {"cme_aaa"},
		"?query=cme_a":          {},
		"?query=run-2":          {"cme_bbb"},
		"?query=msg_1":          {"cme_aaa"},
		"?query=hit":            {"cme_bbb", "cme_aaa"},
		"?query=HIT":            {"cme_bbb", "cme_aaa"},
		"?query=input":          {"cme_ddd", "cme_aaa"},
		"?query=image":          {"cme_ddd", "cme_ccc"},
		"?query=m-a":            {"cme_ddd", "cme_aaa"},
		"?query=m":              {},
		"?query=timeout":        {"cme_ccc"},
		"?query=input_text_hit": {"cme_aaa"},
		"?query=ALICE":          {"cme_ccc", "cme_aaa"},
		"?query=ali":            {},
		"?query=9":              {"cme_ddd"},
		"?query=%20cme_bbb%20":  {"cme_bbb"},
	}
	for q, want := range cases {
		if got := ids(h.list(q)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", q, got, want)
		}
	}
}

func TestEventListFilters(t *testing.T) {
	h := newHarness(t)
	seedEventList(h)
	cases := map[string][]string{
		"?result=hit":                    {"cme_bbb", "cme_aaa"},
		"?result=failed_open":            {"cme_ccc"},
		"?result=passed":                 {"cme_ddd"},
		"?direction=output":              {"cme_ccc", "cme_bbb"},
		"?modality=image":                {"cme_ddd", "cme_ccc"},
		"?user_id=7":                     {"cme_ccc", "cme_aaa"},
		"?run_id=run-4":                  {"cme_ddd"},
		"?category=sexual":               {"cme_aaa"}, // never matches sexual/minors
		"?category=sexual/minors":        {"cme_bbb"},
		"?category=hate":                 {"cme_aaa"},
		"?category=violence":             {},
		"?direction=input&modality=text": {"cme_aaa"},
	}
	for q, want := range cases {
		if got := ids(h.list(q)); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", q, got, want)
		}
	}
}

func TestEventListTimeBoundsAreInclusive(t *testing.T) {
	h := newHarness(t)
	base := h.env.Clock.Now()
	for i, name := range []string{"cme_t0", "cme_t1", "cme_t2"} {
		e := h.insertEvent(Event{PublicID: name, Direction: "input", Modality: "text", Result: "passed"})
		stamp := base.Add(time.Duration(i) * time.Hour)
		h.env.DB.Model(&Event{}).Where("id = ?", e.ID).Update("created_at", stamp)
	}
	from, to := base.Add(time.Hour).Format(time.RFC3339), base.Add(2*time.Hour).Format(time.RFC3339)
	if got := ids(h.list("?from=" + from + "&to=" + to)); strings.Join(got, ",") != "cme_t2,cme_t1" {
		t.Fatalf("both ends inclusive: %v", got)
	}
	if got := ids(h.list("?from=" + to)); strings.Join(got, ",") != "cme_t2" {
		t.Fatalf("from only: %v", got)
	}
	if got := ids(h.list("?to=" + base.Format(time.RFC3339))); strings.Join(got, ",") != "cme_t0" {
		t.Fatalf("to only: %v", got)
	}
}

func TestEventListRejectsBadFilters(t *testing.T) {
	h := newHarness(t)
	seedEventList(h)
	for _, q := range []string{"?user_id=0", "?user_id=abc", "?user_id=-1", "?user_id=1.5", "?user_id=99999999999999999999999"} {
		testkitExpect(t, h.do("GET", "/events"+q, nil), 400, "request.invalid_userid")
	}
	w := h.do("GET", "/events?user_id=abc", nil)
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid userid" {
		t.Fatalf("message %q", msg)
	}
	for _, q := range []string{"?direction=sideways", "?modality=audio", "?result=maybe", "?category=bogus", "?from=yesterday", "?to=2026",
		"?from=2026-03-02T00:00:00Z&to=2026-03-01T00:00:00Z", "?query=" + strings.Repeat("a", 201)} {
		w := h.do("GET", "/events"+q, nil)
		testkitExpect(t, w, 400, "request.invalid_query")
	}
	if msg := testkit.Envelope(t, h.do("GET", "/events?direction=x", nil)).ErrorMsg; msg != "invalid query parameter" {
		t.Fatalf("message %q", msg)
	}
	// 200 characters are fine, and the limit counts characters, not bytes.
	testkit.Expect(t, h.do("GET", "/events?query="+strings.Repeat("a", 200), nil), 200, "")
	testkit.Expect(t, h.do("GET", "/events?query="+strings.Repeat("%E6%BC%A2", 200), nil), 200, "")
}

func TestEventDetailForUnknownAndExpiredEvents(t *testing.T) {
	h := newHarness(t)
	testkitExpect(t, h.do("GET", "/events/cme_missing", nil), 404, "content_moderation.event_not_found")
	if len(h.audit.list()) != 0 {
		t.Fatal("a missing event is not audited")
	}

	ct, err := h.svc.secrets.EncryptBound("flagged words", textBinding("cme_exp"))
	if err != nil {
		t.Fatal(err)
	}
	h.insertEvent(Event{PublicID: "cme_exp", Direction: "output", Modality: "text", Result: "hit", CategoriesJSON: `["hate"]`,
		CategoryScoresJSON: `{"hate":0.5}`, EncryptedText: ct})
	var d struct {
		DecryptedText string             `json:"decryptedText"`
		TextAvailable bool               `json:"textAvailable"`
		Scores        map[string]float64 `json:"categoryScores"`
		Images        []any              `json:"images"`
	}
	w := h.do("GET", "/events/cme_exp", nil)
	decodeData(t, w.Body.Bytes(), &d)
	if !d.TextAvailable || d.DecryptedText != "flagged words" || d.Scores["hate"] != 0.5 || d.Images == nil {
		t.Fatalf("fresh: %+v", d)
	}
	h.env.Clock.Advance(31 * 24 * time.Hour)
	w = h.do("GET", "/events/cme_exp", nil)
	d = struct {
		DecryptedText string             `json:"decryptedText"`
		TextAvailable bool               `json:"textAvailable"`
		Scores        map[string]float64 `json:"categoryScores"`
		Images        []any              `json:"images"`
	}{}
	decodeData(t, w.Body.Bytes(), &d)
	if w.Code != 200 || d.TextAvailable || d.DecryptedText != "" {
		t.Fatalf("expired: %d %+v", w.Code, d)
	}
	if strings.Contains(w.Body.String(), "decryptedText") {
		t.Fatal("no decryptedText key once expired")
	}
	if n := len(h.audit.list()); n != 2 {
		t.Fatalf("audited views: %d", n)
	}
	if a := h.audit.list()[1]; a.Detail["retainedTextAvailable"] != false {
		t.Fatalf("expired view must audit retainedTextAvailable=false: %+v", a)
	}
}

func TestEventDetailTextAvailabilityRules(t *testing.T) {
	h := newHarness(t)
	ct, _ := h.svc.secrets.EncryptBound("secret words", textBinding("cme_a"))
	otherCt, _ := h.svc.secrets.EncryptBound("secret words", textBinding("cme_other"))
	cases := []struct {
		name  string
		event Event
		want  bool
	}{
		{"hit text", Event{PublicID: "cme_a", Result: "hit", Modality: "text", EncryptedText: ct}, true},
		{"passed event never has text", Event{PublicID: "cme_b", Result: "passed", Modality: "text", EncryptedText: ct}, false},
		{"image modality", Event{PublicID: "cme_c", Result: "hit", Modality: "image", EncryptedText: ct}, false},
		{"empty ciphertext", Event{PublicID: "cme_d", Result: "hit", Modality: "text"}, false},
		{"ciphertext bound to another event", Event{PublicID: "cme_e", Result: "hit", Modality: "text", EncryptedText: otherCt}, false},
		{"undecryptable", Event{PublicID: "cme_f", Result: "hit", Modality: "text", EncryptedText: "enc:garbage"}, false},
	}
	for _, tc := range cases {
		e := tc.event
		e.Direction = "input"
		h.insertEvent(e)
		w := h.do("GET", "/events/"+e.PublicID, nil)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", tc.name, w.Code)
		}
		var d struct {
			TextAvailable bool `json:"textAvailable"`
		}
		decodeData(t, w.Body.Bytes(), &d)
		if d.TextAvailable != tc.want {
			t.Errorf("%s: textAvailable %v", tc.name, d.TextAvailable)
		}
	}
}

func TestAuditFailureDoesNotFailTheRequest(t *testing.T) {
	h := newHarness(t)
	h.insertEvent(Event{PublicID: "cme_x", Direction: "input", Modality: "text", Result: "passed"})
	h.audit.fail = true
	testkit.Expect(t, h.do("GET", "/events/cme_x", nil), 200, "")
	if !strings.Contains(h.env.Logs.String(), "content_moderation_audit_failed") {
		t.Fatal("audit failure should be logged")
	}
	h.svc.audit = nil
	testkit.Expect(t, h.do("GET", "/events/cme_x", nil), 200, "")
}

func TestEventStorageErrorsAreNotNotFound(t *testing.T) {
	h := newHarness(t)
	if err := h.env.DB.Migrator().DropTable(&Event{}); err != nil {
		t.Fatal(err)
	}
	testkitExpect(t, h.do("GET", "/events/cme_x", nil), 500, "internal.error")
	testkitExpect(t, h.do("GET", "/events/cme_x/images/0", nil), 500, "internal.error")
}

// ---- statistics -----------------------------------------------------------------------

type statsData struct {
	Items []StatItem `json:"items"`
}

func (h *harness) stats(query string, headers map[string]string) (statsData, int) {
	h.t.Helper()
	if headers == nil {
		headers = h.super()
	}
	w := h.env.Do("GET", "/api/v1/admin/content-moderation/stats"+query, headers, nil)
	var d statsData
	if w.Code == 200 {
		testkit.Decode(h.t, w, &d)
	}
	return d, w.Code
}

func TestStatsUpsertIsAdditive(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := statDeltasFor("input", "text", ResultHit, 1, 30, []string{"hate", "violence"})
	for i := 0; i < 2; i++ {
		if err := h.svc.addStats(ctx, d...); err != nil {
			t.Fatal(err)
		}
	}
	var rows []DailyStat
	h.env.DB.Order("category ASC").Find(&rows)
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	agg := rows[0]
	if agg.Category != "" || agg.CheckCount != 2 || agg.HitCount != 2 || agg.ContentItems != 2 || agg.LatencySumMS != 60 || agg.LatencyCount != 2 {
		t.Fatalf("aggregate %+v", agg)
	}
	if rows[1].Category != "hate" || rows[1].HitCount != 2 || rows[1].CheckCount != 0 || rows[1].LatencyCount != 0 {
		t.Fatalf("category row %+v", rows[1])
	}
	if !agg.StatDate.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("stat date %v", agg.StatDate)
	}
}

func TestStatsDeltaShapes(t *testing.T) {
	d := statDeltasFor("output", "image", ResultFailedOpen, 3, 0, nil)
	if len(d) != 1 || d[0].Failures != 1 || d[0].Items != 3 || d[0].LatencyCount != 0 || d[0].Hits != 0 {
		t.Fatalf("%+v", d)
	}
	d = statDeltasFor("output", "text", ResultPassed, 0, 12, nil)
	if d[0].Items != 1 || d[0].LatencyCount != 1 || d[0].LatencySum != 12 {
		t.Fatalf("unknown item counts default to one: %+v", d)
	}
}

func TestStatsEndpoint(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	day := func(offset int) { h.env.Clock.Set(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, offset)) }
	day(0)
	_ = h.svc.addStats(ctx, statDeltasFor("output", "text", ResultPassed, 1, 5, nil)...)
	_ = h.svc.addStats(ctx, statDeltasFor("input", "text", ResultHit, 1, 5, []string{"hate"})...)
	day(1)
	_ = h.svc.addStats(ctx, statDeltasFor("input", "image", ResultFailedOpen, 2, 0, nil)...)
	day(2)

	d, code := h.stats("", nil)
	if code != 200 || len(d.Items) != 4 {
		t.Fatalf("default window: %d %+v", code, d)
	}
	// Ascending by date, then direction, modality, result, category.
	var order []string
	for _, it := range d.Items {
		order = append(order, fmt.Sprintf("%s/%s/%s/%s/%s", it.StatDate.Format("01-02"), it.Direction, it.Modality, it.Result, it.Category))
	}
	want := "03-01/input/text/hit/,03-01/input/text/hit/hate,03-01/output/text/passed/,03-02/input/image/failed_open/"
	if strings.Join(order, ",") != want {
		t.Fatalf("order\n got %s\nwant %s", strings.Join(order, ","), want)
	}
	if it := d.Items[0]; it.CheckCount != 1 || it.HitCount != 1 || it.LatencySumMS != 5 || it.LatencyCount != 1 {
		t.Fatalf("counters %+v", it)
	}
	if !d.Items[0].StatDate.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("statDate %v", d.Items[0].StatDate)
	}

	// Day granularity: a mid-day "from" still includes that day.
	if d, _ = h.stats("?from=2026-03-02T23:59:00Z", nil); len(d.Items) != 1 {
		t.Fatalf("from mid-day: %+v", d)
	}
	if d, _ = h.stats("?to=2026-03-01T00:00:01Z", nil); len(d.Items) != 3 {
		t.Fatalf("to: %+v", d)
	}
	if d, _ = h.stats("?from=2026-03-01T10:00:00Z&to=2026-03-01T09:00:00Z", nil); len(d.Items) != 3 {
		// same UTC day is not a reversed range
		t.Fatalf("same-day range: %+v", d)
	}
	// Reversed and malformed ranges.
	w := h.do("GET", "/stats?from=2026-03-02T00:00:00Z&to=2026-03-01T00:00:00Z", nil)
	testkitExpect(t, w, 400, "request.invalid_query")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid query parameter" {
		t.Fatalf("message %q", msg)
	}
	w = h.do("GET", "/stats?from=bogus", nil)
	testkitExpect(t, w, 400, "request.invalid_query")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid from" {
		t.Fatalf("message %q", msg)
	}
	w = h.do("GET", "/stats?to=bogus", nil)
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid to" {
		t.Fatalf("message %q", msg)
	}

	// Plain administrators may read statistics; ordinary users may not.
	if _, code := h.stats("", h.env.Bearer(testkit.Admin(2))); code != 200 {
		t.Fatalf("admin: %d", code)
	}
	if _, code := h.stats("", h.env.Bearer(testkit.User(3))); code != 403 {
		t.Fatalf("user: %d", code)
	}
	w = h.env.Do("GET", "/api/v1/admin/content-moderation/stats", h.env.Bearer(testkit.User(3)), nil)
	testkit.Expect(t, w, 403, "auth.admin_required")
}

func TestStatsWindowIsClampedTo90Days(t *testing.T) {
	h := newHarness(t)
	now := h.env.Clock.Now()
	old := now.AddDate(0, 0, -100)
	edge := now.AddDate(0, 0, -90)
	for _, d := range []time.Time{old, edge, now} {
		h.env.DB.Create(&DailyStat{StatDate: utcDay(d), Direction: "input", Modality: "text", Result: "passed", CheckCount: 1})
	}
	d, code := h.stats("", nil)
	if code != 200 || len(d.Items) != 2 {
		t.Fatalf("default 90 day window: %d %+v", code, d)
	}
	d, code = h.stats("?from="+old.Format(time.RFC3339), nil)
	if code != 200 || len(d.Items) != 2 {
		t.Fatalf("an older from is clamped: %d %+v", code, d)
	}
	// "to" is compared with the clamped "from": a range that ends before the
	// 90 day floor is therefore reversed after clamping and answers 400.
	d, code = h.stats("?from="+old.Add(-48*time.Hour).Format(time.RFC3339)+"&to="+old.Format(time.RFC3339), nil)
	if code != 400 {
		t.Fatalf("old range: %d %+v", code, d)
	}
}

// ---- recovery of stuck runs ---------------------------------------------------------------

func TestStaleRunWithHitIsBlocked(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.insertEvent(Event{PublicID: "cme_old", RunID: "stale-1", UserID: 5, Direction: "output", Modality: "text", Result: "hit",
		CategoriesJSON: `["violence"]`})
	h.insertEvent(Event{PublicID: "cme_pass", RunID: "stale-1", UserID: 5, Direction: "input", Modality: "text", Result: "passed"})
	h.runs.stale = []RunRef{{RunID: "stale-1", UserID: 5}}
	if err := h.svc.RecoverStaleRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.runs.blockCount() != 1 {
		t.Fatalf("blocks %d", h.runs.blockCount())
	}
	spec := h.runs.blocks[0]
	if spec.EventID != "cme_old" || spec.Direction != DirectionOutput || len(spec.Categories) != 1 || spec.Categories[0] != "violence" {
		t.Fatalf("spec %+v", spec)
	}
	if h.notifier.last().Type != EventTypeBlocked || h.notifier.last().EventID != "cme_old" {
		t.Fatalf("stream %+v", h.notifier.last())
	}
	// Not announced twice.
	n := len(h.notifier.types())
	_ = h.svc.RecoverStaleRuns(context.Background())
	if len(h.notifier.types()) != n {
		t.Fatal("a second recovery must not repeat the stream event")
	}
}

func TestStaleRunWithoutHitFailsOpenWithWorkerLost(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.runs.stale = []RunRef{{RunID: "stale-2", UserID: 5, ConversationID: 3, MessageID: 4, MessagePublicID: "msg_4"}}
	if err := h.svc.RecoverStaleRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.runs.state("stale-2") != StateFailedOpen {
		t.Fatalf("state %q", h.runs.state("stale-2"))
	}
	events := h.eventsFor("stale-2")
	if len(events) != 1 {
		t.Fatalf("events %d", len(events))
	}
	e := events[0]
	// Only the run id is known to recovery: no user, conversation or message ids.
	if e.Result != ResultFailedOpen || e.ErrorCode != "worker_lost" || e.Direction != DirectionOutput || e.Modality != ModalityText ||
		e.UserID != 0 || e.ConversationID != 0 || e.MessageID != 0 || e.MessagePublicID != "" || e.ModelName != "m-test" {
		t.Fatalf("event %+v", e)
	}
	// This path makes no daily-stat increment.
	if s := h.stat(DirectionOutput, ModalityText, ResultFailedOpen, ""); s.FailureCount != 0 || s.CheckCount != 0 {
		t.Fatalf("stat %+v", s)
	}
}

func TestStaleRunCutoffAndTrackedRuns(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	live := h.begin("live-run", nil)
	defer live.Close()
	h.runs.stale = []RunRef{{RunID: "live-run"}}
	if err := h.svc.RecoverRun(context.Background(), h.runs.stale[0]); err != nil {
		t.Fatal(err)
	}
	if len(h.allEvents()) != 0 || h.runs.state("live-run") != StatePending {
		t.Fatalf("a run tracked by this process must be left alone: %q", h.runs.state("live-run"))
	}
	live.Close()
	if err := h.svc.RecoverRun(context.Background(), h.runs.stale[0]); err != nil {
		t.Fatal(err)
	}
	if h.runs.state("live-run") != StateFailedOpen {
		t.Fatalf("after Close the run is recoverable: %q", h.runs.state("live-run"))
	}
}

func TestStorageFailureNeverBecomesFailedOpen(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	if err := h.env.DB.Migrator().DropTable(&Event{}); err != nil {
		t.Fatal(err)
	}
	err := h.svc.RecoverRun(context.Background(), RunRef{RunID: "unknown"})
	if err == nil {
		t.Fatal("a storage error must surface")
	}
	if h.runs.state("unknown") == StateFailedOpen {
		t.Fatal("a possibly known hit must not be converted to failed-open")
	}
}

func TestRecoveryKeepsBlockPendingWhenPersistenceFails(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.insertEvent(Event{PublicID: "cme_h", RunID: "stale-3", Direction: "input", Modality: "text", Result: "hit", CategoriesJSON: `["hate"]`})
	h.runs.failApply = 1
	if err := h.svc.RecoverRun(context.Background(), RunRef{RunID: "stale-3"}); err == nil {
		t.Fatal("expected an error while the block cannot be stored")
	}
	if h.svc.PendingBlocks() != 1 || len(h.notifier.types()) != 0 {
		t.Fatalf("pending %d events %v", h.svc.PendingBlocks(), h.notifier.types())
	}
	h.svc.RetryPendingBlocks(context.Background())
	if h.svc.PendingBlocks() != 0 || h.runs.blockCount() != 1 {
		t.Fatalf("pending %d blocks %d", h.svc.PendingBlocks(), h.runs.blockCount())
	}
}

// ---- retention ------------------------------------------------------------------------------

func TestContentAndMetadataRetention(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	out := h.begin("run-ret", nil).Finish(context.Background(), Output{AssistantText: "bad words"})
	ctx := context.Background()

	// Before 30 days nothing happens.
	h.env.Clock.Advance(29 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	var e Event
	h.env.DB.Where("public_id = ?", out.EventID).First(&e)
	if e.EncryptedText == "" || e.ContentSummary != SummaryOutputTextHit {
		t.Fatalf("content removed too early: %+v", e)
	}

	// At 31 days the text is gone but the row stays.
	h.env.Clock.Advance(2 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	h.env.DB.Where("public_id = ?", out.EventID).First(&e)
	if e.EncryptedText != "" || e.ContentSummary != "" || e.ImageCount != 0 || e.ImageMetaJSON != "[]" || e.Result != ResultHit {
		t.Fatalf("after 31 days: %+v", e)
	}
	w := h.do("GET", "/events/"+out.EventID, nil)
	var d struct {
		TextAvailable bool `json:"textAvailable"`
	}
	decodeData(t, w.Body.Bytes(), &d)
	if w.Code != 200 || d.TextAvailable {
		t.Fatalf("detail after cleanup: %d %s", w.Code, w.Body)
	}
	var n int64
	h.env.DB.Unscoped().Model(&Event{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows at 31 days: %d", n)
	}

	// At 91 days the row is physically removed.
	h.env.Clock.Advance(60 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	h.env.DB.Unscoped().Model(&Event{}).Count(&n)
	if n != 0 {
		t.Fatalf("rows at 91 days: %d", n)
	}
	// Statistics older than 90 days went with it.
	var stats int64
	h.env.DB.Model(&DailyStat{}).Count(&stats)
	if stats != 0 {
		t.Fatalf("stats rows: %d", stats)
	}
}

func TestPassedAndFailedOpenEventsExpireWithMetadata(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(string, Scope) (*Response, error) { return nil, &ProviderError{Code: CodeTimeout} }
	h.begin("a", nil).Finish(context.Background(), Output{AssistantText: "x"})
	h.prov.text = nil
	h.begin("b", nil).Finish(context.Background(), Output{AssistantText: "x"})
	if len(h.allEvents()) != 2 {
		t.Fatal("setup")
	}
	h.env.Clock.Advance(89 * 24 * time.Hour)
	_ = h.svc.CleanupExpired(context.Background())
	if len(h.allEvents()) != 2 {
		t.Fatal("metadata retention is 90 days")
	}
	h.env.Clock.Advance(2 * 24 * time.Hour)
	_ = h.svc.CleanupExpired(context.Background())
	if len(h.allEvents()) != 0 {
		t.Fatal("rows must be gone after 90 days")
	}
}

func TestFailedObjectDeletionKeepsContentAndMetadata(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(7, pngBytes(t, 6, 6), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	out := h.begin("run-del", nil).Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}}})
	if h.objects.count() != 1 {
		t.Fatal("setup")
	}
	ctx := context.Background()
	h.objects.failDel = true
	h.env.Clock.Advance(31 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	var e Event
	h.env.DB.Where("public_id = ?", out.EventID).First(&e)
	if e.ImageCount != 1 || e.ImageMetaJSON == "[]" || h.objects.count() != 1 {
		t.Fatalf("content must be retained for a retry: %+v", e)
	}
	h.env.Clock.Advance(60 * 24 * time.Hour)
	_ = h.svc.CleanupExpired(ctx)
	var n int64
	h.env.DB.Unscoped().Model(&Event{}).Where("public_id = ?", out.EventID).Count(&n)
	if n != 1 {
		t.Fatal("metadata must not be cleared while content deletion keeps failing")
	}
	// Once the store works again the next pass finishes the job.
	h.objects.failDel = false
	if err := h.svc.CleanupExpired(ctx); err != nil {
		t.Fatal(err)
	}
	h.env.DB.Unscoped().Model(&Event{}).Where("public_id = ?", out.EventID).Count(&n)
	if h.objects.count() != 0 || n != 0 {
		t.Fatalf("objects %d rows %d", h.objects.count(), n)
	}
}

func TestCleanupDoesNotStarveBehindFailingRows(t *testing.T) {
	h := newHarness(t)
	h.objects.failDel = true
	for i := 0; i < 3; i++ {
		h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 1,
			ImageMetaJSON: `[{"index":0,"sha256":"x","mimeType":"image/png","sizeBytes":1,"storagePath":"k` + fmt.Sprint(i) + `"}]`})
	}
	text := h.insertEvent(Event{Direction: "input", Modality: "text", Result: "hit", EncryptedText: "cipher", ContentSummary: SummaryInputTextHit})
	h.env.Clock.Advance(31 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	var e Event
	h.env.DB.First(&e, text.ID)
	if e.EncryptedText != "" {
		t.Fatal("rows behind failing ones must still be cleaned")
	}
}

func TestDeleteEventsBeforeCutoff(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	old := h.insertEvent(Event{Direction: "input", Modality: "text", Result: "passed"})
	oldImg := h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 1,
		ImageMetaJSON: `[{"index":0,"storagePath":"obj/old"}]`})
	h.objects.objects["obj/old"] = []byte("x")
	keep := h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 1,
		ImageMetaJSON: `[{"index":0,"storagePath":"obj/keep"}]`})
	fresh := h.insertEvent(Event{Direction: "input", Modality: "text", Result: "passed"})
	past := h.env.Clock.Now().Add(-48 * time.Hour)
	h.env.DB.Model(&Event{}).Where("id IN ?", []uint{old.ID, oldImg.ID, keep.ID}).Update("created_at", past)

	// An object that cannot be deleted keeps its event.
	h.objects.objects["obj/keep"] = []byte("x")
	h.svc.objects = failingStore{h.objects, "obj/keep"}
	n, err := h.svc.DeleteEventsBefore(context.Background(), h.env.Clock.Now().Add(-24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("deleted %d (%v)", n, err)
	}
	if _, ok := h.objects.objects["obj/old"]; ok {
		t.Fatal("isolated object of a deleted event must be removed")
	}
	var left []Event
	h.env.DB.Find(&left)
	if len(left) != 2 {
		t.Fatalf("left %d", len(left))
	}
	_ = fresh
}

type failingStore struct {
	*memObjects
	failKey string
}

func (f failingStore) Delete(ctx context.Context, key string) error {
	if key == f.failKey {
		return fmt.Errorf("cannot delete")
	}
	return f.memObjects.Delete(ctx, key)
}

// ---- key rotation -----------------------------------------------------------------------------

func TestReencryptSecretsMovesKeyAndRetainedText(t *testing.T) {
	h := newHarness(t)
	oldRing, err := secretbox.NewKeyRing("old-key-material-0123456789abcdef-0123", nil)
	if err != nil {
		t.Fatal(err)
	}
	newRing, err := secretbox.NewKeyRing("new-key-material-0123456789abcdef-0123", []string{"old-key-material-0123456789abcdef-0123"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.secrets, h.svc.store.secrets = oldRing, oldRing
	h.enable(nil)
	outputHitOnly(h, "violence")
	out := h.begin("run-rot", nil).Finish(context.Background(), Output{AssistantText: "rotating words"})

	h.svc.secrets, h.svc.store.secrets = newRing, newRing
	changed, err := h.svc.ReencryptSecrets(context.Background())
	if err != nil || changed != 2 {
		t.Fatalf("changed %d (%v)", changed, err)
	}
	if again, _ := h.svc.ReencryptSecrets(context.Background()); again != 0 {
		t.Fatalf("second pass changed %d", again)
	}

	// With only the new key everything still reads.
	onlyNew, _ := secretbox.NewKeyRing("new-key-material-0123456789abcdef-0123", nil)
	h.svc.secrets, h.svc.store.secrets = onlyNew, onlyNew
	h.svc.store.invalidate()
	if c := h.getConfig().Config; !c.HasAPIKey || c.APIKeyMasked != "sk-t...3456" {
		t.Fatalf("config after rotation: %+v", c)
	}
	var d struct {
		DecryptedText string `json:"decryptedText"`
	}
	decodeData(t, h.do("GET", "/events/"+out.EventID, nil).Body.Bytes(), &d)
	if d.DecryptedText != "rotating words" {
		t.Fatalf("text after rotation: %q", d.DecryptedText)
	}
}

package billing

import (
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const statsURL = "/api/v1/admin/usage-statistics"

func (e *env) stats(q string) (*httpResult, Stats) {
	e.t.Helper()
	w := e.do("GET", statsURL+q, e.admin(9), nil)
	var out Stats
	if w.Code == 200 {
		testkit.Decode(e.t, w, &out)
	}
	return &httpResult{w.Code, w.Body.String()}, out
}

type httpResult struct {
	Code int
	Body string
}

func TestStatsValidation(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"?start_date=2026-01-01":                     "usage_statistics.invalid_date_range",
		"?end_date=2026-01-01":                       "usage_statistics.invalid_date_range",
		"?start_date=2026-02-01&end_date=2026-01-01": "usage_statistics.invalid_date_range",
		"?start_date=2025-01-01&end_date=2026-01-02": "usage_statistics.invalid_date_range", // 367 days
		"?start_date=bad&end_date=2026-01-01":        "usage_statistics.invalid_date_range",
		"?user_id=1&permission_group_id=1":           "usage_statistics.subject_conflict",
		"?billing_scope=weird":                       "usage_statistics.invalid_billing_scope",
		"?section=weird":                             "usage_statistics.invalid_section",
		"?model_rank_by=weird":                       "usage_statistics.invalid_rank_by",
		"?user_rank_by=weird":                        "usage_statistics.invalid_rank_by",
	}
	for q, code := range cases {
		w := e.do("GET", statsURL+q, e.admin(9), nil)
		testkit.Expect(t, w, 400, code)
	}
	// unknown permission group
	w := e.do("GET", statsURL+"?permission_group_id=77", e.admin(9), nil)
	if w.Code != 404 {
		t.Fatalf("unknown group: %d %s", w.Code, w.Body)
	}
	// exactly 366 days is allowed
	if r, _ := e.stats("?start_date=2025-01-01&end_date=2026-01-01"); r.Code != 200 {
		t.Fatalf("366 days: %d %s", r.Code, r.Body)
	}
}

func TestStatsGranularity(t *testing.T) {
	e := newEnv(t)
	for q, want := range map[string]string{
		"": "day",
		"?start_date=2026-02-01&end_date=2026-02-30": "",
		"?start_date=2026-02-01&end_date=2026-02-28": "day",
		"?start_date=2026-01-01&end_date=2026-01-30": "day",
		"?start_date=2026-01-01&end_date=2026-01-31": "week",
		"?start_date=2025-09-01&end_date=2026-02-28": "month",
		"?start_date=2025-09-01&end_date=2026-02-26": "week",
	} {
		r, s := e.stats(q)
		if want == "" {
			if r.Code != 400 {
				t.Errorf("%s: %d", q, r.Code)
			}
			continue
		}
		if r.Code != 200 || s.Range.Granularity != want {
			t.Errorf("%s: %d gran=%q want %q", q, r.Code, s.Range.Granularity, want)
		}
	}
	// default range: the last 30 days ending today
	_, s := e.stats("")
	if s.Range.StartDate != "2026-01-31" || s.Range.EndDate != "2026-03-01" || len(s.Trend) != 30 {
		t.Fatalf("default range %+v trend=%d", s.Range, len(s.Trend))
	}
}

func TestStatsAggregation(t *testing.T) {
	e := newEnv(t)
	e.users.users[1] = UserInfo{Username: "one", DisplayName: "One"}
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-25", billed: 100, in: 10, out: 10, latency: 100})
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-26", billed: 100, in: 10, latency: 300})
	e.ledger(ledgerSpec{user: 2, model: "b", date: "2026-02-26", billed: 500, in: 1})
	e.ledger(ledgerSpec{user: 3, model: "free", date: "2026-02-27", free: true, in: 1000, calls: 5})
	e.ledger(ledgerSpec{user: 3, model: "a", date: "2026-02-10", billed: 9}) // outside the range
	r, s := e.stats("?start_date=2026-02-24&end_date=2026-02-28")
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if s.Section != "all" || s.Range.Granularity != "day" || len(s.Trend) != 5 {
		t.Fatalf("shape %+v trend=%d", s.Range, len(s.Trend))
	}
	if s.Totals.RecordCount != 4 || s.Totals.BilledNanousd != 700 || s.Totals.InputTokens != 1021 || s.Totals.CallCount != 8 || s.Totals.AvgLatencyMS != 200 {
		t.Fatalf("totals %+v", s.Totals)
	}
	if s.Trend[0].RecordCount != 0 || s.Trend[1].RecordCount != 1 || s.Trend[2].RecordCount != 2 || s.Trend[2].BilledNanousd != 600 {
		t.Fatalf("trend %+v", s.Trend)
	}
	// ranking: cost default: b(500) > a(200) > free(0)
	if len(s.TopModels) != 3 || s.TopModels[0].PlatformModelName != "b" || s.TopModels[1].PlatformModelName != "a" || s.TopModels[2].PlatformModelName != "free" {
		t.Fatalf("models %+v", s.TopModels)
	}
	if len(s.TopModels[1].Trend) != 2 {
		t.Fatalf("model trend %+v", s.TopModels[1].Trend)
	}
	if len(s.TopUsers) != 3 || s.TopUsers[0].UserID != 2 || s.TopUsers[1].UserID != 1 || s.TopUsers[1].UserLabel != "One" {
		t.Fatalf("users %+v", s.TopUsers)
	}
	// rank by tokens / calls
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&model_rank_by=tokens&user_rank_by=calls")
	if s.TopModels[0].PlatformModelName != "free" || s.TopUsers[0].UserID != 3 {
		t.Fatalf("rank by tokens/calls: %+v / %+v", s.TopModels, s.TopUsers)
	}
	// scopes
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&billing_scope=free")
	if s.Totals.RecordCount != 1 || s.Totals.BilledNanousd != 0 {
		t.Fatalf("free scope %+v", s.Totals)
	}
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&billing_scope=billable")
	if s.Totals.RecordCount != 3 {
		t.Fatalf("billable scope %+v", s.Totals)
	}
	// filters
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&user_id=1")
	if s.Totals.RecordCount != 2 || len(s.TopUsers) != 1 {
		t.Fatalf("user filter %+v", s.Totals)
	}
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&platform_model_name=b")
	if s.Totals.RecordCount != 1 {
		t.Fatalf("model filter %+v", s.Totals)
	}
	// sections
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&section=models")
	if s.Section != "models" || len(s.TopUsers) != 0 || len(s.TopModels) == 0 || len(s.Trend) != 3 {
		t.Fatalf("models section: users=%d models=%d trend=%d", len(s.TopUsers), len(s.TopModels), len(s.Trend))
	}
	_, s = e.stats("?start_date=2026-02-24&end_date=2026-02-28&section=users")
	if len(s.TopModels) != 0 || len(s.TopUsers) == 0 {
		t.Fatalf("users section")
	}
}

func TestStatsRankingTiesAndLimit(t *testing.T) {
	e := newEnv(t)
	// 12 models with equal cost: ties broken by tokens, then name; limited to 10
	for i := 0; i < 12; i++ {
		e.ledger(ledgerSpec{user: uint(i + 1), model: "m" + string(rune('a'+i)), date: "2026-02-28", billed: 10, in: int64(100 - i)})
	}
	_, s := e.stats("?start_date=2026-02-28&end_date=2026-02-28")
	if len(s.TopModels) != 10 || len(s.TopUsers) != 10 {
		t.Fatalf("limit: %d %d", len(s.TopModels), len(s.TopUsers))
	}
	if s.TopModels[0].PlatformModelName != "ma" || s.TopModels[9].PlatformModelName != "mj" {
		t.Fatalf("tie order %v %v", s.TopModels[0].PlatformModelName, s.TopModels[9].PlatformModelName)
	}
	// equal everything: name / user id ascending
	e2 := newEnv(t)
	e2.ledger(ledgerSpec{user: 5, model: "zz", date: "2026-02-28", billed: 10, in: 1})
	e2.ledger(ledgerSpec{user: 4, model: "aa", date: "2026-02-28", billed: 10, in: 1})
	_, s = e2.stats("?start_date=2026-02-28&end_date=2026-02-28")
	if s.TopModels[0].PlatformModelName != "aa" || s.TopUsers[0].UserID != 4 {
		t.Fatalf("final tie break %+v %+v", s.TopModels, s.TopUsers)
	}
}

func TestStatsWeekAndMonthBuckets(t *testing.T) {
	e := newEnv(t)
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-04", billed: 1}) // Wednesday
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-08", billed: 2}) // Sunday: same ISO week
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-09", billed: 4}) // Monday: next week
	_, s := e.stats("?start_date=2026-02-01&end_date=2026-03-05")
	if s.Range.Granularity != "week" {
		t.Fatalf("gran %s", s.Range.Granularity)
	}
	got := map[string]int64{}
	for _, p := range s.Trend {
		got[p.PeriodStart.Format("2006-01-02")] = p.BilledNanousd
	}
	if got["2026-02-02"] != 3 || got["2026-02-09"] != 4 || got["2026-01-26"] != 0 {
		t.Fatalf("weeks %v", got)
	}
	for _, p := range s.Trend {
		if p.PeriodStart.Weekday() != time.Monday {
			t.Fatalf("week starts on %v", p.PeriodStart.Weekday())
		}
	}
	_, s = e.stats("?start_date=2025-08-01&end_date=2026-02-20")
	if s.Range.Granularity != "month" || s.Trend[0].PeriodStart.Format("2006-01-02") != "2025-08-01" || len(s.Trend) != 7 {
		t.Fatalf("months %+v len=%d", s.Range, len(s.Trend))
	}
}

func TestStatsPermissionGroupFilter(t *testing.T) {
	e := newEnv(t)
	e.groups.exist[3] = true
	e.groups.members[3] = GroupMembers{Exists: true, UserIDs: []uint{1}}
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-28", billed: 1})
	e.ledger(ledgerSpec{user: 2, model: "a", date: "2026-02-28", billed: 2})
	e.ledger(ledgerSpec{user: 3, model: "a", date: "2026-02-28", billed: 4})
	// group 3 is bound to the pro plan, user 3 holds pro now
	pro := e.planByCode("pro")
	g := uint(3)
	e.DB.Model(&Plan{}).Where("id = ?", pro.ID).Update("permission_group_id", g)
	end := e.Clock.Now().Add(24 * time.Hour)
	e.grantSub(3, "pro", e.Clock.Now().Add(-time.Hour), &end)
	_, s := e.stats("?start_date=2026-02-28&end_date=2026-02-28&permission_group_id=3")
	if s.Totals.BilledNanousd != 5 || s.Totals.RecordCount != 2 {
		t.Fatalf("group 3: %+v", s.Totals)
	}
	// the default group contains everyone
	_, s = e.stats("?start_date=2026-02-28&end_date=2026-02-28&permission_group_id=1")
	if s.Totals.RecordCount != 3 {
		t.Fatalf("default group: %+v", s.Totals)
	}
	// a group with nobody
	e.groups.exist[4] = true
	_, s = e.stats("?start_date=2026-02-28&end_date=2026-02-28&permission_group_id=4")
	if s.Totals.RecordCount != 0 {
		t.Fatalf("empty group: %+v", s.Totals)
	}
}

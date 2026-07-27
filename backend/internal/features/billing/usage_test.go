package billing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

type ledgerSpec struct {
	user     uint
	model    string
	date     string // usage date YYYY-MM-DD
	billed   int64
	free     bool
	in, out  int64
	latency  int64
	calls    int64
	ref      string
	snapshot map[string]any
	upstream string
	protocol string
	binding  string
	created  *time.Time
}

func (e *env) ledger(s ledgerSpec) UsageLedger {
	e.t.Helper()
	if s.date == "" {
		s.date = e.Clock.Now().Format("2006-01-02")
	}
	var d DateOnly
	if err := d.parse(s.date); err != nil {
		e.t.Fatal(err)
	}
	if s.calls == 0 {
		s.calls = 1
	}
	snap := s.snapshot
	if snap == nil {
		snap = map[string]any{}
	}
	snapJSON, _ := json.Marshal(snap)
	at := d.In(time.UTC).Add(12 * time.Hour)
	row := UsageLedger{UserID: s.user, RefNo: s.ref, PlatformModelName: s.model, UsageDate: d, BillingAt: at, BilledNanousd: s.billed,
		IsFreeModel: s.free, InputTokens: s.in, OutputTokens: s.out, LatencyMS: s.latency, CallCount: s.calls, BilledCurrency: "USD",
		PricingSnapshotJSON: string(snapJSON), UpstreamName: s.upstream, ProviderProtocol: s.protocol, RoutedBindingCode: s.binding}
	if err := e.DB.Create(&row).Error; err != nil {
		e.t.Fatal(err)
	}
	if s.created != nil {
		e.DB.Model(&UsageLedger{}).Where("id = ?", row.ID).Update("created_at", *s.created)
	}
	return row
}

func TestUsageListFiltersAndSorts(t *testing.T) {
	e := newEnv(t)
	a := e.ledger(ledgerSpec{user: 1, model: "GPT-Alpha", date: "2026-02-27", billed: 300, in: 10, out: 10, latency: 50})
	b := e.ledger(ledgerSpec{user: 1, model: "claude-beta", date: "2026-02-28", billed: 100, in: 500, out: 5, latency: 900})
	c := e.ledger(ledgerSpec{user: 1, model: "free-model", date: "2026-02-28", free: true, in: 1, latency: 10})
	e.ledger(ledgerSpec{user: 2, model: "GPT-Alpha", billed: 999})
	type listOut struct {
		Total   int64        `json:"total"`
		Results []LedgerItem `json:"results"`
	}
	get := func(q string) listOut {
		t.Helper()
		w := e.do("GET", "/api/v1/billing/usage"+q, e.user(1), nil)
		testkit.Expect(t, w, 200, "")
		var out listOut
		testkit.Decode(t, w, &out)
		return out
	}
	idsOf := func(l listOut) []uint {
		var ids []uint
		for _, r := range l.Results {
			ids = append(ids, r.ID)
		}
		return ids
	}
	eq := func(got []uint, want ...uint) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if l := get(""); l.Total != 3 || !eq(idsOf(l), c.ID, b.ID, a.ID) {
		t.Fatalf("newest: %v", idsOf(l))
	}
	if l := get("?sort=oldest"); !eq(idsOf(l), a.ID, b.ID, c.ID) {
		t.Fatalf("oldest: %v", idsOf(l))
	}
	if l := get("?sort=tokens_desc"); !eq(idsOf(l), b.ID, a.ID, c.ID) {
		t.Fatalf("tokens: %v", idsOf(l))
	}
	if l := get("?sort=cost_desc"); !eq(idsOf(l), a.ID, b.ID, c.ID) {
		t.Fatalf("cost: %v", idsOf(l))
	}
	if l := get("?sort=latency_desc"); !eq(idsOf(l), b.ID, a.ID, c.ID) {
		t.Fatalf("latency: %v", idsOf(l))
	}
	if l := get("?query=gpt"); l.Total != 1 || l.Results[0].ID != a.ID {
		t.Fatalf("query: %v", idsOf(l))
	}
	if l := get("?status=free"); l.Total != 1 || l.Results[0].ID != c.ID {
		t.Fatalf("free: %v", idsOf(l))
	}
	if l := get("?status=billable"); l.Total != 2 {
		t.Fatalf("billable: %v", idsOf(l))
	}
	if l := get("?page=2&page_size=2"); l.Total != 3 || len(l.Results) != 1 {
		t.Fatalf("paging: %+v", l)
	}
	if l := get("?page=abc&page_size=-4"); l.Total != 3 || len(l.Results) != 3 {
		t.Fatalf("bad paging falls back: %+v", l)
	}
	if l := get("?page_size=999999"); l.Total != 3 {
		t.Fatalf("clamp: %+v", l)
	}
}

func TestUsageListPrivacyAndFallbacks(t *testing.T) {
	e := newEnv(t)
	snap := map[string]any{
		"upstream_name": "SecretProvider", "model_vendor": "acme", "model_icon": "acme.svg", "nested": map[string]any{"upstreamName": "x", "keep": 1},
		"upstream": map[string]any{"name": "N", "displayName": "D", "id": 7}, "list": []any{map[string]any{"UpstreamName": "y", "ok": true}},
	}
	row := e.ledger(ledgerSpec{user: 1, model: "m", billed: 5, snapshot: snap, upstream: "SecretProvider"})
	w := e.do("GET", "/api/v1/billing/usage", e.user(1), nil)
	if strings.Contains(w.Body.String(), "SecretProvider") || strings.Contains(w.Body.String(), `"upstreamName"`) {
		t.Fatalf("upstream leaked: %s", w.Body)
	}
	var out struct {
		Results []LedgerItem `json:"results"`
	}
	testkit.Decode(t, w, &out)
	it := out.Results[0]
	if it.ModelVendor != "acme" || it.ModelIcon != "acme.svg" {
		t.Fatalf("vendor/icon %+v", it)
	}
	var sn map[string]any
	_ = json.Unmarshal([]byte(it.PricingSnapshotJSON), &sn)
	if _, ok := sn["upstream_name"]; ok {
		t.Fatal("upstream_name kept")
	}
	if up := sn["upstream"].(map[string]any); up["name"] != nil || up["displayName"] != nil || up["id"] != float64(7) {
		t.Fatalf("upstream object %v", up)
	}
	if n := sn["nested"].(map[string]any); n["upstreamName"] != nil || n["keep"] != float64(1) {
		t.Fatalf("nested %v", n)
	}
	if l := sn["list"].([]any)[0].(map[string]any); l["UpstreamName"] != nil || l["ok"] != true {
		t.Fatalf("list %v", l)
	}
	// admin call logs keep everything
	w = e.do("GET", "/api/v1/admin/call-logs", e.admin(2), nil)
	if !strings.Contains(w.Body.String(), "SecretProvider") {
		t.Fatalf("admin lost upstream: %s", w.Body)
	}
	// unparsable / empty snapshots become {}
	e.DB.Model(&UsageLedger{}).Where("id = ?", row.ID).Update("pricing_snapshot_json", "###")
	testkit.Decode(t, e.do("GET", "/api/v1/billing/usage", e.user(1), nil), &out)
	if out.Results[0].PricingSnapshotJSON != "{}" || out.Results[0].ModelVendor != "" {
		t.Fatalf("broken snapshot: %+v", out.Results[0])
	}
	e.DB.Model(&UsageLedger{}).Where("id = ?", row.ID).Update("pricing_snapshot_json", "")
	testkit.Decode(t, e.do("GET", "/api/v1/billing/usage", e.user(1), nil), &out)
	if out.Results[0].PricingSnapshotJSON != "{}" {
		t.Fatalf("empty snapshot: %q", out.Results[0].PricingSnapshotJSON)
	}
}

func TestUsageBalanceAfterFallsBackToTransaction(t *testing.T) {
	e := newEnv(t)
	row := e.ledger(ledgerSpec{user: 1, model: "m", billed: 5})
	acc, _ := ensureAccount(e.DB, 1)
	e.DB.Create(&BalanceTransaction{AccountID: acc.ID, UserID: 1, Type: TxUsageDebit, AmountNanousd: -5, BalanceAfterNanousd: 1_500_000_000, RefType: "usage_ledger", RefID: row.ID})
	var out struct {
		Results []LedgerItem `json:"results"`
	}
	testkit.Decode(t, e.do("GET", "/api/v1/billing/usage", e.user(1), nil), &out)
	it := out.Results[0]
	if it.BalanceAfterNanousd == nil || *it.BalanceAfterNanousd != 1_500_000_000 || it.BalanceAfterUSD == nil || *it.BalanceAfterUSD != 1.5 {
		t.Fatalf("fallback %+v", it)
	}
	// rows without any balance information stay null
	e.ledger(ledgerSpec{user: 1, model: "m2", billed: 0})
	testkit.Decode(t, e.do("GET", "/api/v1/billing/usage?sort=newest", e.user(1), nil), &out)
	if out.Results[0].BalanceAfterNanousd != nil {
		t.Fatalf("expected null, got %v", *out.Results[0].BalanceAfterNanousd)
	}
}

func TestMonthlyUsage(t *testing.T) {
	e := newEnv(t) // clock: 2026-03-01
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-03-01", billed: 1_000_000_000, in: 100, out: 50, latency: 100, calls: 2})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-03-01", billed: 500_000_000, in: 10, latency: 301})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-03-01", latency: 0})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-01-15", billed: 7})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2025-03-20", billed: 9}) // outside 12 months
	e.ledger(ledgerSpec{user: 2, model: "m", date: "2026-03-02", billed: 77})
	get := func(q string) []MonthSummary {
		w := e.do("GET", "/api/v1/billing/usage/monthly"+q, e.user(1), nil)
		testkit.Expect(t, w, 200, "")
		return decodeData[[]MonthSummary](t, w)
	}
	ms := get("")
	if len(ms) != 12 || ms[11].MonthStartAt.Month() != 3 || ms[0].MonthStartAt.Month() != 4 || ms[0].MonthStartAt.Year() != 2025 {
		t.Fatalf("series bounds: %d %v .. %v", len(ms), ms[0].MonthStartAt, ms[11].MonthStartAt)
	}
	cur := ms[11]
	if cur.RecordCount != 3 || cur.InputTokens != 110 || cur.OutputTokens != 50 || cur.TotalTokens != 160 || cur.CallCount != 4 ||
		cur.BilledNanousd != 1_500_000_000 || cur.BilledUSD != 1.5 || cur.AvgLatencyMS != 201 {
		t.Fatalf("current month %+v", cur)
	}
	if ms[9].RecordCount != 1 || ms[10].RecordCount != 0 || ms[10].BilledNanousd != 0 {
		t.Fatalf("zero fill: %+v %+v", ms[9], ms[10])
	}
	if n := len(get("?months=3")); n != 3 {
		t.Fatalf("months=3 gave %d", n)
	}
	if n := len(get("?months=100")); n != 24 {
		t.Fatalf("cap: %d", n)
	}
	for _, q := range []string{"?months=0", "?months=-2", "?months=abc"} {
		if n := len(get(q)); n != 12 {
			t.Fatalf("%s: %d", q, n)
		}
	}
}

func TestDailyUsageSelection(t *testing.T) {
	e := newEnv(t, withPorts(func(p *Ports) {}))
	e.users.users[1] = UserInfo{Username: "u", CreatedAt: time.Date(2025, 12, 20, 8, 0, 0, 0, time.UTC)}
	// clock is 2026-03-01
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-03-01", billed: 300, latency: 100})
	e.ledger(ledgerSpec{user: 1, model: "b", date: "2026-03-01", billed: 300, latency: 400})
	e.ledger(ledgerSpec{user: 1, model: "c", date: "2026-03-01", billed: 900, latency: 0})
	e.ledger(ledgerSpec{user: 1, model: "a", date: "2026-02-25", billed: 1})
	get := func(q string) (int, []DaySummary) {
		w := e.do("GET", "/api/v1/billing/usage/daily"+q, e.user(1), nil)
		if w.Code != 200 {
			return w.Code, nil
		}
		return 200, decodeData[[]DaySummary](t, w)
	}
	code, days := get("?days=3")
	if code != 200 || len(days) != 3 || days[2].UsageDate.Format("2006-01-02") != "2026-03-01" || days[0].UsageDate.Format("2006-01-02") != "2026-02-27" {
		t.Fatalf("days=3: %d %+v", code, days)
	}
	today := days[2]
	if today.RecordCount != 3 || today.BilledNanousd != 1500 || today.AvgLatencyMS != 250 || len(today.Models) != 3 ||
		today.Models[0].PlatformModelName != "c" || today.Models[1].PlatformModelName != "a" || today.Models[2].PlatformModelName != "b" {
		t.Fatalf("today %+v", today)
	}
	if days[1].RecordCount != 0 || len(days[1].Models) != 0 {
		t.Fatalf("zero day %+v", days[1])
	}
	// anchored cycle: registration day 20 -> window [Feb 20, Mar 20)
	code, days = get("")
	if code != 200 || len(days) != 28 || days[0].UsageDate.Format("2006-01-02") != "2026-02-20" {
		t.Fatalf("anchored: %d len=%d first=%v", code, len(days), days[0].UsageDate)
	}
	// explicit range: end exclusive
	code, days = get("?start_date=2026-02-25&end_date=2026-03-02")
	if code != 200 || len(days) != 5 || days[0].RecordCount != 1 {
		t.Fatalf("range: %d %d", code, len(days))
	}
	// end not after start: one day
	code, days = get("?start_date=2026-02-25&end_date=2026-02-20")
	if code != 200 || len(days) != 1 {
		t.Fatalf("one day: %d %d", code, len(days))
	}
	// 120 day range truncated to 90
	code, days = get("?start_date=2025-10-01&end_date=2026-01-29")
	if code != 200 || len(days) != 90 {
		t.Fatalf("truncate: %d %d", code, len(days))
	}
	code, days = get("?days=500")
	if code != 200 || len(days) != 90 {
		t.Fatalf("days cap: %d %d", code, len(days))
	}
	// errors
	for _, q := range []string{"?days=0", "?days=abc", "?days=-3", "?days="} {
		w := e.do("GET", "/api/v1/billing/usage/daily"+q, e.user(1), nil)
		testkit.Expect(t, w, 400, "billing.invalid_daily_usage_days")
	}
	for _, q := range []string{"?start_date=bad&end_date=2026-03-01", "?start_date=2026-03-01&end_date=2026/03/05"} {
		w := e.do("GET", "/api/v1/billing/usage/daily"+q, e.user(1), nil)
		testkit.Expect(t, w, 400, "billing.invalid_daily_usage_date_range")
	}
	// the range wins over days
	code, days = get("?days=abc&start_date=2026-03-01&end_date=2026-03-03")
	if code != 200 || len(days) != 2 {
		t.Fatalf("priority: %d %d", code, len(days))
	}
}

func TestAnchoredCycle(t *testing.T) {
	loc := time.UTC
	d := func(s string) time.Time { x, _ := time.ParseInLocation("2006-01-02", s, loc); return x }
	cases := []struct{ created, today, start, end string }{
		{"2025-12-20", "2026-03-01", "2026-02-20", "2026-03-20"},
		{"2025-12-20", "2026-03-25", "2026-03-20", "2026-04-20"},
		{"2025-12-31", "2026-03-05", "2026-02-28", "2026-03-31"},
		{"2025-12-31", "2026-03-31", "2026-03-31", "2026-04-30"},
		{"2025-01-01", "2026-03-01", "2026-03-01", "2026-04-01"},
		{"2025-12-31", "2026-03-30", "2026-02-28", "2026-03-31"},
		{"2025-12-15", "2026-01-10", "2025-12-15", "2026-01-15"},
	}
	for _, c := range cases {
		s, e := anchoredCycle(d(c.created), d(c.today), loc)
		if s != d(c.start) || e != d(c.end) {
			t.Errorf("%+v: got [%s,%s)", c, s.Format("2006-01-02"), e.Format("2006-01-02"))
		}
	}
}

func TestActivityByDay(t *testing.T) {
	e := newEnv(t)
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-03-01", in: 10, out: 5})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-03-01", in: 1, snapshot: map[string]any{"service_only": true}})
	e.ledger(ledgerSpec{user: 1, model: "m", date: "2026-02-28", in: 3})
	from := time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	got, err := e.svc.ActivityByDay(t.Context(), 1, from, to)
	if err != nil || len(got) != 2 || got[0].Date.String() != "2026-02-28" || got[0].Count != 1 || got[0].Tokens != 3 ||
		got[1].Count != 1 || got[1].Tokens != 15 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCallLogs(t *testing.T) {
	e := newEnv(t)
	e.users.users[1] = UserInfo{Username: "alice", DisplayName: "Alice"}
	e.users.users[2] = UserInfo{Username: "bob"}
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
	tok := e.ledger(ledgerSpec{user: 1, model: "gpt-x", billed: 10, in: 5, latency: 30, created: &t1, upstream: "OpenAI-prod", protocol: "openai_chat_completions", binding: "bind-1", ref: "run-1",
		snapshot: map[string]any{"pricing_mode": "token"}})
	call := e.ledger(ledgerSpec{user: 2, model: "img", billed: 90, in: 1, latency: 10, created: &t2, snapshot: map[string]any{"pricing_mode": "call"}})
	free := e.ledger(ledgerSpec{user: 2, model: "gpt-x", free: true, created: &t3, upstream: "Other"})
	legacy := e.ledger(ledgerSpec{user: 1, model: "legacy", billed: 3, created: &t3, snapshot: map[string]any{}}) // missing mode counts as token
	type item struct {
		ID           uint   `json:"id"`
		UpstreamName string `json:"upstreamName"`
		Username     string `json:"username"`
		UserLabel    string `json:"userLabel"`
	}
	var out struct {
		Total   int64  `json:"total"`
		Results []item `json:"results"`
	}
	get := func(q string) []uint {
		t.Helper()
		w := e.do("GET", "/api/v1/admin/call-logs"+q, e.admin(9), nil)
		testkit.Expect(t, w, 200, "")
		out.Results = nil
		testkit.Decode(t, w, &out)
		var ids []uint
		for _, r := range out.Results {
			ids = append(ids, r.ID)
		}
		return ids
	}
	same := func(got []uint, want ...uint) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if ids := get(""); !same(ids, legacy.ID, free.ID, call.ID, tok.ID) {
		t.Fatalf("default order %v", ids)
	}
	if out.Results[3].UpstreamName != "OpenAI-prod" || out.Results[3].Username != "alice" || out.Results[3].UserLabel != "Alice" || out.Results[2].UserLabel != "bob" {
		t.Fatalf("labels %+v", out.Results)
	}
	if ids := get("?sort=created_asc"); !same(ids, tok.ID, call.ID, free.ID, legacy.ID) {
		t.Fatalf("asc %v", ids)
	}
	if ids := get("?sort=cost_desc"); !same(ids, call.ID, tok.ID, legacy.ID, free.ID) {
		t.Fatalf("cost %v", ids)
	}
	if ids := get("?sort=latency_desc"); ids[0] != tok.ID {
		t.Fatalf("latency %v", ids)
	}
	if ids := get("?sort=tokens_desc"); ids[0] != tok.ID {
		t.Fatalf("tokens %v", ids)
	}
	if ids := get("?query=OPENAI-PROD"); !same(ids, tok.ID) {
		t.Fatalf("query upstream %v", ids)
	}
	if ids := get("?query=bind-1"); !same(ids, tok.ID) {
		t.Fatalf("query binding %v", ids)
	}
	if ids := get("?query=chat_completions"); !same(ids, tok.ID) {
		t.Fatalf("query protocol %v", ids)
	}
	if ids := get("?query=run-1"); !same(ids, tok.ID) {
		t.Fatalf("query ref %v", ids)
	}
	if ids := get("?platform_model_name=gpt-x"); !same(ids, free.ID, tok.ID) {
		t.Fatalf("model %v", ids)
	}
	if ids := get("?user_id=2"); !same(ids, free.ID, call.ID) {
		t.Fatalf("user %v", ids)
	}
	if ids := get("?billing_mode=free"); !same(ids, free.ID) {
		t.Fatalf("free %v", ids)
	}
	if ids := get("?billing_mode=call"); !same(ids, call.ID) {
		t.Fatalf("call %v", ids)
	}
	if ids := get("?billing_mode=token"); !same(ids, legacy.ID, tok.ID) || out.Total != 2 {
		t.Fatalf("token %v total %d", ids, out.Total)
	}
	if ids := get("?billing_mode=token&page=2&page_size=1"); !same(ids, tok.ID) || out.Total != 2 {
		t.Fatalf("token page2 %v total %d", ids, out.Total)
	}
	if ids := get("?created_from=" + t2.Format(time.RFC3339) + "&created_to=" + t2.Format(time.RFC3339)); !same(ids, call.ID) {
		t.Fatalf("time %v", ids)
	}
	testkit.Expect(t, e.do("GET", "/api/v1/admin/call-logs?created_from=bad", e.admin(9), nil), 400, "request.invalid_query")
	testkit.Expect(t, e.do("GET", "/api/v1/admin/call-logs?user_id=abc", e.admin(9), nil), 400, "request.invalid_query")
	// ordinary users are refused
	if w := e.do("GET", "/api/v1/admin/call-logs", e.user(1), nil); w.Code == 200 {
		t.Fatal("user reached admin route")
	}
}

func TestPaymentOrdersList(t *testing.T) {
	e := payEnv(t, newStripeFake(t))
	e.users.users[5] = UserInfo{Username: "eve", DisplayName: "Eve"}
	a := e.pendingOrder(5, "epay", map[string]any{"priceID": e.priceID("pro-monthly")})
	e.Clock.Advance(time.Minute)
	b := e.pendingOrder(6, "stripe", map[string]any{"amountMinorUnits": 9900})
	e.Clock.Advance(time.Minute)
	if _, _, err := e.svc.CompleteOrder(t.Context(), a.OrderNo, "T-1", time.Time{}); err != nil {
		t.Fatal(err)
	}
	type item struct {
		OrderNo     string     `json:"orderNo"`
		UserLabel   string     `json:"userLabel"`
		Status      string     `json:"status"`
		PaidAt      *time.Time `json:"paidAt"`
		CheckoutURL string     `json:"checkoutURL"`
		Snapshot    string     `json:"snapshotJSON"`
	}
	var out struct {
		Total   int64  `json:"total"`
		Results []item `json:"results"`
	}
	get := func(q string) []string {
		t.Helper()
		w := e.do("GET", "/api/v1/admin/payment-orders"+q, e.admin(9), nil)
		testkit.Expect(t, w, 200, "")
		out.Results = nil
		testkit.Decode(t, w, &out)
		if strings.Contains(w.Body.String(), "checkoutURL") || strings.Contains(w.Body.String(), "pay.example.com") {
			t.Fatalf("checkout url exposed: %s", w.Body)
		}
		var nos []string
		for _, r := range out.Results {
			nos = append(nos, r.OrderNo)
		}
		return nos
	}
	if nos := get(""); len(nos) != 2 || nos[0] != b.OrderNo || out.Results[1].UserLabel != "Eve" || out.Results[1].Status != "paid" || out.Results[1].PaidAt == nil {
		t.Fatalf("default %v %+v", nos, out.Results)
	}
	if nos := get("?sort=created_asc"); nos[0] != a.OrderNo {
		t.Fatalf("asc %v", nos)
	}
	if nos := get("?sort=paid_desc"); nos[0] != a.OrderNo || nos[1] != b.OrderNo {
		t.Fatalf("paid_desc %v", nos)
	}
	if nos := get("?sort=amount_desc"); nos[0] != a.OrderNo {
		// order a pays 144.00 CNY (14400), order b pays 99.00 USD (9900)
		t.Fatalf("amount_desc %v", nos)
	}
	for q, want := range map[string]string{"?order_type=topup": b.OrderNo, "?provider=epay": a.OrderNo, "?status=pending": b.OrderNo,
		"?user_id=5": a.OrderNo, "?query=T-1": a.OrderNo, "?query=cs_test": b.OrderNo, "?query=PAID": a.OrderNo} {
		if nos := get(q); len(nos) != 1 || nos[0] != want {
			t.Errorf("%s: %v want %s", q, nos, want)
		}
	}
	from := e.Clock.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	if nos := get("?created_from=" + from); len(nos) != 1 || nos[0] != b.OrderNo {
		t.Fatalf("created_from %v", nos)
	}
	testkit.Expect(t, e.do("GET", "/api/v1/admin/payment-orders?created_to=nope", e.admin(9), nil), 400, "request.invalid_query")
}

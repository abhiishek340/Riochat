package billing

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// LedgerItem is the user-facing form of a usage ledger row. It deliberately has
// no upstream name.
type LedgerItem struct {
	ID                  uint      `json:"id"`
	UserID              uint      `json:"userID"`
	ConversationID      uint      `json:"conversationID"`
	ProviderProtocol    string    `json:"providerProtocol"`
	PlatformModelName   string    `json:"platformModelName"`
	RoutedBindingCode   string    `json:"routedBindingCode"`
	UpstreamModelName   string    `json:"upstreamModelName"`
	ModelVendor         string    `json:"modelVendor"`
	ModelIcon           string    `json:"modelIcon"`
	IsFreeModel         bool      `json:"isFreeModel"`
	BillingAt           time.Time `json:"billingAt"`
	UsageDate           time.Time `json:"usageDate"`
	InputTokens         int64     `json:"inputTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
	CacheWriteTokens    int64     `json:"cacheWriteTokens"`
	CacheWrite5mTokens  int64     `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens  int64     `json:"cacheWrite1hTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	ReasoningTokens     int64     `json:"reasoningTokens"`
	CallCount           int64     `json:"callCount"`
	DurationSeconds     int64     `json:"durationSeconds"`
	LatencyMS           int64     `json:"latencyMS"`
	UsageSpeed          string    `json:"usageSpeed"`
	ServiceTier         string    `json:"serviceTier"`
	BilledCurrency      string    `json:"billedCurrency"`
	BilledNanousd       int64     `json:"billedNanousd"`
	BilledUSD           float64   `json:"billedUSD"`
	BalanceAfterNanousd *int64    `json:"balanceAfterNanousd"`
	BalanceAfterUSD     *float64  `json:"balanceAfterUSD"`
	PricingSnapshotJSON string    `json:"pricingSnapshotJSON"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// AdminLedgerItem adds the upstream name and user labels for administrators.
type AdminLedgerItem struct {
	LedgerItem
	UpstreamName    string `json:"upstreamName"`
	Username        string `json:"username"`
	UserDisplayName string `json:"userDisplayName"`
	UserLabel       string `json:"userLabel"`
}

func snapshotString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func ledgerItem(l UsageLedger, snap string) LedgerItem {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(l.PricingSnapshotJSON), &m)
	it := LedgerItem{
		ID: l.ID, UserID: l.UserID, ConversationID: l.ConversationID, ProviderProtocol: l.ProviderProtocol,
		PlatformModelName: l.PlatformModelName, RoutedBindingCode: l.RoutedBindingCode, UpstreamModelName: l.UpstreamModelName,
		ModelVendor: snapshotString(m, "model_vendor"), ModelIcon: snapshotString(m, "model_icon"), IsFreeModel: l.IsFreeModel,
		BillingAt: l.BillingAt, UsageDate: dateTime(l.UsageDate, l.BillingAt.Location()),
		InputTokens: l.InputTokens, CacheReadTokens: l.CacheReadTokens, CacheWriteTokens: l.CacheWriteTokens,
		CacheWrite5mTokens: l.CacheWrite5mTokens, CacheWrite1hTokens: l.CacheWrite1hTokens, OutputTokens: l.OutputTokens,
		ReasoningTokens: l.ReasoningTokens, CallCount: l.CallCount, DurationSeconds: l.DurationSeconds, LatencyMS: l.LatencyMS,
		UsageSpeed: l.UsageSpeed, ServiceTier: l.ServiceTier, BilledCurrency: l.BilledCurrency, BilledNanousd: l.BilledNanousd,
		BilledUSD: NanoToUSD(l.BilledNanousd), BalanceAfterNanousd: l.BalanceAfterNanousd, PricingSnapshotJSON: snap,
		CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
	if l.BalanceAfterNanousd != nil {
		v := NanoToUSD(*l.BalanceAfterNanousd)
		it.BalanceAfterUSD = &v
	}
	return it
}

func dateTime(d DateOnly, loc *time.Location) time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	if loc == nil {
		loc = time.UTC
	}
	return d.In(loc)
}

// sanitizeSnapshot removes upstream provider naming from a stored snapshot:
// any key whose lower-cased, underscore-free name is "upstreamname", and the
// name/displayname keys inside an object stored under "upstream".
func sanitizeSnapshot(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "{}"
	}
	b, err := json.Marshal(scrubUpstream(v))
	if err != nil {
		return "{}"
	}
	return string(b)
}

func scrubUpstream(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			norm := strings.ReplaceAll(strings.ToLower(k), "_", "")
			if norm == "upstreamname" {
				continue
			}
			child := scrubUpstream(val)
			if k == "upstream" {
				if obj, ok := child.(map[string]any); ok {
					for ck := range obj {
						n := strings.ReplaceAll(strings.ToLower(ck), "_", "")
						if n == "name" || n == "displayname" {
							delete(obj, ck)
						}
					}
				}
			}
			out[k] = child
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = scrubUpstream(e)
		}
		return out
	}
	return v
}

// UsageQuery filters a user's usage list.
type UsageQuery struct {
	Query  string
	Status string
	Sort   string
}

func orderClause(sort string) string {
	switch sort {
	case "oldest":
		return "usage_date ASC, id ASC"
	case "tokens_desc":
		return "(input_tokens + cache_read_tokens + cache_write_tokens + output_tokens + reasoning_tokens) DESC, id DESC"
	case "cost_desc":
		return "billed_nanousd DESC, id DESC"
	case "latency_desc":
		return "latency_ms DESC, id DESC"
	}
	return "usage_date DESC, id DESC"
}

// ListUsage returns a page of the user's own usage ledger.
func (s *Service) ListUsage(ctx context.Context, userID uint, q UsageQuery, offset, limit int) ([]LedgerItem, int64, error) {
	db := s.db.WithContext(ctx).Model(&UsageLedger{}).Where("user_id = ?", userID)
	if strings.TrimSpace(q.Query) != "" {
		db = db.Where(`LOWER(platform_model_name) LIKE ? ESCAPE '\'`, likePattern(q.Query))
	}
	switch q.Status {
	case "free":
		db = db.Where("is_free_model = ?", true)
	case "billable":
		db = db.Where("is_free_model = ?", false)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []UsageLedger
	if err := db.Order(orderClause(q.Sort)).Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	// Balance fallback from the matching usage_debit transaction.
	var missing []uint
	for _, r := range rows {
		if r.BalanceAfterNanousd == nil {
			missing = append(missing, r.ID)
		}
	}
	fallback := map[uint]int64{}
	if len(missing) > 0 {
		var txs []BalanceTransaction
		if err := s.db.WithContext(ctx).Where("type = ? AND ref_type = ? AND ref_id IN ?", TxUsageDebit, "usage_ledger", missing).
			Order("id ASC").Find(&txs).Error; err != nil {
			return nil, 0, err
		}
		for _, t := range txs {
			fallback[t.RefID] = t.BalanceAfterNanousd
		}
	}
	items := make([]LedgerItem, 0, len(rows))
	for _, r := range rows {
		if v, ok := fallback[r.ID]; ok && r.BalanceAfterNanousd == nil {
			vv := v
			r.BalanceAfterNanousd = &vv
		}
		li := ledgerItem(r, sanitizeSnapshot(r.PricingSnapshotJSON))
		li.UsageDate = dateTime(r.UsageDate, s.loc)
		items = append(items, li)
	}
	return items, total, nil
}

// Metrics aggregates ledger rows.
type Metrics struct {
	RecordCount      int64   `json:"recordCount"`
	InputTokens      int64   `json:"inputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CallCount        int64   `json:"callCount"`
	DurationSeconds  int64   `json:"durationSeconds"`
	AvgLatencyMS     int64   `json:"avgLatencyMS"`
	BilledNanousd    int64   `json:"billedNanousd"`
	BilledUSD        float64 `json:"billedUSD"`
}

type metricAcc struct {
	records, in, cr, cw, out, reason, calls, dur, billed int64
	latSum, latN                                         int64
}

func (a *metricAcc) add(l UsageLedger) {
	a.records++
	a.in += l.InputTokens
	a.cr += l.CacheReadTokens
	a.cw += l.CacheWriteTokens
	a.out += l.OutputTokens
	a.reason += l.ReasoningTokens
	a.calls += l.CallCount
	a.dur += l.DurationSeconds
	a.billed = satAdd(a.billed, l.BilledNanousd)
	if l.LatencyMS > 0 {
		a.latSum += l.LatencyMS
		a.latN++
	}
}

func (a metricAcc) avgLatency() int64 {
	if a.latN == 0 {
		return 0
	}
	return int64(math.Round(float64(a.latSum) / float64(a.latN)))
}

func (a metricAcc) metrics() Metrics {
	return Metrics{RecordCount: a.records, InputTokens: a.in, CacheReadTokens: a.cr, CacheWriteTokens: a.cw, OutputTokens: a.out,
		ReasoningTokens: a.reason, TotalTokens: a.in + a.cr + a.cw + a.out + a.reason, CallCount: a.calls, DurationSeconds: a.dur,
		AvgLatencyMS: a.avgLatency(), BilledNanousd: a.billed, BilledUSD: NanoToUSD(a.billed)}
}

// MonthSummary is one month of the monthly usage series.
type MonthSummary struct {
	MonthStartAt time.Time `json:"monthStartAt"`
	Metrics
}

// MonthlyUsage returns the last n months (default 12, capped at 24), oldest
// first, zero-filled.
func (s *Service) MonthlyUsage(ctx context.Context, userID uint, months int) ([]MonthSummary, error) {
	if months <= 0 {
		months = 12
	}
	if months > 24 {
		months = 24
	}
	nowL := s.now().In(s.loc)
	cur := time.Date(nowL.Year(), nowL.Month(), 1, 0, 0, 0, 0, s.loc)
	first := cur.AddDate(0, -(months - 1), 0)
	end := cur.AddDate(0, 1, 0)
	var rows []UsageLedger
	if err := s.db.WithContext(ctx).Where("user_id = ? AND usage_date >= ? AND usage_date < ?", userID, DateOf(first).String(), DateOf(end).String()).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	acc := map[string]*metricAcc{}
	for _, r := range rows {
		key := r.UsageDate.String()[:7]
		if acc[key] == nil {
			acc[key] = &metricAcc{}
		}
		acc[key].add(r)
	}
	out := make([]MonthSummary, 0, months)
	for i := 0; i < months; i++ {
		m := first.AddDate(0, i, 0)
		ms := MonthSummary{MonthStartAt: m}
		if a := acc[m.Format("2006-01")]; a != nil {
			ms.Metrics = a.metrics()
		}
		out = append(out, ms)
	}
	return out, nil
}

// ModelDay is one model's slice of a day.
type ModelDay struct {
	PlatformModelName string `json:"platformModelName"`
	Metrics
}

// DaySummary is one day of the daily usage series.
type DaySummary struct {
	UsageDate time.Time `json:"usageDate"`
	Metrics
	Models []ModelDay `json:"models"`
}

// DailyQuery selects the daily window (see the spec for precedence).
type DailyQuery struct {
	StartDate string
	EndDate   string
	Days      string
	HasDays   bool
}

func parseDay(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), loc)
	return t, err == nil
}

// anchoredCycle returns [start, end) of the month-long window beginning on the
// day-of-month of the user's registration date and containing today.
func anchoredCycle(created, today time.Time, loc *time.Location) (time.Time, time.Time) {
	anchor := created.In(loc).Day()
	at := func(y int, m time.Month) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
		d := anchor
		if d > last {
			d = last
		}
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	start := at(today.Year(), today.Month())
	if start.After(today) {
		p := time.Date(today.Year(), today.Month()-1, 1, 0, 0, 0, 0, loc)
		start = at(p.Year(), p.Month())
	}
	// Derive the next cycle from the calendar month, never by adding a month to a
	// possibly clamped day (31 January + 1 month would skip February).
	n := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, loc)
	end := at(n.Year(), n.Month())
	if !end.After(start) {
		end = start.AddDate(0, 0, 28)
	}
	return start, end
}

// DailyUsage returns per-day summaries with per-model breakdowns.
func (s *Service) DailyUsage(ctx context.Context, userID uint, q DailyQuery) ([]DaySummary, error) {
	nowL := s.now().In(s.loc)
	today := time.Date(nowL.Year(), nowL.Month(), nowL.Day(), 0, 0, 0, 0, s.loc)
	var start, end time.Time
	switch {
	case strings.TrimSpace(q.StartDate) != "" && strings.TrimSpace(q.EndDate) != "":
		st, ok1 := parseDay(q.StartDate, s.loc)
		en, ok2 := parseDay(q.EndDate, s.loc)
		if !ok1 || !ok2 {
			return nil, ErrInvalidDailyRange
		}
		start, end = st, en
		if !end.After(start) {
			end = start.AddDate(0, 0, 1)
		}
		if end.After(start.AddDate(0, 0, 90)) {
			end = start.AddDate(0, 0, 90)
		}
	case !q.HasDays:
		var created time.Time
		if s.ports.Users != nil {
			if m, err := s.ports.Users.Users(ctx, []uint{userID}); err == nil {
				created = m[userID].CreatedAt
			}
		}
		if created.IsZero() {
			created = time.Date(2000, 1, 1, 0, 0, 0, 0, s.loc)
		}
		start, end = anchoredCycle(created, today, s.loc)
	default:
		n, ok := atoiPositive(q.Days)
		if !ok {
			return nil, ErrInvalidDailyDays
		}
		if n > 90 {
			n = 90
		}
		end = today.AddDate(0, 0, 1)
		start = end.AddDate(0, 0, -n)
	}
	var rows []UsageLedger
	if err := s.db.WithContext(ctx).Where("user_id = ? AND usage_date >= ? AND usage_date < ?", userID, DateOf(start).String(), DateOf(end).String()).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	type dayAcc struct {
		total  metricAcc
		models map[string]*metricAcc
	}
	days := map[string]*dayAcc{}
	for _, r := range rows {
		key := r.UsageDate.String()
		d := days[key]
		if d == nil {
			d = &dayAcc{models: map[string]*metricAcc{}}
			days[key] = d
		}
		d.total.add(r)
		m := d.models[r.PlatformModelName]
		if m == nil {
			m = &metricAcc{}
			d.models[r.PlatformModelName] = m
		}
		m.add(r)
	}
	var out []DaySummary
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		ds := DaySummary{UsageDate: day, Models: []ModelDay{}}
		if d := days[DateOf(day).String()]; d != nil {
			ds.Metrics = d.total.metrics()
			var latSum, latN float64
			for name, m := range d.models {
				ds.Models = append(ds.Models, ModelDay{PlatformModelName: name, Metrics: m.metrics()})
				latSum += float64(m.avgLatency()) * float64(m.latN)
				latN += float64(m.latN)
			}
			ds.AvgLatencyMS = 0
			if latN > 0 {
				ds.AvgLatencyMS = int64(math.Round(latSum / latN))
			}
			sort.Slice(ds.Models, func(i, j int) bool {
				a, b := ds.Models[i], ds.Models[j]
				if a.BilledNanousd != b.BilledNanousd {
					return a.BilledNanousd > b.BilledNanousd
				}
				return a.PlatformModelName < b.PlatformModelName
			})
		}
		out = append(out, ds)
	}
	return out, nil
}

func atoiPositive(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			n = 1_000_000
		}
	}
	return n, n > 0
}

// DailyActivity is the per-day activity count used by the user profile area.
type DailyActivity struct {
	Date   DateOnly
	Count  int64
	Tokens int64
}

// ActivityByDay counts a user's non-service-only ledger rows per day with their
// summed tokens, for days in [from, to).
func (s *Service) ActivityByDay(ctx context.Context, userID uint, from, to time.Time) ([]DailyActivity, error) {
	var rows []UsageLedger
	if err := s.db.WithContext(ctx).Where("user_id = ? AND usage_date >= ? AND usage_date < ?", userID, DateOf(from.In(s.loc)).String(), DateOf(to.In(s.loc)).String()).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	acc := map[string]*DailyActivity{}
	for _, r := range rows {
		var snap struct {
			ServiceOnly bool `json:"service_only"`
		}
		_ = json.Unmarshal([]byte(r.PricingSnapshotJSON), &snap)
		if snap.ServiceOnly {
			continue
		}
		k := r.UsageDate.String()
		a := acc[k]
		if a == nil {
			a = &DailyActivity{Date: r.UsageDate}
			acc[k] = a
		}
		a.Count++
		a.Tokens += r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens + r.OutputTokens + r.ReasoningTokens
	}
	out := make([]DailyActivity, 0, len(acc))
	for _, a := range acc {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.String() < out[j].Date.String() })
	return out, nil
}

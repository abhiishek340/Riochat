package billing

import (
	"context"
	"sort"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// StatsQuery selects the usage statistics of the administrator dashboard.
type StatsQuery struct {
	StartDate         string
	EndDate           string
	UserID            uint
	PermissionGroupID uint
	PlatformModelName string
	BillingScope      string
	Section           string
	ModelRankBy       string
	UserRankBy        string
}

// StatsRange echoes the resolved range.
type StatsRange struct {
	StartDate   string `json:"startDate"`
	EndDate     string `json:"endDate"`
	Granularity string `json:"granularity"`
}

// TrendPoint is one period of a trend.
type TrendPoint struct {
	PeriodStart time.Time `json:"periodStart"`
	Metrics
}

// ModelStat ranks one model.
type ModelStat struct {
	PlatformModelName string `json:"platformModelName"`
	Metrics
	Trend []TrendPoint `json:"trend"`
}

// UserStat ranks one user.
type UserStat struct {
	UserID          uint   `json:"userID"`
	Username        string `json:"username"`
	UserDisplayName string `json:"userDisplayName"`
	UserLabel       string `json:"userLabel"`
	Metrics
	Trend []TrendPoint `json:"trend"`
}

// Stats is the usage statistics payload.
type Stats struct {
	Section   string       `json:"section"`
	Range     StatsRange   `json:"range"`
	Totals    Metrics      `json:"totals"`
	Trend     []TrendPoint `json:"trend"`
	TopModels []ModelStat  `json:"topModels"`
	TopUsers  []UserStat   `json:"topUsers"`
}

const statsTopN = 10

func weekStart(t time.Time) time.Time {
	back := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -back)
}

func bucketOf(gran string, t time.Time) time.Time {
	switch gran {
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "week":
		return weekStart(t)
	}
	return t
}

func nextBucket(gran string, t time.Time) time.Time {
	switch gran {
	case "month":
		return t.AddDate(0, 1, 0)
	case "week":
		return t.AddDate(0, 0, 7)
	}
	return t.AddDate(0, 0, 1)
}

type statAcc struct {
	total  metricAcc
	trend  map[time.Time]*metricAcc
	userID uint
	name   string
}

func (a *statAcc) add(bucket time.Time, l UsageLedger) {
	a.total.add(l)
	if a.trend == nil {
		a.trend = map[time.Time]*metricAcc{}
	}
	t := a.trend[bucket]
	if t == nil {
		t = &metricAcc{}
		a.trend[bucket] = t
	}
	t.add(l)
}

func (a *statAcc) trendPoints() []TrendPoint {
	keys := make([]time.Time, 0, len(a.trend))
	for k := range a.trend {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	out := make([]TrendPoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, TrendPoint{PeriodStart: k, Metrics: a.trend[k].metrics()})
	}
	return out
}

func rankValue(m metricAcc, by string) int64 {
	switch by {
	case "tokens":
		return m.in + m.cr + m.cw + m.out + m.reason
	case "calls":
		return m.calls
	}
	return m.billed
}

func rankSort(list []*statAcc, by string, byUser bool) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if va, vb := rankValue(a.total, by), rankValue(b.total, by); va != vb {
			return va > vb
		}
		if ta, tb := rankValue(a.total, "tokens"), rankValue(b.total, "tokens"); ta != tb {
			return ta > tb
		}
		if a.total.billed != b.total.billed {
			return a.total.billed > b.total.billed
		}
		if byUser {
			return a.userID < b.userID
		}
		return a.name < b.name
	})
}

func validRank(v string) (string, bool) {
	switch v {
	case "":
		return "cost", true
	case "cost", "tokens", "calls":
		return v, true
	}
	return "", false
}

// UsageStatistics aggregates the usage ledger for administrators.
func (s *Service) UsageStatistics(ctx context.Context, q StatsQuery) (*Stats, error) {
	nowL := s.now().In(s.loc)
	today := time.Date(nowL.Year(), nowL.Month(), nowL.Day(), 0, 0, 0, 0, s.loc)
	var start, end time.Time
	switch {
	case q.StartDate == "" && q.EndDate == "":
		end = today
		start = today.AddDate(0, 0, -29)
	case q.StartDate == "" || q.EndDate == "":
		return nil, ErrStatsInvalidRange
	default:
		st, ok1 := parseDay(q.StartDate, s.loc)
		en, ok2 := parseDay(q.EndDate, s.loc)
		if !ok1 || !ok2 {
			return nil, ErrStatsInvalidRange
		}
		start, end = st, en
	}
	if end.Before(start) {
		return nil, ErrStatsInvalidRange
	}
	spanDays := int(end.Sub(start).Hours()/24+0.5) + 1
	if spanDays > 366 {
		return nil, ErrStatsInvalidRange
	}
	if q.UserID != 0 && q.PermissionGroupID != 0 {
		return nil, ErrStatsSubject
	}
	scope := q.BillingScope
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "free" && scope != "billable" {
		return nil, ErrStatsInvalidScope
	}
	section := q.Section
	if section == "" {
		section = "all"
	}
	if section != "all" && section != "models" && section != "users" {
		return nil, ErrStatsInvalidSection
	}
	modelRank, ok1 := validRank(q.ModelRankBy)
	userRank, ok2 := validRank(q.UserRankBy)
	if !ok1 || !ok2 {
		return nil, ErrStatsInvalidRank
	}
	gran := "day"
	switch {
	case spanDays >= 180:
		gran = "month"
	case spanDays > 30:
		gran = "week"
	}

	db := s.db.WithContext(ctx).Model(&UsageLedger{}).
		Where("usage_date >= ? AND usage_date < ?", DateOf(start).String(), DateOf(end.AddDate(0, 0, 1)).String())
	if q.UserID != 0 {
		db = db.Where("user_id = ?", q.UserID)
	}
	if q.PermissionGroupID != 0 {
		ids, everyone, err := s.groupUserIDs(ctx, q.PermissionGroupID)
		if err != nil {
			return nil, err
		}
		if !everyone {
			if len(ids) == 0 {
				db = db.Where("1 = 0")
			} else {
				db = db.Where("user_id IN ?", ids)
			}
		}
	}
	if q.PlatformModelName != "" {
		db = db.Where("platform_model_name = ?", q.PlatformModelName)
	}
	switch scope {
	case "free":
		db = db.Where("is_free_model = ?", true)
	case "billable":
		db = db.Where("is_free_model = ?", false)
	}
	var rows []UsageLedger
	if err := db.Select("id", "user_id", "platform_model_name", "usage_date", "input_tokens", "cache_read_tokens", "cache_write_tokens",
		"output_tokens", "reasoning_tokens", "call_count", "duration_seconds", "latency_ms", "billed_nanousd").Find(&rows).Error; err != nil {
		return nil, err
	}

	var totals metricAcc
	overview := map[time.Time]*metricAcc{}
	models := map[string]*statAcc{}
	users := map[uint]*statAcc{}
	for _, r := range rows {
		day := r.UsageDate.In(s.loc)
		b := bucketOf(gran, day)
		totals.add(r)
		if overview[b] == nil {
			overview[b] = &metricAcc{}
		}
		overview[b].add(r)
		if section != "users" {
			m := models[r.PlatformModelName]
			if m == nil {
				m = &statAcc{name: r.PlatformModelName}
				models[r.PlatformModelName] = m
			}
			m.add(b, r)
		}
		if section != "models" {
			u := users[r.UserID]
			if u == nil {
				u = &statAcc{userID: r.UserID}
				users[r.UserID] = u
			}
			u.add(b, r)
		}
	}
	out := &Stats{Section: section, Range: StatsRange{StartDate: DateOf(start).String(), EndDate: DateOf(end).String(), Granularity: gran},
		Totals: totals.metrics(), Trend: []TrendPoint{}, TopModels: []ModelStat{}, TopUsers: []UserStat{}}
	if section == "all" {
		for b := bucketOf(gran, start); !b.After(end); b = nextBucket(gran, b) {
			p := TrendPoint{PeriodStart: b}
			if a := overview[b]; a != nil {
				p.Metrics = a.metrics()
			}
			out.Trend = append(out.Trend, p)
		}
	} else {
		keys := make([]time.Time, 0, len(overview))
		for k := range overview {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
		for _, k := range keys {
			out.Trend = append(out.Trend, TrendPoint{PeriodStart: k, Metrics: overview[k].metrics()})
		}
	}
	if section != "users" {
		list := make([]*statAcc, 0, len(models))
		for _, m := range models {
			list = append(list, m)
		}
		rankSort(list, modelRank, false)
		for i, m := range list {
			if i == statsTopN {
				break
			}
			out.TopModels = append(out.TopModels, ModelStat{PlatformModelName: m.name, Metrics: m.total.metrics(), Trend: m.trendPoints()})
		}
	}
	if section != "models" {
		list := make([]*statAcc, 0, len(users))
		for _, u := range users {
			list = append(list, u)
		}
		rankSort(list, userRank, true)
		if len(list) > statsTopN {
			list = list[:statsTopN]
		}
		ids := make([]uint, 0, len(list))
		for _, u := range list {
			ids = append(ids, u.userID)
		}
		labels := s.labelsFor(ctx, ids)
		for _, u := range list {
			l := labels[u.userID]
			out.TopUsers = append(out.TopUsers, UserStat{UserID: u.userID, Username: l.Username, UserDisplayName: l.DisplayName,
				UserLabel: l.Label, Metrics: u.total.metrics(), Trend: u.trendPoints()})
		}
	}
	return out, nil
}

// groupUserIDs resolves who counts as a member of a permission group right now:
// everyone for the default group, otherwise manual members plus users whose
// current active subscription plan binds the group.
func (s *Service) groupUserIDs(ctx context.Context, groupID uint) (ids []uint, everyone bool, err error) {
	if s.ports.Groups == nil {
		return nil, false, apperr.ErrNotFound.WithStatus(404)
	}
	gm, err := s.ports.Groups.Members(ctx, groupID)
	if err != nil {
		return nil, false, err
	}
	if !gm.Exists {
		return nil, false, apperr.ErrNotFound.WithStatus(404)
	}
	if gm.Everyone {
		return nil, true, nil
	}
	set := map[uint]bool{}
	for _, id := range gm.UserIDs {
		set[id] = true
	}
	now := s.now()
	var subs []Subscription
	if err := s.db.WithContext(ctx).
		Where("status = ? AND current_period_start_at <= ? AND (current_period_end_at IS NULL OR current_period_end_at > ?) AND plan_id IN (?)",
			SubActive, now, now, s.db.Model(&Plan{}).Select("id").Where("permission_group_id = ? AND is_active = ?", groupID, true)).
		Find(&subs).Error; err != nil {
		return nil, false, err
	}
	for _, sub := range subs {
		set[sub.UserID] = true
	}
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, false, nil
}

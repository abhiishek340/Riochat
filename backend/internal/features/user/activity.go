package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

const (
	defaultActivityDays = 365
	maxActivityDays     = 366
	dateLayout          = "2006-01-02"
)

// activityItem is one day of the heat map.
type activityItem struct {
	Date         string `json:"date"`
	RequestCount int64  `json:"requestCount"`
	TokenUsage   int64  `json:"tokenUsage"`
}

// activityWindow lists the N calendar days ending on today's date (in the
// clock's own location), oldest first.
func activityWindow(now time.Time, days int) []string {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	out := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		out = append(out, today.AddDate(0, 0, -i).Format(dateLayout))
	}
	return out
}

// buildActivity zero-fills the window from the aggregated rows.
func buildActivity(window []string, rows []DayActivity) []activityItem {
	byDate := make(map[string]DayActivity, len(rows))
	for _, r := range rows {
		cur := byDate[r.Date]
		cur.Requests += r.Requests
		cur.Tokens += r.Tokens
		byDate[r.Date] = cur
	}
	items := make([]activityItem, len(window))
	for i, day := range window {
		r := byDate[day]
		items[i] = activityItem{Date: day, RequestCount: r.Requests, TokenUsage: r.Tokens}
	}
	return items
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func parseActivityDays(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultActivityDays, nil
	}
	n, err := strconv.Atoi(raw)
	var numErr *strconv.NumError
	if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) && allDigits(strings.TrimPrefix(raw, "+")) {
		// An integer too large for int is still an integer: clamp it.
		return maxActivityDays, nil
	}
	if err != nil || n <= 0 {
		return 0, ErrInvalidActivityDay
	}
	return min(n, maxActivityDays), nil
}

func activityHandler(src ActivitySource, clk clock.Clock) gin.HandlerFunc {
	return func(c *gin.Context) {
		days, err := parseActivityDays(c.Query("days"))
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, err)
			return
		}
		if src == nil {
			httpx.Fail(c, http.StatusInternalServerError, ErrInternal.WithCause(fmt.Errorf("activity source is not configured")))
			return
		}
		window := activityWindow(clk.Now(), days)
		end, _ := time.Parse(dateLayout, window[len(window)-1])
		rows, err := src.DailyActivity(c.Request.Context(), middleware.MustPrincipal(c).UserID,
			window[0], end.AddDate(0, 0, 1).Format(dateLayout))
		if err != nil {
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		httpx.OK(c, buildActivity(window, rows))
	}
}

// ---- default source: the billing usage ledger -----------------------------

// ledgerActivity reads billing_usage_ledgers directly. The billing area owns
// that table; only the columns below are read.
type ledgerActivity struct{ db *gorm.DB }

type ledgerRow struct {
	UsageDate        string
	InputTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	OutputTokens     int64
	ReasoningTokens  int64
	Snapshot         string
}

// serviceOnly reports whether a ledger row records an internal call (title,
// label or compaction). An explicit flag wins; rows written before the flag
// existed are recognized by the code of their first service item.
func serviceOnly(snapshot string) bool {
	if strings.TrimSpace(snapshot) == "" {
		return false
	}
	var snap struct {
		ServiceOnly  *bool `json:"service_only"`
		ServiceItems []struct {
			Code string `json:"service_code"`
		} `json:"service_items"`
	}
	if err := json.Unmarshal([]byte(snapshot), &snap); err != nil {
		return false
	}
	if snap.ServiceOnly != nil {
		return *snap.ServiceOnly
	}
	if len(snap.ServiceItems) > 0 {
		switch snap.ServiceItems[0].Code {
		case "title", "labels", "compact":
			return true
		}
	}
	return false
}

func (l ledgerActivity) DailyActivity(ctx context.Context, userID uint, from, to string) ([]DayActivity, error) {
	var rows []ledgerRow
	err := l.db.WithContext(ctx).Raw(
		`SELECT CAST(usage_date AS TEXT) AS usage_date, input_tokens, cache_read_tokens, cache_write_tokens,
		        output_tokens, reasoning_tokens, pricing_snapshot_json AS snapshot
		   FROM billing_usage_ledgers
		  WHERE user_id = ? AND usage_date >= ? AND usage_date < ? AND deleted_at IS NULL`,
		userID, from, to).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	acc := map[string]*DayActivity{}
	for _, r := range rows {
		if serviceOnly(r.Snapshot) {
			continue
		}
		day := r.UsageDate
		if len(day) > 10 {
			day = day[:10]
		}
		a := acc[day]
		if a == nil {
			a = &DayActivity{Date: day}
			acc[day] = a
		}
		a.Requests++
		a.Tokens += r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens + r.OutputTokens + r.ReasoningTokens
	}
	out := make([]DayActivity, 0, len(acc))
	for _, a := range acc {
		out = append(out, *a)
	}
	return out, nil
}

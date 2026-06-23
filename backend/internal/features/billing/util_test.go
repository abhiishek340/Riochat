package billing

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	testkit.Decode(t, w, &out)
	return out
}

// seedPlanPrices returns the id of the price with the given code.
func (e *env) priceID(code string) uint {
	e.t.Helper()
	var p Price
	if err := e.DB.Where("code = ?", code).First(&p).Error; err != nil {
		e.t.Fatalf("price %s: %v", code, err)
	}
	return p.ID
}

func (e *env) planByCode(code string) Plan {
	e.t.Helper()
	var p Plan
	if err := e.DB.Where("code = ?", code).First(&p).Error; err != nil {
		e.t.Fatalf("plan %s: %v", code, err)
	}
	return p
}

// grantSub inserts an active subscription directly.
func (e *env) grantSub(userID uint, planCode string, start time.Time, end *time.Time) Subscription {
	e.t.Helper()
	p := e.planByCode(planCode)
	var price Price
	if err := e.DB.Where("plan_id = ? AND is_default = ?", p.ID, true).First(&price).Error; err != nil {
		e.t.Fatal(err)
	}
	sub := Subscription{UserID: userID, PlanID: p.ID, PriceID: price.ID, Status: "active", StartAt: start, CurrentPeriodStartAt: start, CurrentPeriodEndAt: end, AutoRenew: end != nil}
	if err := e.DB.Create(&sub).Error; err != nil {
		e.t.Fatal(err)
	}
	return sub
}

func uitoa(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func isCode(err error, want error) bool { return errors.Is(err, want) }

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

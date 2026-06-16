package billing

import (
	"errors"
	"strings"
	"time"
)

// Protocol identifiers that change pricing behavior.
const (
	protoAnthropic       = "anthropic_messages"
	protoOpenAIChat      = "openai_chat_completions"
	protoOpenAIResponses = "openai_responses"
)

func isOpenAIProtocol(p string) bool { return p == protoOpenAIChat || p == protoOpenAIResponses }

// priceSpec is a parsed model price row.
type priceSpec struct {
	Name         string
	Currency     string
	IsFree       bool
	Mode         string
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	Output       int64
	Call         int64
	Duration     int64
	Basis        string
	Tiers        *TierConfig
	Schedule     *ScheduleConfig
	TieredJSON   string
	ScheduleJSON string
}

func normalizePricingMode(m string) string {
	switch strings.TrimSpace(m) {
	case PricingCall:
		return PricingCall
	case PricingDuration:
		return PricingDuration
	case PricingTiered:
		return PricingTiered
	}
	return PricingToken
}

// specFromRow parses a stored price row. An unparsable tier table is an error
// (it would silently mis-bill); an unparsable schedule is ignored so billing is
// never blocked by it.
func specFromRow(row ModelPrice) (priceSpec, error) {
	sp := priceSpec{
		Name: row.PlatformModelName, Currency: row.Currency, IsFree: row.IsFree, Mode: normalizePricingMode(row.PricingMode),
		Input: row.InputNanousdPerMTokens, CacheRead: row.CacheReadNanousdPerMTokens, CacheWrite: row.CacheWriteNanousdPerMTokens,
		Output: row.OutputNanousdPerMTokens, Call: row.CallNanousdPerCall, Duration: row.DurationNanousdPerSecond,
		Basis: row.CacheWritePriceBasis, TieredJSON: row.TieredPricingJSON, ScheduleJSON: row.SchedulePricingJSON,
	}
	if sp.Currency == "" {
		sp.Currency = CurrencyUSD
	}
	if sp.Mode == PricingTiered {
		tc, err := parseTiersJSON(row.TieredPricingJSON)
		if err != nil {
			return sp, err
		}
		sp.Tiers = tc
	}
	if sc, err := parseScheduleJSON(row.SchedulePricingJSON); err == nil {
		sp.Schedule = sc
	}
	return sp, nil
}

// usageCounts is the metered usage of one primary or service call.
type usageCounts struct {
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	CacheWrite5m int64
	CacheWrite1h int64
	Output       int64
	Reasoning    int64
	Calls        int64
	Seconds      int64
}

// rateSet holds unit prices in nano-dollars (per million tokens, per call, per
// second).
type rateSet struct {
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	CacheWrite5m int64
	CacheWrite1h int64
	Output       int64
	Call         int64
	Duration     int64
}

// multiplier is the combined rate multiplier of a call with its provenance.
type multiplier struct {
	Base            ratio
	GroupPercent    int
	SchedulePercent int
	ScheduleName    string
	Total           ratio
}

func (m multiplier) hasSchedule() bool { return m.ScheduleName != "" }

// cacheBucket normalizes the cache TTL to "5m" or "1h".
func cacheBucket(timeout string) string {
	if strings.EqualFold(strings.TrimSpace(timeout), "1h") {
		return "1h"
	}
	return "5m"
}

// speedAndTier derives the billing speed and service tier of a call.
// Speed matters for Anthropic only, the tier for the OpenAI protocols only.
func billingSpeed(protocol, usageSpeed, requestSpeed string) string {
	if protocol != protoAnthropic {
		return ""
	}
	s := strings.ToLower(strings.TrimSpace(usageSpeed))
	if s == "" {
		s = strings.ToLower(strings.TrimSpace(requestSpeed))
	}
	return s
}

func billingTier(protocol, usageTier string) string {
	if !isOpenAIProtocol(protocol) {
		return ""
	}
	switch t := strings.ToLower(strings.TrimSpace(usageTier)); t {
	case "default", "flex", "priority":
		return t
	}
	return "default"
}

// baseRatio is the protocol-level multiplier (fast mode, service tier).
func baseRatio(protocol, speed, tier, platformModel, upstreamModel string) ratio {
	switch {
	case protocol == protoAnthropic:
		if speed == "fast" {
			return ratio{6, 1}
		}
	case isOpenAIProtocol(protocol):
		switch tier {
		case "flex":
			return ratio{1, 2}
		case "priority":
			for _, n := range []string{platformModel, upstreamModel} {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(n)), "gpt-5.5") {
					return ratio{5, 2}
				}
			}
			return ratio{2, 1}
		}
	}
	return one
}

// effectiveCacheWriteRate resolves the cache-write unit price for a cache
// bucket. basis "direct" and non-Anthropic protocols use the configured rate;
// "anthropic_5m" means the configured rate already carries the 5-minute
// premium; the legacy empty basis treats it as the plain input-like rate.
func effectiveCacheWriteRate(basis, protocol, bucket string, configured int64) int64 {
	if basis == "direct" || protocol != protoAnthropic {
		return configured
	}
	if configured <= 0 {
		return 0
	}
	if basis == "anthropic_5m" {
		if bucket == "1h" {
			return configured * 8 / 5
		}
		return configured
	}
	if bucket == "1h" {
		return configured * 2
	}
	return configured * 5 / 4
}

// CacheWriteMultipliers reports the effective cache-write multipliers (5m, 1h)
// of a model under the Anthropic protocol, evaluated for a 1 USD configured
// rate.
func CacheWriteMultipliers(basis string) (m5, m1h float64) {
	const unit = NanoPerUSD
	return NanoToUSD(effectiveCacheWriteRate(basis, protoAnthropic, "5m", unit)),
		NanoToUSD(effectiveCacheWriteRate(basis, protoAnthropic, "1h", unit))
}

// cacheSplit is the normalized cache-write breakdown.
type cacheSplit struct {
	Total int64
	T5m   int64
	T1h   int64
	Split bool // true when the 5m/1h breakdown is recorded (Anthropic only)
}

func splitCacheWrite(protocol string, total, t5m, t1h int64, timeout string) cacheSplit {
	total = clampNonNeg(total)
	if protocol != protoAnthropic {
		return cacheSplit{Total: total}
	}
	t5m, t1h = clampNonNeg(t5m), clampNonNeg(t1h)
	if sum := t5m + t1h; sum > total {
		total = sum
	}
	if rem := total - t5m - t1h; rem > 0 {
		if cacheBucket(timeout) == "1h" {
			t1h += rem
		} else {
			t5m += rem
		}
	}
	return cacheSplit{Total: total, T5m: t5m, T1h: t1h, Split: true}
}

// modelCost is the full result of pricing one call against a price.
type modelCost struct {
	Spec      priceSpec
	Base      rateSet // before multipliers
	Rates     rateSet // after multipliers
	Split     cacheSplit
	Input     int64
	CacheRead int64
	CacheW    int64
	CacheW5m  int64
	CacheW1h  int64
	Output    int64
	Call      int64
	Duration  int64
	Total     int64
	// tier details (tiered pricing only)
	Tiered    bool
	TierFrom  int64
	TierUpTo  *int64
	CacheRate int64 // recorded aggregate cache-write rate
}

// baseRates returns the unmultiplied unit prices for the given input-side
// token total, resolving tiers.
func (p priceSpec) baseRates(inputSide int64) (rs rateSet, tier *TierRate) {
	switch p.Mode {
	case PricingCall:
		rs.Call = p.Call
	case PricingDuration:
		rs.Duration = p.Duration
	case PricingTiered:
		t := p.Tiers.selectTier(inputSide)
		tier = &t
		rs.Input = USDToNano(t.InputUSDPerMTokens)
		rs.CacheRead = USDToNano(t.CacheReadUSDPerMTokens)
		rs.CacheWrite = USDToNano(t.CacheWriteUSDPerMTokens)
		rs.Output = USDToNano(t.OutputUSDPerMTokens)
	default:
		rs.Input, rs.CacheRead, rs.CacheWrite, rs.Output = p.Input, p.CacheRead, p.CacheWrite, p.Output
	}
	return rs, tier
}

func clampUsage(u usageCounts) usageCounts {
	u.Input, u.CacheRead, u.CacheWrite = clampNonNeg(u.Input), clampNonNeg(u.CacheRead), clampNonNeg(u.CacheWrite)
	u.Output, u.Reasoning, u.Calls, u.Seconds = clampNonNeg(u.Output), clampNonNeg(u.Reasoning), clampNonNeg(u.Calls), clampNonNeg(u.Seconds)
	return u
}

var errTiersMissing = errors.New("tiered pricing without tiers")

// priceUsage computes the cost of usage under spec. Free pricing yields zero
// cost but still resolves rates for the snapshot.
func priceUsage(spec priceSpec, u usageCounts, protocol, timeout string, mult ratio) (modelCost, error) {
	u = clampUsage(u)
	mc := modelCost{Spec: spec}
	if spec.Mode == PricingTiered && spec.Tiers == nil {
		return mc, errTiersMissing
	}
	split := splitCacheWrite(protocol, u.CacheWrite, u.CacheWrite5m, u.CacheWrite1h, timeout)
	mc.Split = split
	inputSide := u.Input + u.CacheRead + split.Total
	base, tier := spec.baseRates(inputSide)
	if tier != nil {
		mc.Tiered, mc.TierFrom, mc.TierUpTo = true, tier.FromTokens, tier.UpToTokens
	}
	// Effective cache-write rates (still before the multiplier).
	configured := base.CacheWrite
	bucket := cacheBucket(timeout)
	base.CacheWrite = effectiveCacheWriteRate(spec.Basis, protocol, bucket, configured)
	base.CacheWrite5m = effectiveCacheWriteRate(spec.Basis, protocol, "5m", configured)
	base.CacheWrite1h = effectiveCacheWriteRate(spec.Basis, protocol, "1h", configured)
	mc.Base = base
	r := rateSet{
		Input: mult.apply(base.Input), CacheRead: mult.apply(base.CacheRead), CacheWrite: mult.apply(base.CacheWrite),
		CacheWrite5m: mult.apply(base.CacheWrite5m), CacheWrite1h: mult.apply(base.CacheWrite1h),
		Output: mult.apply(base.Output), Call: mult.apply(base.Call), Duration: mult.apply(base.Duration),
	}
	mc.Rates = r
	switch spec.Mode {
	case PricingCall:
		mc.Call = unitAmount(maxI64(u.Calls, 1), r.Call)
	case PricingDuration:
		mc.Duration = unitAmount(u.Seconds, r.Duration)
	default:
		mc.Input = tokenAmount(u.Input, r.Input)
		mc.CacheRead = tokenAmount(u.CacheRead, r.CacheRead)
		if split.Split {
			mc.CacheW5m = tokenAmount(split.T5m, r.CacheWrite5m)
			mc.CacheW1h = tokenAmount(split.T1h, r.CacheWrite1h)
			mc.CacheW = satAdd(mc.CacheW5m, mc.CacheW1h)
			mc.CacheRate = r.CacheWrite
			if mc.CacheW > 0 && split.Total > 0 {
				mc.CacheRate = (mc.CacheW*1_000_000 + split.Total/2) / split.Total
			}
		} else {
			mc.CacheW = tokenAmount(split.Total, r.CacheWrite)
			mc.CacheRate = r.CacheWrite
		}
		mc.Output = tokenAmount(u.Output+u.Reasoning, r.Output)
	}
	mc.Total = satAdd(satAdd(satAdd(mc.Input, mc.CacheRead), satAdd(mc.CacheW, mc.Output)), satAdd(mc.Call, mc.Duration))
	return mc, nil
}

// scheduleAt returns the schedule period active at t (nil when none).
func (p priceSpec) scheduleAt(t time.Time, loc *time.Location) *SchedulePeriod {
	if p.IsFree || p.Schedule == nil {
		return nil
	}
	return p.Schedule.match(t.In(loc))
}

// composeMultiplier combines the protocol ratio with the permission-group
// percent and the schedule percent. Percent values of 100 (or <= 0 for the
// group rate) leave the multiplier unchanged.
func composeMultiplier(base ratio, groupPercent int, period *SchedulePeriod) multiplier {
	m := multiplier{Base: base, GroupPercent: 100, SchedulePercent: 100, Total: base}
	if groupPercent > 0 && groupPercent != 100 {
		m.GroupPercent = groupPercent
		m.Total = m.Total.mul(ratio{int64(groupPercent), 100})
	}
	if period != nil {
		m.ScheduleName = period.Name
		m.SchedulePercent = period.RatePercent
		if period.RatePercent != 100 {
			m.Total = m.Total.mul(ratio{int64(period.RatePercent), 100})
		}
	}
	return m
}

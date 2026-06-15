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

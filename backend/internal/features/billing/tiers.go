package billing

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

const maxTiers = 100

// TierRate is one input-size tier. Rates are USD per million tokens.
type TierRate struct {
	FromTokens              int64   `json:"fromTokens"`
	UpToTokens              *int64  `json:"upToTokens"`
	InputUSDPerMTokens      float64 `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens  float64 `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens float64 `json:"cacheWriteUSDPerMTokens"`
	OutputUSDPerMTokens     float64 `json:"outputUSDPerMTokens"`
}

// TierConfig is the parsed tieredPricingJSON.
type TierConfig struct {
	Tiers []TierRate `json:"tiers"`
}

func validRate(f float64) bool { return f >= 0 && !math.IsNaN(f) && !math.IsInf(f, 0) }

// normalizeTiers validates a tier list and fills in each tier's lower bound.
func normalizeTiers(c *TierConfig) (*TierConfig, error) {
	if c == nil || len(c.Tiers) == 0 {
		return nil, errors.New("tiers required")
	}
	if len(c.Tiers) > maxTiers {
		return nil, errors.New("too many tiers")
	}
	out := &TierConfig{}
	var prev int64
	for i, t := range c.Tiers {
		if !validRate(t.InputUSDPerMTokens) || !validRate(t.CacheReadUSDPerMTokens) ||
			!validRate(t.CacheWriteUSDPerMTokens) || !validRate(t.OutputUSDPerMTokens) {
			return nil, errors.New("invalid tier rate")
		}
		nt := TierRate{FromTokens: prev, InputUSDPerMTokens: t.InputUSDPerMTokens, CacheReadUSDPerMTokens: t.CacheReadUSDPerMTokens,
			CacheWriteUSDPerMTokens: t.CacheWriteUSDPerMTokens, OutputUSDPerMTokens: t.OutputUSDPerMTokens}
		if t.UpToTokens != nil && *t.UpToTokens > 0 {
			limit := *t.UpToTokens
			if limit <= prev {
				return nil, errors.New("tier limits must increase")
			}
			nt.UpToTokens = &limit
			prev = limit
		} else if i != len(c.Tiers)-1 {
			return nil, errors.New("only the last tier may be unbounded")
		}
		out.Tiers = append(out.Tiers, nt)
	}
	return out, nil
}

// parseTiersJSON parses and normalizes tier text.
func parseTiersJSON(text string) (*TierConfig, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("tiers required")
	}
	var c TierConfig
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return nil, err
	}
	return normalizeTiers(&c)
}

func marshalTiers(c *TierConfig) string {
	if c == nil {
		return "{}"
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// selectTier picks the tier for total input-side tokens. When the total exceeds
// every bounded tier the last tier applies with an open upper bound.
func (c *TierConfig) selectTier(total int64) TierRate {
	if total < 0 {
		total = 0
	}
	for _, t := range c.Tiers {
		if t.UpToTokens == nil || *t.UpToTokens >= total {
			return t
		}
	}
	last := c.Tiers[len(c.Tiers)-1]
	last.UpToTokens = nil
	return last
}

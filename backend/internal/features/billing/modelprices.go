package billing

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

const publicPriceCacheKey = "billing:public_prices"
const publicPriceCacheTTL = 30 * time.Second

// ModelPriceInput is the PUT body of a model price.
type ModelPriceInput struct {
	PlatformModelName       string   `json:"platformModelName" binding:"required,max=128"`
	Currency                string   `json:"currency" binding:"max=16"`
	IsFree                  *bool    `json:"isFree" binding:"required"`
	PricingMode             string   `json:"pricingMode" binding:"required,oneof=token call duration tiered"`
	InputUSDPerMTokens      *float64 `json:"inputUSDPerMTokens" binding:"required,gte=0"`
	CacheReadUSDPerMTokens  *float64 `json:"cacheReadUSDPerMTokens" binding:"required,gte=0"`
	CacheWriteUSDPerMTokens *float64 `json:"cacheWriteUSDPerMTokens" binding:"required,gte=0"`
	OutputUSDPerMTokens     *float64 `json:"outputUSDPerMTokens" binding:"required,gte=0"`
	CallUSDPerCall          *float64 `json:"callUSDPerCall" binding:"required,gte=0"`
	DurationUSDPerSecond    *float64 `json:"durationUSDPerSecond" binding:"required,gte=0"`
	CacheWritePriceBasis    string   `json:"cacheWritePriceBasis" binding:"omitempty,oneof=direct anthropic_5m"`
	TieredPricingJSON       string   `json:"tieredPricingJSON" binding:"max=20000"`
	SchedulePricingJSON     string   `json:"schedulePricingJSON" binding:"max=20000"`
}

// ModelPriceView is the API form of a model price.
type ModelPriceView struct {
	ID                          uint      `json:"id"`
	PlatformModelName           string    `json:"platformModelName"`
	ModelVendor                 string    `json:"modelVendor"`
	ModelIcon                   string    `json:"modelIcon"`
	Currency                    string    `json:"currency"`
	IsFree                      bool      `json:"isFree"`
	PricingMode                 string    `json:"pricingMode"`
	InputUSDPerMTokens          float64   `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens      float64   `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens     float64   `json:"cacheWriteUSDPerMTokens"`
	CacheWritePriceBasis        string    `json:"cacheWritePriceBasis,omitempty"`
	OutputUSDPerMTokens         float64   `json:"outputUSDPerMTokens"`
	CallUSDPerCall              float64   `json:"callUSDPerCall"`
	DurationUSDPerSecond        float64   `json:"durationUSDPerSecond"`
	TieredPricingJSON           string    `json:"tieredPricingJSON"`
	SchedulePricingJSON         string    `json:"schedulePricingJSON"`
	InputNanousdPerMTokens      int64     `json:"inputNanousdPerMTokens"`
	CacheReadNanousdPerMTokens  int64     `json:"cacheReadNanousdPerMTokens"`
	CacheWriteNanousdPerMTokens int64     `json:"cacheWriteNanousdPerMTokens"`
	OutputNanousdPerMTokens     int64     `json:"outputNanousdPerMTokens"`
	CallNanousdPerCall          int64     `json:"callNanousdPerCall"`
	DurationNanousdPerSecond    int64     `json:"durationNanousdPerSecond"`
	CreatedAt                   time.Time `json:"createdAt"`
	UpdatedAt                   time.Time `json:"updatedAt"`
}

func modelPriceView(m ModelPrice, info *ModelInfo) ModelPriceView {
	v := ModelPriceView{
		ID: m.ID, PlatformModelName: m.PlatformModelName, Currency: m.Currency, IsFree: m.IsFree, PricingMode: m.PricingMode,
		InputUSDPerMTokens: NanoToUSD(m.InputNanousdPerMTokens), CacheReadUSDPerMTokens: NanoToUSD(m.CacheReadNanousdPerMTokens),
		CacheWriteUSDPerMTokens: NanoToUSD(m.CacheWriteNanousdPerMTokens), CacheWritePriceBasis: m.CacheWritePriceBasis,
		OutputUSDPerMTokens: NanoToUSD(m.OutputNanousdPerMTokens), CallUSDPerCall: NanoToUSD(m.CallNanousdPerCall),
		DurationUSDPerSecond: NanoToUSD(m.DurationNanousdPerSecond), TieredPricingJSON: orEmptyObject(m.TieredPricingJSON),
		SchedulePricingJSON: orEmptyObject(m.SchedulePricingJSON), InputNanousdPerMTokens: m.InputNanousdPerMTokens,
		CacheReadNanousdPerMTokens: m.CacheReadNanousdPerMTokens, CacheWriteNanousdPerMTokens: m.CacheWriteNanousdPerMTokens,
		OutputNanousdPerMTokens: m.OutputNanousdPerMTokens, CallNanousdPerCall: m.CallNanousdPerCall,
		DurationNanousdPerSecond: m.DurationNanousdPerSecond, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
	if info != nil {
		v.ModelVendor, v.ModelIcon = info.Vendor, info.Icon
	}
	return v
}

func orEmptyObject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// ListModelPrices lists prices ordered by model name. With a model catalog only
// prices of currently active platform models are listed.
func (s *Service) ListModelPrices(ctx context.Context, q string, offset, limit int) ([]ModelPriceView, int64, error) {
	db := s.db.WithContext(ctx).Model(&ModelPrice{})
	info := map[string]ModelInfo{}
	if s.ports.Models != nil {
		models, err := s.ports.Models.ActiveModels(ctx)
		if err != nil {
			return nil, 0, err
		}
		names := make([]string, 0, len(models))
		for _, m := range models {
			info[m.Name] = m
			names = append(names, m.Name)
		}
		if len(names) == 0 {
			return []ModelPriceView{}, 0, nil
		}
		db = db.Where("platform_model_name IN ?", names)
	}
	if strings.TrimSpace(q) != "" {
		db = db.Where(`LOWER(platform_model_name) LIKE ? ESCAPE '\'`, likePattern(q))
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ModelPrice
	if err := db.Order("platform_model_name ASC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]ModelPriceView, 0, len(rows))
	for _, r := range rows {
		var ip *ModelInfo
		if mi, ok := info[r.PlatformModelName]; ok {
			ip = &mi
		}
		out = append(out, modelPriceView(r, ip))
	}
	return out, total, nil
}

// UpsertModelPrice validates and stores the price of a platform model.
func (s *Service) UpsertModelPrice(ctx context.Context, in ModelPriceInput) (*ModelPriceView, error) {
	name := strings.TrimSpace(in.PlatformModelName)
	if name == "" {
		return nil, ErrPlatformModelRequired
	}
	mode := normalizePricingMode(in.PricingMode)
	var info *ModelInfo
	if s.ports.Models != nil {
		mi, ok, err := s.ports.Models.Model(ctx, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrInvalidModelPricing
		}
		info = &mi
		if mode == PricingDuration && !mi.SupportsVideo {
			return nil, ErrInvalidModelPricing
		}
	} else if mode == PricingDuration {
		return nil, ErrInvalidModelPricing
	}
	basis := strings.TrimSpace(in.CacheWritePriceBasis)
	if basis != "" && basis != "direct" && basis != "anthropic_5m" {
		return nil, ErrInvalidModelPricing
	}
	row := ModelPrice{PlatformModelName: name, Currency: CurrencyUSD, IsFree: in.IsFree != nil && *in.IsFree, PricingMode: mode,
		TieredPricingJSON: "{}", SchedulePricingJSON: "{}"}
	switch mode {
	case PricingCall:
		row.CallNanousdPerCall = USDToNano(deref(in.CallUSDPerCall))
	case PricingDuration:
		row.DurationNanousdPerSecond = USDToNano(deref(in.DurationUSDPerSecond))
	case PricingTiered:
		tc, err := parseTiersJSON(in.TieredPricingJSON)
		if err != nil {
			return nil, ErrInvalidModelPricing.WithCause(err)
		}
		row.TieredPricingJSON = marshalTiers(tc)
		row.CacheWritePriceBasis = basis
	default:
		row.InputNanousdPerMTokens = USDToNano(deref(in.InputUSDPerMTokens))
		row.CacheReadNanousdPerMTokens = USDToNano(deref(in.CacheReadUSDPerMTokens))
		row.CacheWriteNanousdPerMTokens = USDToNano(deref(in.CacheWriteUSDPerMTokens))
		row.OutputNanousdPerMTokens = USDToNano(deref(in.OutputUSDPerMTokens))
		row.CacheWritePriceBasis = basis
	}
	sc, err := parseScheduleJSON(in.SchedulePricingJSON)
	if err != nil {
		return nil, ErrInvalidModelPricing.WithCause(err)
	}
	row.SchedulePricingJSON = marshalSchedule(sc)

	var saved ModelPrice
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur ModelPrice
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("platform_model_name = ?", name).First(&cur).Error
		switch {
		case e == nil:
			row.ID, row.CreatedAt = cur.ID, cur.CreatedAt
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		case database.IsNotFound(e):
			if err := database.Translate(tx.Create(&row).Error); err != nil {
				return err
			}
		default:
			return e
		}
		saved = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = s.cache.Delete(ctx, publicPriceCacheKey)
	v := modelPriceView(saved, info)
	return &v, nil
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// PublicTier is a tier in the public price view.
type PublicTier struct {
	FromTokens              int64   `json:"fromTokens"`
	UpToTokens              *int64  `json:"upToTokens"`
	InputUSDPerMTokens      float64 `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens  float64 `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens float64 `json:"cacheWriteUSDPerMTokens"`
	OutputUSDPerMTokens     float64 `json:"outputUSDPerMTokens"`
}

// PublicPricing is the pricing object attached to public model list entries.
type PublicPricing struct {
	Currency                 string           `json:"currency"`
	IsFree                   bool             `json:"isFree"`
	Mode                     string           `json:"mode"`
	InputUSDPerMTokens       float64          `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens   float64          `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens  float64          `json:"cacheWriteUSDPerMTokens"`
	CacheWrite5mMultiplier   float64          `json:"cacheWrite5mMultiplier"`
	CacheWrite1hMultiplier   float64          `json:"cacheWrite1hMultiplier"`
	OutputUSDPerMTokens      float64          `json:"outputUSDPerMTokens"`
	CallUSDPerCall           float64          `json:"callUSDPerCall"`
	DurationUSDPerSecond     float64          `json:"durationUSDPerSecond"`
	Tiers                    []PublicTier     `json:"tiers"`
	SchedulePeriods          []SchedulePeriod `json:"schedulePeriods"`
	ScheduleUTCOffsetMinutes int              `json:"scheduleUTCOffsetMinutes"`
}

func (s *Service) publicPricingOf(row ModelPrice, offsetMin int) PublicPricing {
	m5, m1 := CacheWriteMultipliers(row.CacheWritePriceBasis)
	p := PublicPricing{Currency: firstNonEmpty(row.Currency, CurrencyUSD), IsFree: row.IsFree, Mode: normalizePricingMode(row.PricingMode),
		InputUSDPerMTokens: NanoToUSD(row.InputNanousdPerMTokens), CacheReadUSDPerMTokens: NanoToUSD(row.CacheReadNanousdPerMTokens),
		CacheWriteUSDPerMTokens: NanoToUSD(row.CacheWriteNanousdPerMTokens), CacheWrite5mMultiplier: m5, CacheWrite1hMultiplier: m1,
		OutputUSDPerMTokens: NanoToUSD(row.OutputNanousdPerMTokens), CallUSDPerCall: NanoToUSD(row.CallNanousdPerCall),
		DurationUSDPerSecond: NanoToUSD(row.DurationNanousdPerSecond), Tiers: []PublicTier{}, SchedulePeriods: []SchedulePeriod{},
		ScheduleUTCOffsetMinutes: offsetMin}
	if p.Mode == PricingTiered {
		if tc, err := parseTiersJSON(row.TieredPricingJSON); err == nil {
			for _, t := range tc.Tiers {
				p.Tiers = append(p.Tiers, PublicTier(t))
			}
		}
	}
	if sc, err := parseScheduleJSON(row.SchedulePricingJSON); err == nil && sc != nil {
		p.SchedulePeriods = sc.Periods
	}
	return p
}

// PublicPricing returns the public pricing of the named models. It is empty in
// self mode (pricing is not shown) and omits models without a price row; the
// caller hides those models in non-self modes. Results may be up to 30 seconds
// stale and are invalidated by every price upsert.
func (s *Service) PublicPricing(ctx context.Context) (mode string, prices map[string]PublicPricing, err error) {
	mode = s.currentMode(ctx)
	prices = map[string]PublicPricing{}
	if mode == ModeSelf {
		return mode, prices, nil
	}
	_, offset := s.now().In(s.loc).Zone()
	if cached, ok, _ := cache.GetJSON[map[string]PublicPricing](ctx, s.cache, publicPriceCacheKey); ok {
		return mode, cached, nil
	}
	var rows []ModelPrice
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return mode, nil, err
	}
	for _, r := range rows {
		prices[r.PlatformModelName] = s.publicPricingOf(r, offset/60)
	}
	_ = cache.SetJSON(ctx, s.cache, publicPriceCacheKey, prices, publicPriceCacheTTL)
	return mode, prices, nil
}

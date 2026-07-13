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

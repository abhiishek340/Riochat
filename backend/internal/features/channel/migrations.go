package channel

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// builtinVendors are the vendors seeded at startup.
var builtinVendors = []struct {
	Key, Name, Icon string
	Order           int
}{
	{"openai", "OpenAI", "openai", 100}, {"anthropic", "Anthropic", "anthropic", 200}, {"google", "Google", "google", 300},
	{"meta", "Meta", "meta", 400}, {"microsoft", "Microsoft", "microsoft", 500}, {"amazon", "Amazon", "aws", 600},
	{"nvidia", "NVIDIA", "nvidia", 700}, {"deepseek", "DeepSeek", "deepseek", 800}, {"moonshot", "MoonShot", "moonshot", 900},
	{"zhipu", "ZhiPu", "zhipu", 1000}, {"minimax", "MiniMax", "minimax", 1100}, {"bytedance", "ByteDance", "bytedance", 1200},
	{"tencent", "Tencent", "tencent", 1300}, {"longcat", "LongCat", "longcat", 1400}, {"mistral", "Mistral", "mistral", 1500},
	{"alibaba", "Alibaba", "alibaba", 1600}, {"xai", "xAI", "xai", 1700}, {"xiaomi", "Xiaomi", "xiaomimimo", 1800},
	{"iflytek", "iFlytek", "iflytekcloud", 1900}, {"stepfun", "StepFun", "stepfun", 2000}, {"baichuan", "Baichuan", "baichuan", 2100},
	{"baidu", "Baidu", "baidu", 2200}, {"openrouter", "OpenRouter", "openrouter", 2300}, {"copilot", "GitHub Copilot", "copilot", 2400},
	{"unknown", "Unknown", "", 2500},
}

// Migrations returns the channel area's idempotent schema and seed steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables",
			&Upstream{}, &PlatformModel{}, &ModelVendor{}, &ModelDisplayGroup{}, &IconAsset{},
			&UpstreamModel{}, &ModelRoute{},
			&PermissionGroup{}, &PermissionGroupModelAccess{}, &PermissionGroupModelRule{}, &PermissionGroupUserAccess{},
		),
		partialIconIndex("idx_llm_platform_models_icon_asset", "llm_platform_models"),
		partialIconIndex("idx_llm_model_vendors_icon_asset", "llm_model_vendors"),
		partialIconIndex("idx_llm_model_display_groups_icon_asset", "llm_model_display_groups"),
		migrate.Seed("vendors", seedVendors),
		migrate.Seed("settings", seedSettings),
	}
}

// partialIconIndex indexes only the rows whose icon is a managed asset
// reference, which is what reference counting scans.
func partialIconIndex(name, table string) migrate.Step {
	stmt := "CREATE INDEX IF NOT EXISTS " + name + " ON " + table + " (icon) WHERE icon LIKE 'asset:%'"
	return migrate.Exec(name, migrate.PhaseSchema, map[string]string{"sqlite": stmt, "postgres": stmt})
}

func seedVendors(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	for _, v := range builtinVendors {
		var existing ModelVendor
		err := db.Where("\"key\" = ?", v.Key).First(&existing).Error
		switch {
		case err == nil:
			if !existing.BuiltIn {
				if err := db.Model(&ModelVendor{}).Where("id = ?", existing.ID).Update("built_in", true).Error; err != nil {
					return err
				}
			}
		case database.IsNotFound(err):
			row := ModelVendor{Key: v.Key, Name: v.Name, Icon: v.Icon, BuiltIn: true, SortOrder: v.Order}
			if err := db.Create(&row).Error; err != nil && !database.IsDuplicate(err) {
				return err
			}
		default:
			return err
		}
	}
	// Vendors referenced by models but missing from the catalog get a row.
	var used []string
	if err := db.Model(&PlatformModel{}).Where("vendor <> ''").Distinct().Pluck("vendor", &used).Error; err != nil {
		return err
	}
	for _, key := range used {
		var n int64
		if err := db.Model(&ModelVendor{}).Where("\"key\" = ?", key).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			row := ModelVendor{Key: key, Name: key, Icon: ""}
			if err := db.Create(&row).Error; err != nil && !database.IsDuplicate(err) {
				return err
			}
		}
	}
	return nil
}

// llm settings seeded when missing (never overwritten).
var llmSettingSeeds = []struct{ Key, Value, Description string }{
	{"circuit_breaker.error_classification", `{"circuit_errors":["5xx","timeout","connection_error"],"rate_limit_errors":["429"],"ignore_errors":["4xx"]}`, "Error type classification for circuit breaking"},
	{"circuit_breaker.defaults", defaultBreakerJSON, "Default circuit breaker settings"},
	{"rate_limit.defaults", `{"backoff_base_sec":5,"backoff_max_sec":60,"backoff_multiplier":2}`, "Default wait settings for retries after rate limiting"},
	{"load_balance.defaults", `{"algorithm":"weighted_random"}`, "Default load balancing settings"},
}

const settingsNamespace = "llm"

func seedSettings(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	// The settings area owns the table; without it there is nothing to seed
	// and readers fall back to built-in defaults.
	if !db.Migrator().HasTable(&settingRow{}) {
		return nil
	}
	for _, s := range llmSettingSeeds {
		var n int64
		if err := db.Model(&settingRow{}).Where("namespace = ? AND \"key\" = ?", settingsNamespace, s.Key).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		row := settingRow{Namespace: settingsNamespace, Key: s.Key, Value: s.Value, ValueType: "json", Description: s.Description}
		if err := db.Create(&row).Error; err != nil && !database.IsDuplicate(err) {
			return err
		}
	}
	return nil
}

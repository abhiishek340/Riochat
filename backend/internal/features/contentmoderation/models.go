package contentmoderation

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Event is the record of one moderation check outcome that is worth keeping: a
// hit, a failed-open check, or a pass.
type Event struct {
	database.Model
	PublicID        string `gorm:"column:public_id;size:40;not null;uniqueIndex"`
	UserID          uint   `gorm:"column:user_id;not null;default:0"`
	ConversationID  uint   `gorm:"column:conversation_id;not null;default:0;index"`
	RunID           string `gorm:"column:run_id;size:64;not null;default:'';index"`
	MessageID       uint   `gorm:"column:message_id;not null;default:0;index"`
	MessagePublicID string `gorm:"column:message_public_id;size:32;not null;default:'';index"`

	Direction     string `gorm:"column:direction;size:16;not null;default:'';index"`
	Modality      string `gorm:"column:modality;size:16;not null;default:'';index"`
	ModelName     string `gorm:"column:model;size:128;not null;default:'';index"`
	PolicyVersion int64  `gorm:"column:policy_version;not null;default:0"`
	Result        string `gorm:"column:result;size:32;not null;default:'';index"`

	CategoriesJSON     string `gorm:"column:categories_json;type:text;not null;default:'[]'"`
	CategoryScoresJSON string `gorm:"column:category_scores_json;type:text;not null;default:'{}'"`

	LatencyMS    int64  `gorm:"column:latency_ms;not null;default:0"`
	ErrorCode    string `gorm:"column:error_code;size:64;not null;default:'';index"`
	ErrorMessage string `gorm:"column:error_message;size:255;not null;default:''"`

	ContentLocationJSON string `gorm:"column:content_location_json;type:text;not null;default:'{}'"`
	ContentSummary      string `gorm:"column:content_summary;size:255;not null;default:'';index"`
	EncryptedText       string `gorm:"column:encrypted_text;type:text;not null;default:''"`
	ImageCount          int    `gorm:"column:image_count;not null;default:0"`
	ImageMetaJSON       string `gorm:"column:image_meta_json;type:text;not null;default:'[]'"`

	ContentExpiresAt  time.Time `gorm:"column:content_expires_at;not null;index"`
	MetadataExpiresAt time.Time `gorm:"column:metadata_expires_at;not null;index"`
}

// TableName implements database.Tabler.
func (Event) TableName() string { return "content_moderation_events" }

// DailyStat is one anonymous aggregate row. An empty Category marks the
// aggregate row of a (direction, modality, result) combination.
type DailyStat struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	StatDate  time.Time `gorm:"column:stat_date;type:date;not null;uniqueIndex:uidx_content_moderation_daily_stats_key,priority:1"`
	Direction string    `gorm:"column:direction;size:16;not null;default:'';uniqueIndex:uidx_content_moderation_daily_stats_key,priority:2"`
	Modality  string    `gorm:"column:modality;size:16;not null;default:'';uniqueIndex:uidx_content_moderation_daily_stats_key,priority:3"`
	Result    string    `gorm:"column:result;size:32;not null;default:'';uniqueIndex:uidx_content_moderation_daily_stats_key,priority:4"`
	Category  string    `gorm:"column:category;size:64;not null;default:'';uniqueIndex:uidx_content_moderation_daily_stats_key,priority:5"`

	CheckCount   int64 `gorm:"column:check_count;not null;default:0"`
	ContentItems int64 `gorm:"column:content_items;not null;default:0"`
	HitCount     int64 `gorm:"column:hit_count;not null;default:0"`
	FailureCount int64 `gorm:"column:failure_count;not null;default:0"`
	LatencySumMS int64 `gorm:"column:latency_sum_ms;not null;default:0"`
	LatencyCount int64 `gorm:"column:latency_count;not null;default:0"`
}

// TableName implements database.Tabler.
func (DailyStat) TableName() string { return "content_moderation_daily_stats" }

// settingRow maps the shared system_settings table. The settings area owns the
// table; this package reads and writes only its own namespace, and declares the
// model so the table exists when this area runs alone.
type settingRow struct {
	ID          uint      `gorm:"primaryKey"`
	Namespace   string    `gorm:"column:namespace;size:32;not null;uniqueIndex:ux_system_settings_ns_key"`
	Key         string    `gorm:"column:key;size:128;not null;uniqueIndex:ux_system_settings_ns_key"`
	Value       string    `gorm:"column:value;type:text;not null"`
	ValueType   string    `gorm:"column:value_type;size:16;not null"`
	Description string    `gorm:"column:description;size:255;not null;default:''"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// TableName implements database.Tabler.
func (settingRow) TableName() string { return "system_settings" }

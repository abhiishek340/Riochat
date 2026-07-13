package skill

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Scope values of a skill.
const (
	ScopeBuiltin = "builtin"
	ScopeUser    = "user"
)

// Field limits, counted in characters.
const (
	maxTitle       = 64
	maxTrigger     = 64
	maxDescription = 256
	maxMarkdown    = 10000
)

// Skill is a reusable prompt package. Rows are hard deleted.
type Skill struct {
	database.HardModel
	Scope           string `gorm:"column:scope;size:32;not null;default:user;index;uniqueIndex:uq_skills_scope_owner_trigger,priority:1"`
	OwnerUserID     uint   `gorm:"column:owner_user_id;not null;default:0;index;uniqueIndex:uq_skills_scope_owner_trigger,priority:2"`
	Title           string `gorm:"column:title;size:64;not null;default:''"`
	Trigger         string `gorm:"column:trigger;size:64;not null;default:'';index;uniqueIndex:uq_skills_scope_owner_trigger,priority:3"`
	Description     string `gorm:"column:description;size:256;not null;default:''"`
	Markdown        string `gorm:"column:markdown;type:text;not null;default:''"`
	Enabled         bool   `gorm:"column:enabled;not null;default:true;index"`
	SortOrder       int    `gorm:"column:sort_order;not null;default:0;index"`
	CreatedByUserID uint   `gorm:"column:created_by_user_id;not null;default:0;index"`
	UpdatedByUserID uint   `gorm:"column:updated_by_user_id;not null;default:0;index"`
}

// TableName is the skills table.
func (Skill) TableName() string { return "skills" }

// ProjectLink is a row of the project default skills join table. The
// conversation area reads and writes the same table; this model lets the skill
// area clean it up when skills or users go away.
type ProjectLink struct {
	ProjectID uint `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	SkillID   uint `gorm:"column:skill_id;primaryKey;autoIncrement:false;index"`
	SortOrder int  `gorm:"column:sort_order;not null;default:0"`
}

// TableName is the join table of project default skills.
func (ProjectLink) TableName() string { return "chat_conversation_project_skills" }

// Full is the complete wire form of a skill, markdown included.
type Full struct {
	ID              uint      `json:"id"`
	Scope           string    `json:"scope"`
	Title           string    `json:"title"`
	Trigger         string    `json:"trigger"`
	Description     string    `json:"description"`
	Markdown        string    `json:"markdown"`
	Enabled         bool      `json:"enabled"`
	SortOrder       int       `json:"sortOrder"`
	CreatedByUserID uint      `json:"createdByUserID"`
	UpdatedByUserID uint      `json:"updatedByUserID"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Summary is the discovery form of a skill: no markdown body.
type Summary struct {
	ID          uint      `json:"id"`
	Scope       string    `json:"scope"`
	Title       string    `json:"title"`
	Trigger     string    `json:"trigger"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	SortOrder   int       `json:"sortOrder"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (s Skill) full() Full {
	return Full{
		ID: s.ID, Scope: s.Scope, Title: s.Title, Trigger: s.Trigger, Description: s.Description,
		Markdown: s.Markdown, Enabled: s.Enabled, SortOrder: s.SortOrder,
		CreatedByUserID: s.CreatedByUserID, UpdatedByUserID: s.UpdatedByUserID,
		CreatedAt: s.CreatedAt.Local(), UpdatedAt: s.UpdatedAt.Local(),
	}
}

func (s Skill) summary() Summary {
	return Summary{
		ID: s.ID, Scope: s.Scope, Title: s.Title, Trigger: s.Trigger, Description: s.Description,
		Enabled: s.Enabled, SortOrder: s.SortOrder, CreatedAt: s.CreatedAt.Local(), UpdatedAt: s.UpdatedAt.Local(),
	}
}

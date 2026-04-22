package knowledgebase

import "github.com/riochat/riochat/backend/internal/platform/migrate"

// Migrations creates the area's two tables. No foreign keys are declared; the
// relations to file objects and projects are enforced in code.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &KnowledgeBase{}, &KnowledgeBaseFile{}),
	}
}

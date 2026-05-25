package ragindex

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations creates the chunk, vector and queue tables and applies the
// migration step for indexes without a vector-space signature.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &Chunk{}, &ChunkVector{}, &Job{}),
		migrate.Seed("unsigned_chunks_stale", func(ctx context.Context, db *gorm.DB) error {
			return markUnsignedStale(ctx, db)
		}),
	}
}

// markUnsignedStale sets every ready file that owns a chunk with an empty
// signature to stale. It does nothing when the file table does not exist yet.
func markUnsignedStale(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable(&File{}) {
		return nil
	}
	sub := db.Model(&Chunk{}).Select("file_obj_id").Where("embedding_signature = ''")
	return db.WithContext(ctx).Model(&File{}).
		Where("embed_status = ? AND id IN (?)", StateReady, sub).
		UpdateColumns(map[string]any{"embed_status": StateStale, "embed_error": errReindexRequired}).Error
}

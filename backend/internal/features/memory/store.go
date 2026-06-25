package memory

import (
	"context"
	"encoding/binary"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// store is the persistence layer. Every method is scoped by user id.
type store struct{ db *gorm.DB }

func (s *store) list(ctx context.Context, userID uint) ([]Entry, error) {
	var rows []Memory
	err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC, id DESC").
		Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = toEntry(r)
	}
	return out, nil
}

// upsert creates or updates the entry for (userID, key). A create beyond the
// per-user cap fails with ErrLimitExceeded; an update never does. A changed
// entry loses its vector in the same transaction.
func (s *store) upsert(ctx context.Context, now time.Time, userID uint, key, value, scope, updatedBy string) (Entry, error) {
	var out Entry
	// A concurrent create of the same key can win the unique index between our
	// lookup and insert; one retry then takes the update path.
	for attempt := 0; ; attempt++ {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var cur Memory
			err := tx.Where("user_id = ? AND memory_key = ?", userID, key).Take(&cur).Error
			switch {
			case err == nil:
				changes := map[string]any{
					"value": value, "scope": scope, "updated_by": updatedBy,
					"updated_at": now, "embedding_signature": "",
				}
				if err := tx.Model(&Memory{}).Where("id = ?", cur.ID).Updates(changes).Error; err != nil {
					return err
				}
				if err := tx.Where("memory_id = ?", cur.ID).Delete(&vectorRow{}).Error; err != nil {
					return err
				}
				cur.Value, cur.Scope, cur.UpdatedBy, cur.UpdatedAt = value, scope, updatedBy, now
				out = toEntry(cur)
				return nil
			case database.IsNotFound(err):
				var n int64
				if err := tx.Model(&Memory{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
					return err
				}
				if n >= MaxEntriesPerUser {
					return ErrLimitExceeded
				}
				row := Memory{
					Model:  database.Model{CreatedAt: now, UpdatedAt: now},
					UserID: userID, MemoryKey: key, Value: value, Scope: scope, UpdatedBy: updatedBy,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				out = toEntry(row)
				return nil
			default:
				return err
			}
		})
		if err == nil {
			return out, nil
		}
		if attempt == 0 && database.IsDuplicate(err) {
			continue
		}
		return Entry{}, translateKeep(err)
	}
}

// translateKeep maps driver errors but leaves this area's typed errors alone.
func translateKeep(err error) error {
	if err == ErrLimitExceeded {
		return err
	}
	return database.Translate(err)
}

// remove soft-deletes the entry and drops its vector. A missing entry is not an
// error.
func (s *store) remove(ctx context.Context, userID uint, key string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&Memory{}).Where("user_id = ? AND memory_key = ?", userID, key).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("memory_id IN ?", ids).Delete(&vectorRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&Memory{}).Error
	})
}

// purgeUser hard-deletes every row of the user, soft-deleted ones included,
// together with the vectors.
func (s *store) purgeUser(tx *gorm.DB, userID uint) error {
	if err := tx.Where("user_id = ?", userID).Delete(&vectorRow{}).Error; err != nil {
		return err
	}
	return tx.Unscoped().Where("user_id = ?", userID).Delete(&Memory{}).Error
}

// setVector stores the vector when the live entry still matches. The match is
// on user and key and, when expectedValue is non-empty, on the exact current
// value; otherwise the vector is stale and silently dropped.
func (s *store) setVector(ctx context.Context, userID uint, key, expectedValue string, vec []float32, signature string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("user_id = ? AND memory_key = ?", userID, key)
		if expectedValue != "" {
			q = q.Where("value = ?", expectedValue)
		}
		var cur Memory
		if err := q.Take(&cur).Error; err != nil {
			if database.IsNotFound(err) {
				return nil
			}
			return err
		}
		if err := tx.Model(&Memory{}).Where("id = ?", cur.ID).UpdateColumn("embedding_signature", signature).Error; err != nil {
			return err
		}
		if err := tx.Where("memory_id = ?", cur.ID).Delete(&vectorRow{}).Error; err != nil {
			return err
		}
		return tx.Create(&vectorRow{MemoryID: cur.ID, UserID: userID, Signature: signature, Embedding: packVector(vec)}).Error
	})
}

type scoredRow struct {
	Memory
	Embedding []byte `gorm:"column:vec_embedding"`
}

// search ranks the user's embedded entries by exact cosine similarity.
func (s *store) search(ctx context.Context, userID uint, query []float32, signature string, topK int, minSim float64) ([]Entry, error) {
	var rows []scoredRow
	err := s.db.WithContext(ctx).
		Table("user_memories AS m").
		Select("m.*, v.embedding AS vec_embedding").
		Joins("JOIN user_memory_embeddings AS v ON v.memory_id = m.id").
		Where("m.user_id = ? AND m.deleted_at IS NULL AND m.embedding_signature = ?", userID, signature).
		Where("v.user_id = ? AND v.embedding_signature = ?", userID, signature).
		Scan(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	type scored struct {
		e   Entry
		sim float64
	}
	var hits []scored
	for _, r := range rows {
		vec, ok := unpackVector(r.Embedding)
		if !ok {
			continue
		}
		sim, ok := cosine(query, vec)
		if !ok || sim < minSim {
			continue
		}
		hits = append(hits, scored{e: Entry{
			ID: r.ID, UserID: r.UserID, MemoryKey: r.MemoryKey, Value: r.Value,
			Scope: r.Scope, UpdatedBy: r.UpdatedBy,
		}, sim: sim})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].sim != hits[j].sim {
			return hits[i].sim > hits[j].sim
		}
		return hits[i].e.ID < hits[j].e.ID
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}
	out := make([]Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out, nil
}

func packVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func unpackVector(b []byte) ([]float32, bool) {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil, false
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, true
}

// cosine returns the cosine similarity of a and b. ok is false when the
// dimensions differ or either vector has no length.
func cosine(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}

func finiteVector(v []float32) bool {
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return false
		}
	}
	return true
}

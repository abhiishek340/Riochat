// Package migrate runs schema and data migrations.
//
// There is no external migration tool and no version table. Every step is
// idempotent and the whole set runs on every start, so the same code upgrades
// an old database, initializes a new one, and tolerates two instances starting
// at once (a cross-process lock serializes them on PostgreSQL).
//
// Each feature area owns a Set of Steps in its own migrations.go. The runner
// executes all Schema steps of all areas (in registry order) before any Seed
// step, so a seed may rely on tables owned by another area.
package migrate

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Phase orders steps across all areas.
type Phase int

const (
	// PhaseSchema creates and reconciles tables, columns and indexes.
	PhaseSchema Phase = iota
	// PhaseSeed inserts built-in rows and applies data fix-ups.
	PhaseSeed
)

func (p Phase) String() string {
	if p == PhaseSeed {
		return "seed"
	}
	return "schema"
}

// Step is one idempotent unit of work.
type Step struct {
	// Name identifies the step in logs and errors; unique within its Set.
	Name string
	// Phase defaults to PhaseSchema.
	Phase Phase
	// Up performs the work. It must be safe to run repeatedly.
	Up func(ctx context.Context, db *gorm.DB) error
}

// Set is the ordered list of steps owned by one feature area.
type Set struct {
	Area  string
	Steps []Step
}

// LockName is the cross-process lock held while migrating.
const LockName = "riochat_migrations_v1"

// Run executes every set: all schema steps first, then all seed steps. It stops
// at the first failure. Areas and steps run in the order given.
func Run(ctx context.Context, db *gorm.DB, log *logx.Logger, sets ...Set) error {
	if log == nil {
		log = logx.Nop()
	}
	if err := validate(sets); err != nil {
		return err
	}
	return database.WithAdvisoryLock(ctx, db, LockName, func(tx *gorm.DB) error {
		for _, phase := range []Phase{PhaseSchema, PhaseSeed} {
			for _, set := range sets {
				for _, step := range set.Steps {
					if step.Phase != phase {
						continue
					}
					log.Debug("migration_step", logx.F("area", set.Area), logx.F("step", step.Name), logx.F("phase", phase))
					if err := step.Up(ctx, tx); err != nil {
						return fmt.Errorf("migrate %s/%s (%s): %w", set.Area, step.Name, phase, err)
					}
				}
			}
		}
		return nil
	})
}

func validate(sets []Set) error {
	areas := map[string]bool{}
	for _, set := range sets {
		if set.Area == "" {
			return errors.New("migrate: set without an area name")
		}
		if areas[set.Area] {
			return fmt.Errorf("migrate: duplicate area %q", set.Area)
		}
		areas[set.Area] = true
		names := map[string]bool{}
		for _, s := range set.Steps {
			if s.Name == "" || s.Up == nil {
				return fmt.Errorf("migrate: area %q has a step without a name or Up function", set.Area)
			}
			if names[s.Name] {
				return fmt.Errorf("migrate: area %q has duplicate step %q", set.Area, s.Name)
			}
			names[s.Name] = true
		}
	}
	return nil
}

// AutoMigrate returns a schema step that creates or reconciles the tables for
// models. Every model must declare its table name (TableName) with a permitted
// domain prefix; violations fail the step so a misnamed table is caught the
// first time the area runs.
func AutoMigrate(name string, models ...any) Step {
	return Step{
		Name:  name,
		Phase: PhaseSchema,
		Up: func(ctx context.Context, db *gorm.DB) error {
			for _, m := range models {
				t, ok := m.(database.Tabler)
				if !ok {
					return fmt.Errorf("model %T must declare its table name with TableName()", m)
				}
				if err := database.CheckTableName(t.TableName()); err != nil {
					return fmt.Errorf("model %T: %w", m, err)
				}
			}
			return db.WithContext(ctx).AutoMigrate(models...)
		},
	}
}

// Exec returns a step that runs one statement per dialect ("sqlite" and/or
// "postgres"). A dialect without a statement is skipped. The statement must be
// idempotent (CREATE INDEX IF NOT EXISTS, ...).
func Exec(name string, phase Phase, byDialect map[string]string) Step {
	return Step{
		Name:  name,
		Phase: phase,
		Up: func(ctx context.Context, db *gorm.DB) error {
			stmt, ok := byDialect[database.Dialect(db)]
			if !ok || stmt == "" {
				return nil
			}
			return db.WithContext(ctx).Exec(stmt).Error
		},
	}
}

// Seed returns a seed-phase step from fn.
func Seed(name string, fn func(ctx context.Context, db *gorm.DB) error) Step {
	return Step{Name: name, Phase: PhaseSeed, Up: fn}
}

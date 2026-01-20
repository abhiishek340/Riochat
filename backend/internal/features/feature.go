// Package features is the module registration contract.
//
// Every feature area is a package at internal/features/<area> that exports two
// functions:
//
//	func Migrations() []migrate.Step   // its tables, indexes and seeds (may be nil)
//	func Mount(d deps.Deps) error      // its routes, hooks and workers
//
// The integrator lists each area once in internal/registry/registry.go. Nothing else in this
// package changes when an area is added.
package features

import (
	"fmt"
	"regexp"
	"runtime/debug"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Feature is one registered area.
type Feature struct {
	// Name is the area's identifier: lower case letters, digits and underscores,
	// unique across the registry. By convention it equals the package directory.
	Name string
	// Migrations returns the area's idempotent migration steps. May be nil.
	Migrations func() []migrate.Step
	// Mount registers the area's routes and wires its hooks and workers.
	Mount func(d deps.Deps) error
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate checks names and callbacks across a feature list.
func Validate(list []Feature) error {
	seen := map[string]bool{}
	for _, f := range list {
		if !nameRe.MatchString(f.Name) {
			return fmt.Errorf("features: invalid area name %q (use lower case letters, digits and underscores)", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("features: duplicate area %q", f.Name)
		}
		seen[f.Name] = true
		if f.Mount == nil {
			return fmt.Errorf("features: area %q has no Mount function", f.Name)
		}
	}
	return nil
}

// MigrationSets collects each area's steps in registry order.
func MigrationSets(list []Feature) []migrate.Set {
	sets := make([]migrate.Set, 0, len(list))
	for _, f := range list {
		var steps []migrate.Step
		if f.Migrations != nil {
			steps = f.Migrations()
		}
		if len(steps) == 0 {
			continue
		}
		sets = append(sets, migrate.Set{Area: f.Name, Steps: steps})
	}
	return sets
}

// MountAll mounts every area in registry order, each with a Deps whose logger
// carries the area name. A panic inside Mount (for example a route conflict)
// is reported as an error naming the area.
func MountAll(base deps.Deps, list []Feature) error {
	for _, f := range list {
		if err := mountOne(base.ForArea(f.Name), f); err != nil {
			return err
		}
	}
	return nil
}

func mountOne(d deps.Deps, f Feature) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("features: mounting %q panicked: %v\n%s", f.Name, r, debug.Stack())
		}
	}()
	if err := f.Mount(d); err != nil {
		return fmt.Errorf("features: mounting %q: %w", f.Name, err)
	}
	return nil
}

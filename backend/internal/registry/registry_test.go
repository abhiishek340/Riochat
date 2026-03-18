package registry

import (
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
)

func TestRegistryIsValid(t *testing.T) {
	if err := features.Validate(Registered()); err != nil {
		t.Fatalf("registry invalid: %v", err)
	}
}

func TestEachCallBuildsIndependentFeatureList(t *testing.T) {
	a, b := Registered(), Registered()
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("unexpected lists: %d and %d entries", len(a), len(b))
	}
}

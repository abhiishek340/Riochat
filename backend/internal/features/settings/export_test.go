package settings

import "context"

// Test-only access to internals for the black-box tests in this directory.

// Seed runs the start-up seeding again.
func (s *Service) Seed(ctx context.Context) error { return s.seed(ctx) }

// DefaultAllowedDoc returns a fresh copy of the built-in allow document.
func DefaultAllowedDoc() map[string][]string { return builtinAllowedDoc() }

// MarshalDoc renders a document the way the settings store keeps it.
func MarshalDoc(d map[string][]string) string { return marshalDoc(d) }

// DefaultMimeTypes is the current default upload type list.
func DefaultMimeTypes() string { return defaultMimeTypes }

// HistoricalMimeLists returns the earlier default lists as comma separated text.
func HistoricalMimeLists() []string {
	var out []string
	for _, set := range historicalMimeSets() {
		var parts []string
		for t := range set {
			parts = append(parts, t)
		}
		out = append(out, joinSorted(parts))
	}
	return out
}

func joinSorted(parts []string) string {
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j] < parts[j-1]; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += ","
		}
		s += p
	}
	return s
}

// RegistryCount is the number of registered settings.
func RegistryCount() int { return len(reg.list) }

// RegistryDefault returns the registry default for a key.
func RegistryDefault(ns, key string) string {
	d := reg.lookup(ns, key)
	if d == nil {
		panic("unknown setting " + ns + "/" + key)
	}
	return d.def
}

// RegistryTypes returns the value type of every registered key.
func RegistryTypes() map[string]string {
	out := map[string]string{}
	for _, d := range reg.list {
		out[d.ns+"/"+d.key] = string(d.typ)
	}
	return out
}

// Backfill runs the start-up signature backfill.
func (s *Service) Backfill(ctx context.Context) { s.backfillSignature(ctx) }

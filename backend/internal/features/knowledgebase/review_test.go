package knowledgebase

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// Storage conditions that reach the HTTP layer untranslated still map to the
// documented statuses, while codes owned by collaborators are never rewritten.
func TestMapStorage(t *testing.T) {
	if got := mapStorage(gorm.ErrRecordNotFound); !errors.Is(got, ErrNotFound) {
		t.Fatalf("not found: %v", got)
	}
	if got := mapStorage(gorm.ErrDuplicatedKey); !errors.Is(got, ErrConflict) {
		t.Fatalf("duplicate: %v", got)
	}
	if got := mapStorage(gorm.ErrInvalidData); !errors.Is(got, ErrInvalid) {
		t.Fatalf("invalid input: %v", got)
	}
	// A collaborator's typed error keeps its own code even with a storage cause.
	wrapped := ErrFileNotFound.WithCause(gorm.ErrRecordNotFound)
	if got := mapStorage(wrapped); statusFor(got) != 404 || !errors.Is(got, ErrFileNotFound) {
		t.Fatalf("typed collaborator error rewritten: %v", got)
	}
	plain := errors.New("boom")
	if got := mapStorage(plain); got != plain {
		t.Fatalf("unknown errors pass through: %v", got)
	}
}

// An embedding queue failure that is a storage "not found" is answered through
// the area's mapping instead of the generic 500.
func TestEmbeddingStorageErrorUsesAreaMapping(t *testing.T) {
	te := newTestEnv(t)
	te.embed.err = gorm.ErrRecordNotFound
	w := te.Do("POST", api+"/admin/knowledge-bases/files/embeddings", te.admin(1), map[string]any{"fileIDs": []string{"a"}})
	testkit.Expect(t, w, 404, "knowledge_base.not_found")

	te.embed.err = errors.New("queue exploded")
	w = te.Do("POST", api+"/admin/knowledge-bases/files/embeddings", te.admin(1), map[string]any{"fileIDs": []string{"a"}})
	testkit.Expect(t, w, 500, "knowledge_base.internal")
}

// The 100-id ceiling of the status endpoints counts the ids as sent, so a
// request padded with duplicates past 100 is refused rather than trimmed.
func TestStatusIDCeilingCountsRawInput(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	raw := make([]string, 0, 101)
	for i := 0; i < 100; i++ {
		raw = append(raw, fmt.Sprintf("f%d", i))
	}
	raw = append(raw, "f0")
	if _, err := te.svc.statuses(t.Context(), personalOf(11), kb.PublicID, raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("statuses 101 raw ids: %v", err)
	}
	if _, err := te.svc.snapshot(t.Context(), personalOf(11), kb.PublicID, raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("snapshot 101 raw ids: %v", err)
	}
	if _, err := te.svc.statuses(t.Context(), personalOf(11), kb.PublicID, raw[:100]); err != nil {
		t.Fatalf("100 ids are fine: %v", err)
	}
}

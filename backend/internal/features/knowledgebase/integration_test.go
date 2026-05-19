package knowledgebase

import (
	"errors"
	"strconv"
	"testing"

	"gorm.io/gorm"
)

func refUnavailable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrReferenceUnavailable) {
		t.Fatalf("want reference unavailable, got %v", err)
	}
}

// Scenario 24.
func TestResolve(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	b1 := te.createBuiltin("B1", true)
	b2 := te.createBuiltin("B2", false)
	m := te.createMine(11, "M")
	o := te.createMine(12, "O")

	ready := te.addFile(0, "ready", ragReady)
	te.link(b1, ready, te.addFile(0, "pending"))
	te.link(m,
		te.addFile(11, "mine-ready", ragReady),
		te.addFile(11, "opted-out", ragReady, func(f *fileObject) { f.RagOptOut = true }),
		te.addFile(11, "no-chunks", ragReady, func(f *fileObject) { f.ChunkCount = 0 }),
		te.addFile(11, "inactive", ragReady, func(f *fileObject) { f.Status = "deleted" }),
		te.addFile(11, "embedding", func(f *fileObject) { f.EmbedStatus = "processing" }))
	soft := te.addFile(11, "soft-deleted", ragReady)
	te.link(m, soft)
	te.DB.Delete(&soft)
	// the same ready file linked into two requested bases appears once
	te.link(m, ready)

	res, err := te.svc.Resolve(ctx, 11, []string{b1.PublicID, m.PublicID})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bases) != 2 || res.Bases[0].PublicID != b1.PublicID || res.Bases[1].PublicID != m.PublicID {
		t.Fatalf("bases (request order expected): %+v", res.Bases)
	}
	if res.Bases[0].FileCount != 2 || res.Bases[0].ReadyFileCount != 1 {
		t.Fatalf("counts: %+v", res.Bases[0])
	}
	var ids []string
	for _, f := range res.Files {
		ids = append(ids, f.FileID)
	}
	if !equalStrings(ids, []string{"ready", "mine-ready"}) {
		t.Fatalf("files: %v", ids)
	}
	if got := res.Files[0]; len(got.KnowledgeBaseIDs) != 2 || got.OwnerUserID != 0 || got.FileObjectID == 0 || got.ChunkCount != 2 {
		t.Fatalf("shared file: %+v", got)
	}

	// request order is preserved
	res, err = te.svc.Resolve(ctx, 11, []string{m.PublicID, b1.PublicID})
	if err != nil || res.Bases[0].PublicID != m.PublicID {
		t.Fatalf("reordered: %v %+v", err, res)
	}

	_, err = te.svc.Resolve(ctx, 11, []string{b2.PublicID})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, []string{o.PublicID})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, []string{b1.PublicID, "ghost"})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, []string{b1.PublicID, b1.PublicID})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, []string{b1.PublicID, " " + b1.PublicID})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, []string{b1.PublicID, " "})
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 11, nil)
	refUnavailable(t, err)
	_, err = te.svc.Resolve(ctx, 0, []string{b1.PublicID})
	refUnavailable(t, err)
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, "id"+strconv.Itoa(i))
	}
	_, err = te.svc.Resolve(ctx, 11, nine)
	refUnavailable(t, err)

	// eight references are allowed
	var eight []string
	for i := 0; i < 8; i++ {
		eight = append(eight, te.createMine(11, "many"+strconv.Itoa(i)).PublicID)
	}
	if _, err := te.svc.Resolve(ctx, 11, eight); err != nil {
		t.Fatalf("8 ids: %v", err)
	}

	// a disabled base stops resolving the moment it is disabled
	te.Do("PATCH", api+"/admin/knowledge-bases/"+b1.PublicID, te.admin(1), map[string]any{"enabled": false})
	_, err = te.svc.Resolve(ctx, 11, []string{b1.PublicID})
	refUnavailable(t, err)
}

func TestResolveStorageProblemsPassThrough(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "M")
	te.DB.Exec("DROP TABLE knowledge_bases")
	_, err := te.svc.Resolve(t.Context(), 11, []string{kb.PublicID})
	if err == nil || errors.Is(err, ErrReferenceUnavailable) {
		t.Fatalf("storage failure must surface as is: %v", err)
	}
}

func TestValidateKnowledgeBasesReadiness(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	good := te.createMine(11, "Good")
	empty := te.createMine(11, "Empty")
	te.link(good, te.addFile(11, "g1", ragReady))

	if err := te.svc.ValidateKnowledgeBases(ctx, 11, []string{good.PublicID}); err != nil {
		t.Fatalf("ready base: %v", err)
	}
	if err := te.svc.ValidateKnowledgeBases(ctx, 11, []string{empty.PublicID}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("empty base: %v", err)
	}
	// one unready base among ready ones rejects the whole selection
	if err := te.svc.ValidateKnowledgeBases(ctx, 11, []string{good.PublicID, empty.PublicID}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("mixed: %v", err)
	}
	refUnavailable(t, te.svc.ValidateKnowledgeBases(ctx, 12, []string{good.PublicID}))
}

func TestFilterProjectBases(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	b1 := te.createBuiltin("B1", true)
	b2 := te.createBuiltin("B2", false)
	m := te.createMine(11, "M")
	o := te.createMine(12, "O")
	got, err := te.svc.FilterProjectBases(ctx, 11, []string{m.PublicID, o.PublicID, b2.PublicID, "ghost", b1.PublicID, m.PublicID})
	if err != nil || !equalStrings(got, []string{m.PublicID, b1.PublicID}) {
		t.Fatalf("filtered: %v %v", got, err)
	}
	if got, _ := te.svc.FilterProjectBases(ctx, 11, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}

// Section 9.2.
func TestFileInEnabledBuiltinBase(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	b := te.createBuiltin("B", true)
	f := te.addFile(0, "pool")
	mine := te.createMine(11, "M")
	g := te.addFile(11, "mine")
	te.link(b, f)
	te.link(mine, g)
	if ok, _ := te.svc.FileInEnabledBuiltinBase(ctx, f.ID); !ok {
		t.Fatal("pool file in an enabled builtin base is retrievable by everyone")
	}
	if ok, _ := te.svc.FileInEnabledBuiltinBase(ctx, g.ID); ok {
		t.Fatal("personal bases grant no shared access")
	}
	te.Do("PATCH", api+"/admin/knowledge-bases/"+b.PublicID, te.admin(1), map[string]any{"enabled": false})
	if ok, _ := te.svc.FileInEnabledBuiltinBase(ctx, f.ID); ok {
		t.Fatal("disabling the base removes the access")
	}
}

// Scenario 26.
func TestIsFileLinkedGuardsGenericDeletion(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	kb := te.createMine(11, "M")
	f := te.addFile(11, "f1")
	if linked, err := te.svc.IsFileLinked(ctx, f.ID); err != nil || linked {
		t.Fatalf("unlinked: %v %v", linked, err)
	}
	te.Do("POST", api+"/knowledge-bases/mine/"+kb.PublicID+"/files", te.user(11), map[string]any{"fileIDs": []string{"f1"}})
	if linked, _ := te.svc.IsFileLinked(ctx, f.ID); !linked {
		t.Fatal("linked file must be reported")
	}
	te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"/files/f1", te.user(11), nil)
	if linked, _ := te.svc.IsFileLinked(ctx, f.ID); linked {
		t.Fatal("after removing the link the file may be deleted")
	}
}

// Scenario 25.
func TestAccountDeletion(t *testing.T) {
	te := newTestEnv(t)
	ctx := t.Context()
	b := te.createBuiltin("Shared", true)
	mine := te.createMine(11, "Mine")
	other := te.createMine(12, "Other")
	owned := te.addFile(11, "owned")
	te.addFile(11, "second")
	foreign := te.addFile(12, "foreign")
	te.link(b, owned)
	te.link(mine, te.addFile(11, "in-mine"))
	te.link(other, foreign)
	te.DB.Exec("CREATE TABLE chat_conversation_project_knowledge_bases (project_id integer, knowledge_base_id varchar(64), sort_order integer, PRIMARY KEY (project_id, knowledge_base_id))")
	te.DB.Exec("INSERT INTO chat_conversation_project_knowledge_bases VALUES (1, ?, 1), (2, ?, 1)", mine.PublicID, other.PublicID)

	err := te.svc.CheckAccountDeletion(ctx, 11)
	if !errors.Is(err, ErrOwnerFileReference) {
		t.Fatalf("blocked deletion: %v", err)
	}
	if statusFor(err) != 409 {
		t.Fatalf("status: %d", statusFor(err))
	}
	if err := te.svc.CheckAccountDeletion(ctx, 12); err != nil {
		t.Fatalf("user 12 owns no builtin-linked file: %v", err)
	}
	// an inactive or soft-deleted owned file does not block
	te.DB.Model(&fileObject{}).Where("file_id = ?", "owned").Update("status", "deleted")
	if err := te.svc.CheckAccountDeletion(ctx, 11); err != nil {
		t.Fatalf("inactive file must not block: %v", err)
	}
	te.DB.Model(&fileObject{}).Where("file_id = ?", "owned").Update("status", "active")
	if err := te.svc.CheckAccountDeletion(ctx, 11); err == nil {
		t.Fatal("blocked again")
	}

	// unlink, then purge
	te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID+"/files/owned", te.admin(1), nil)
	if err := te.svc.CheckAccountDeletion(ctx, 11); err != nil {
		t.Fatalf("after unlinking: %v", err)
	}
	// a stale link of the user's own file in a builtin base is purged too
	te.link(b, te.addFile(11, "stale", func(f *fileObject) { f.Status = "deleted" }))
	if err := te.DB.Transaction(func(tx *gorm.DB) error { return te.svc.PurgeUserData(ctx, tx, 11) }); err != nil {
		t.Fatal(err)
	}
	if te.baseExists(mine) {
		t.Fatal("personal base must vanish")
	}
	if !te.baseExists(other) || !te.baseExists(b) {
		t.Fatal("other bases must stay")
	}
	var links, projects int64
	te.DB.Model(&KnowledgeBaseFile{}).Count(&links)
	te.DB.Table("chat_conversation_project_knowledge_bases").Count(&projects)
	if links != 1 {
		t.Fatalf("only the other user's link remains, got %d", links)
	}
	if projects != 1 {
		t.Fatalf("project links remaining: %d", projects)
	}
	var left fileObject
	if err := te.DB.Where("file_id = ?", "foreign").Take(&left).Error; err != nil {
		t.Fatal("file objects are not this area's to delete")
	}
	// purging a user without anything is a no-op
	if err := te.DB.Transaction(func(tx *gorm.DB) error { return te.svc.PurgeUserData(ctx, tx, 999) }); err != nil {
		t.Fatal(err)
	}
}

func TestEnabledFollowsSettings(t *testing.T) {
	te := newTestEnv(t)
	if !te.svc.Enabled(t.Context()) {
		t.Fatal("enabled by default")
	}
	te.settings.set(true)
	if te.svc.Enabled(t.Context()) {
		t.Fatal("switch off")
	}
}

func TestPublicIDsAreUniqueHex(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newPublicID()
		if len(id) != 32 || seen[id] {
			t.Fatalf("bad id %q", id)
		}
		for _, r := range id {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
				t.Fatalf("non-hex id %q", id)
			}
		}
		seen[id] = true
	}
}

func TestNormalizeHelpers(t *testing.T) {
	if got := normalizeIDs([]string{" a ", "", "a", "b", "  "}); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("normalizeIDs: %v", got)
	}
	if _, ok := normalizeIDFilter([]string{"", " "}); ok {
		t.Fatal("all blank is an invalid selection")
	}
	if ids, ok := normalizeIDFilter(nil); !ok || ids != nil {
		t.Fatal("no filter is valid")
	}
	if got := escapeLike(`50%_\x`); got != `50\%\_\\x` {
		t.Fatalf("escapeLike: %q", got)
	}
	for _, c := range []struct {
		in   string
		ok   bool
		want string
	}{{"  ok  ", true, "ok"}, {"   ", false, ""}, {"", false, ""}} {
		got, err := cleanName(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("cleanName(%q) = %q, %v", c.in, got, err)
		}
	}
	if parseLenientBool("TRUE") == nil || *parseLenientBool("TRUE") != true || parseLenientBool("zzz") != nil || parseLenientBool("") != nil {
		t.Fatal("parseLenientBool")
	}
	f := fileObject{ProcessingStatus: "ready", EmbedStatus: "processing"}
	if !f.isProcessing() || f.isRAGReady() {
		t.Fatal("predicates")
	}
}

func TestDefaultMountWorksWithoutCollaborators(t *testing.T) {
	// Mount (no ports) must run and degrade as documented.
	te := newTestEnv(t, func(s *setup) {
		s.noCleaner, s.noReader, s.noUploader, s.noEmbedder, s.noVectorizer = true, true, true, true, true
	})
	kb := te.createMine(11, "M")
	te.link(kb, te.addFile(11, "f1"))
	p := te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files", te.user(11))
	if p.Total != 1 || p.Results[0].CanVectorize {
		t.Fatalf("files: %+v", p)
	}
}

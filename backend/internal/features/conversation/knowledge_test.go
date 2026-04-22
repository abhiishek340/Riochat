package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// ----- fakes for the retrieval collaborators -----

type fakeKnowledge struct {
	mu   sync.Mutex
	res  KnowledgeResult
	err  error
	reqs []KnowledgeRequest
}

func (k *fakeKnowledge) Prepare(_ context.Context, req KnowledgeRequest) (KnowledgeResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reqs = append(k.reqs, req)
	if req.Searching != nil && k.res.Ran {
		req.Searching()
	}
	return k.res, k.err
}

func (k *fakeKnowledge) requests() []KnowledgeRequest {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]KnowledgeRequest(nil), k.reqs...)
}

type fakeIndexer struct {
	mu        sync.Mutex
	submitted [][]string
	textReady []uint
	forgot    []uint
	cloned    [][2]uint
	cloneErr  error
	vect      func(FileFacts) (bool, string)
	submitErr error
}

func (f *fakeIndexer) Submit(_ context.Context, _ uint, ids []string) (*EmbeddingSubmission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	f.submitted = append(f.submitted, ids)
	return &EmbeddingSubmission{SubmittedFileIDs: ids, Skipped: []EmbeddingSkip{}}, nil
}

func (f *fakeIndexer) Vectorization(_ context.Context, facts FileFacts) (bool, string) {
	if f.vect != nil {
		return f.vect(facts)
	}
	return true, ""
}

func (f *fakeIndexer) TextReady(_ context.Context, id uint) {
	f.mu.Lock()
	f.textReady = append(f.textReady, id)
	f.mu.Unlock()
}

func (f *fakeIndexer) Forget(_ context.Context, _ *gorm.DB, id uint) error {
	f.mu.Lock()
	f.forgot = append(f.forgot, id)
	f.mu.Unlock()
	return nil
}

func (f *fakeIndexer) Clone(_ context.Context, _ *gorm.DB, from, to uint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cloned = append(f.cloned, [2]uint{from, to})
	return f.cloneErr
}

func newKnowledgeEnv(t *testing.T) (*tenv, *fakeKnowledge, *fakeIndexer) {
	t.Helper()
	kn := &fakeKnowledge{}
	ix := &fakeIndexer{}
	e := newEnvPorts(t, nil, func(p *Ports, _ *tenv) {
		p.Knowledge = kn
		p.Indexer = ix
	})
	e.set.mutate(func(r *RuntimeSettings) { r.RAGAvailable = true; r.RAGAvailabilityReason = "" })
	return e, kn, ix
}

func fileIDOf(m map[string]any) string { return str(m["file"].(map[string]any), "fileID") }

// ----- the turn -----

func TestRetrievalShapesTheTurnAndStoresEvidence(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	e.set.mutate(func(rs *RuntimeSettings) { rs.ContextArtifactRetentionDays = 90 })
	fid := fileIDOf(e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("the whole file text")))
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.res = KnowledgeResult{
		Ran: true, Context: "<retrieval>passages</retrieval>", Retrieved: map[string]bool{fid: true}, Inline: map[string]bool{},
		Sources: []KnowledgeSource{{FileName: "notes.txt", FileID: fid, ChunkIndex: 3, Score: 0.8, Preview: "the whole"}},
		Artifacts: []ContextArtifactInput{{
			Kind: "file_rag_chunk", SourceType: "file_chunk", SourceID: fid + ":3", SourceTitle: "notes.txt",
			Content: "the whole file text", TokenEstimate: 5, Score: 0.8, MetadataJSON: `{"query":"q"}`,
		}},
		Trace: &KnowledgeTrace{Title: "Knowledge retrieval", Summary: "retrieved 1 relevant passages", Detail: "d", Status: "completed", PayloadJSON: `{"hitCount":1}`},
	}
	m := e.send(e.u1, conv, "what is in it?", map[string]any{"fileIDs": []string{fid}})
	_, asst := pair(m)

	req := e.gen.requests()[0]
	if req.DynamicContext != "<retrieval>passages</retrieval>" {
		t.Fatalf("dynamic context: %q", req.DynamicContext)
	}
	files := req.Messages[len(req.Messages)-1].Files
	if len(files) != 1 || files[0].Text != "" || !files[0].Retrieved {
		t.Fatalf("a retrieved attachment must not carry its full text: %+v", files)
	}
	ks, _ := asst["knowledgeSources"].([]any)
	if len(ks) != 1 || str(ks[0].(map[string]any), "fileId") != fid {
		t.Fatalf("knowledge sources: %v", asst["knowledgeSources"])
	}
	// The trace carries the retrieval section.
	tr := e.ok("GET", "/messages/"+str(asst, "publicID")+"/trace", e.u1, nil)
	if !strings.Contains(fmt.Sprint(tr["processTrace"]), "retrieved 1 relevant passages") {
		t.Fatalf("trace: %v", tr["processTrace"])
	}

	// The evidence row belongs to the answer and expires after the retention.
	var rec ContextRecord
	if err := e.DB.Where("record_type = ? AND kind = ?", recordArtifact, "file_rag_chunk").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.UserID != 1 || rec.ConversationID == 0 || rec.MessageID == 0 || rec.RunID == "" || len(rec.ContentHash) != 64 {
		t.Fatalf("evidence ownership: %+v", rec)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("retention 90 days must set an expiry")
	}
	if d := rec.ExpiresAt.Sub(e.Clock.Now()); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("expiry is %v ahead", d)
	}
	art := e.ok("GET", fmt.Sprintf("/context-artifacts/%d", rec.ID), e.u1, nil)
	if str(art, "content") != "the whole file text" || str(art, "sourceID") != fid+":3" || art["expiresAt"] == nil {
		t.Fatalf("artifact: %v", art)
	}
	e.expectErr("GET", fmt.Sprintf("/context-artifacts/%d", rec.ID), e.u2, nil, 404, "context_artifact.not_found")
	e.DB.Model(&ContextRecord{}).Where("id = ?", rec.ID).UpdateColumn("expires_at", e.Clock.Now().Add(-time.Minute))
	e.expectErr("GET", fmt.Sprintf("/context-artifacts/%d", rec.ID), e.u1, nil, 404, "context_artifact.not_found")
}

func TestEvidenceNeverExpiresWithRetentionZero(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	e.set.mutate(func(rs *RuntimeSettings) { rs.ContextArtifactRetentionDays = 0 })
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.res = KnowledgeResult{Ran: true, Artifacts: []ContextArtifactInput{{Kind: "file_rag_chunk", SourceID: "x:0", Content: "c", ContentHash: strings.Repeat("a", 64)}}}
	e.send(e.u1, conv, "q", nil)
	var rec ContextRecord
	if err := e.DB.Where("record_type = ?", recordArtifact).First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.ExpiresAt != nil || rec.ContentHash != strings.Repeat("a", 64) {
		t.Fatalf("%+v", rec)
	}
}

func TestInlineAttachmentsKeepTheirText(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	fid := fileIDOf(e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("the whole file text")))
	other := fileIDOf(e.uploadOK(e.u1, "skip.txt", "text/plain", []byte("skipped file text")))
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.res = KnowledgeResult{Ran: true, Context: "ctx", Inline: map[string]bool{fid: true}, Retrieved: map[string]bool{}}
	e.send(e.u1, conv, "q", map[string]any{"fileIDs": []string{fid, other}})
	files := e.gen.requests()[0].Messages[0].Files
	byID := map[string]PromptFile{}
	for _, f := range files {
		byID[f.FileID] = f
	}
	if byID[fid].Text != "the whole file text" || byID[fid].Retrieved {
		t.Fatalf("inline file: %+v", byID[fid])
	}
	if byID[other].Text != "" || !byID[other].Retrieved {
		t.Fatalf("a file nobody provides loses its text (the context says so): %+v", byID[other])
	}
}

func TestRetrievalNotRunLeavesAttachmentsAlone(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	fid := fileIDOf(e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("the whole file text")))
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.res = KnowledgeResult{Ran: false}
	e.send(e.u1, conv, "q", map[string]any{"fileIDs": []string{fid}})
	req := e.gen.requests()[0]
	f := req.Messages[0].Files[0]
	if f.Text != "the whole file text" || f.Retrieved || req.DynamicContext != "" {
		t.Fatalf("%+v %q", f, req.DynamicContext)
	}
}

func TestRetrievalAttachesRetrievedImages(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.res = KnowledgeResult{Ran: true, Attach: []KnowledgeAttach{{FileID: "file_img", Name: "pic.png"}}}
	e.send(e.u1, conv, "q", nil)
	files := e.gen.requests()[0].Messages[0].Files
	if len(files) != 1 || files[0].Kind != "image" || files[0].FileID != "file_img" || files[0].FileName != "pic.png" {
		t.Fatalf("%+v", files)
	}
}

func TestRetrievalFailureAbortsTheSend(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	conv := e.newConv(e.u1, "t", "gpt-4o")
	kn.err = ErrKBUnavailable
	e.expectErr("POST", "/conversations/"+conv+"/messages", e.u1, sendBody("q", map[string]any{"knowledgeBaseIDs": []string{"kb"}}), 503, "knowledge_base.unavailable")
	for _, r := range e.gen.requests() {
		if r.Task == taskChat {
			t.Fatal("the model must not be called when retrieval aborts the send")
		}
	}
	if e.bill.released == 0 && e.bill.settled == 0 {
		t.Fatalf("the reservation must be closed: %+v", e.bill)
	}
}

func TestKnowledgeRequestDescribesTheBranch(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	e.set.users["1:chat.file_mode"] = "rag"
	e.set.mutate(func(rs *RuntimeSettings) { rs.ContextTokenBudgetEnabled = true; rs.ContextWindowFallbackTokens = 4321 })
	first := fileIDOf(e.uploadOK(e.u1, "first.txt", "text/plain", []byte("first file text")))
	second := fileIDOf(e.uploadOK(e.u1, "second.txt", "text/plain", []byte("second file text")))
	img := fileIDOf(e.uploadOK(e.u1, "pic.png", "image/png", pngBytes(10)))
	conv := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, conv, "first question", map[string]any{"fileIDs": []string{first, img}})
	kn.mu.Lock()
	kn.reqs = nil
	kn.mu.Unlock()
	e.send(e.u1, conv, "second question", map[string]any{"fileIDs": []string{second}, "knowledgeBaseIDs": []string{"kb1"}})
	reqs := kn.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.UserID != 1 || r.Message != "second question" || r.FileMode != "rag" || !r.ContextBudgetFeature || r.ContextBudgetTokens != 4321 {
		t.Fatalf("%+v", r)
	}
	if len(r.PriorUserMessages) != 1 || r.PriorUserMessages[0] != "first question" {
		t.Fatalf("prior user messages: %v", r.PriorUserMessages)
	}
	if len(r.KnowledgeBaseIDs) != 1 || r.KnowledgeBaseIDs[0] != "kb1" || r.Ephemeral {
		t.Fatalf("%+v", r)
	}
	byID := map[string]KnowledgeFile{}
	for _, f := range r.Files {
		byID[f.FileID] = f
	}
	if len(byID) != 2 || !byID[second].CurrentTurn || byID[first].CurrentTurn || byID[first].Generated {
		t.Fatalf("files: %+v", r.Files)
	}
	if _, ok := byID[img]; ok {
		t.Fatal("images are attached natively and never planned for retrieval")
	}
	if !byID[second].FullContextOK || byID[second].Text != "second file text" || byID[second].TextTokens == 0 {
		t.Fatalf("%+v", byID[second])
	}
}

func TestKnowledgeBasesIgnoredByRetrievalWhenTheFeatureIsOff(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	ps := &pendingSend{userID: 1, req: preparedRequest{Content: "q", KBIDs: []string{"kb1"}}}
	ps.conv = Conversation{}
	e.set.mutate(func(rs *RuntimeSettings) { rs.KnowledgeBaseEnabled = false })
	if _, err := e.svc.knowledgeFor(context.Background(), ps, func(string, map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	e.set.mutate(func(rs *RuntimeSettings) { rs.KnowledgeBaseEnabled = true })
	if _, err := e.svc.knowledgeFor(context.Background(), ps, func(string, map[string]any) {}); err != nil {
		t.Fatal(err)
	}
	reqs := kn.requests()
	if len(reqs) != 2 || len(reqs[0].KnowledgeBaseIDs) != 0 || len(reqs[1].KnowledgeBaseIDs) != 1 {
		t.Fatalf("%+v", reqs)
	}
}

func TestSearchingProgressEventIsStreamed(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	kn.res = KnowledgeResult{Ran: true}
	conv := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+conv+"/messages/stream", e.u1, sendBody("q", nil))
	got := types(ndjson(t, w))
	idx := -1
	for i, ty := range got {
		if ty == "rag_search" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("events: %v", got)
	}
}

func TestTemporaryChatRetrievalIsEphemeralAndReturnsSources(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	kn.res = KnowledgeResult{
		Ran: true, Context: "ctx",
		Sources:   []KnowledgeSource{{FileName: "kb.txt", FileID: "file_kb", ChunkIndex: 1, Score: 0.7, Preview: "p"}},
		Artifacts: []ContextArtifactInput{{Kind: "file_rag_chunk", SourceID: "file_kb:1", Content: "c"}},
	}
	body := tempBody([2]string{"user", "hi"})
	body["knowledgeBaseIDs"] = []string{"kb1"}
	w := e.call("POST", "/temporary-chat/messages/stream", e.u1, body)
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "completed" {
		t.Fatalf("lines: %v", types(lines))
	}
	asst := last["data"].(map[string]any)["assistantMessage"].(map[string]any)
	ks, _ := asst["knowledgeSources"].([]any)
	if len(ks) != 1 {
		t.Fatalf("temporary chats return sources: %v", asst)
	}
	reqs := kn.requests()
	if len(reqs) != 1 || !reqs[0].Ephemeral || reqs[0].UserID != 1 || len(reqs[0].Files) != 0 {
		t.Fatalf("%+v", reqs)
	}
	if got := e.gen.requests()[0].DynamicContext; got != "ctx" {
		t.Fatalf("dynamic context: %q", got)
	}
	var n int64
	e.DB.Model(&ContextRecord{}).Count(&n)
	if n != 0 {
		t.Fatal("nothing is persisted for a temporary chat")
	}
}

func TestTemporaryChatRetrievalErrorAborts(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	kn.err = ErrKBUnavailable
	body := tempBody([2]string{"user", "hi"})
	body["knowledgeBaseIDs"] = []string{"kb1"}
	w := e.call("POST", "/temporary-chat/messages/stream", e.u1, body)
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || !strings.Contains(fmt.Sprint(last), "knowledge_base.unavailable") {
		t.Fatalf("lines: %v", lines)
	}
	for _, r := range e.gen.requests() {
		if r.Task == taskChat {
			t.Fatal("the model must not run")
		}
	}
}

// ----- the file store -----

func TestFilesUseTheIndexerForVectorization(t *testing.T) {
	e, _, ix := newKnowledgeEnv(t)
	ix.vect = func(f FileFacts) (bool, string) {
		if f.FileCategory == "image" {
			return false, "unsupported"
		}
		return true, "outdated_index"
	}
	m := e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("hello file world"))
	f := m["file"].(map[string]any)
	if f["canVectorize"] != true || str(f, "vectorizationReason") != "outdated_index" {
		t.Fatalf("%v", f)
	}
	img := e.uploadOK(e.u1, "pic.png", "image/png", pngBytes(20))["file"].(map[string]any)
	if img["canVectorize"] != false || str(img, "vectorizationReason") != "unsupported" {
		t.Fatalf("%v", img)
	}
	p := e.ok("GET", "/files/"+str(f, "fileID")+"/processing", e.u1, nil)
	if p["canVectorize"] != true {
		t.Fatalf("processing payload: %v", p)
	}
	if len(ix.textReady) == 0 {
		t.Fatal("the indexer must be told when a file's text is ready")
	}
}

func TestSubmitEmbeddingsGoesThroughTheIndexer(t *testing.T) {
	e, _, ix := newKnowledgeEnv(t)
	w := e.call("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{"a", "b"}})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(ix.submitted) != 1 || len(ix.submitted[0]) != 2 {
		t.Fatalf("%v", ix.submitted)
	}
	ix.submitErr = ErrEmbedNotConfigured
	e.expectErr("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{"a"}}, 503, "embedding.service_not_configured")
	ix.submitErr = ErrEmbedTooMany
	e.expectErr("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{"a"}}, 400, "embedding.too_many_files")
}

func TestDeletingAFileForgetsItsIndex(t *testing.T) {
	e, _, ix := newKnowledgeEnv(t)
	fid := fileIDOf(e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("bye")))
	var obj FileObject
	e.DB.Where("file_id = ?", fid).First(&obj)
	e.ok("DELETE", "/files/"+fid, e.u1, nil)
	if len(ix.forgot) != 1 || ix.forgot[0] != obj.ID {
		t.Fatalf("forgot: %v", ix.forgot)
	}
}

func TestCopiedFileCarriesOrResetsTheIndex(t *testing.T) {
	e, _, ix := newKnowledgeEnv(t)
	svc := e.svc
	src := FileObject{
		FileID: "file_src", UserID: 1, FileName: "a.txt", Status: fileStatusActive, EmbedStatus: "ready",
		EmbedSignature: "sig@8", ChunkCount: 3, ProcessingReady: true,
	}
	src.ID = 7
	n := 0
	mk := func(s FileObject, _ uint) *FileObject {
		n++
		d := s
		d.ID, d.FileID, d.UserID = 0, fmt.Sprintf("file_dup%d", n), 2
		if err := e.DB.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
		return &d
	}
	dup := mk(src, 0)
	svc.carryIndex(context.Background(), e.DB, src, dup)
	if len(ix.cloned) != 1 || ix.cloned[0][0] != 7 || ix.cloned[0][1] != dup.ID || dup.EmbedStatus != "ready" || dup.ChunkCount != 3 {
		t.Fatalf("clone: %v %+v", ix.cloned, dup)
	}

	// A failing clone falls back to normal indexing: never-indexed copy.
	ix.cloneErr = errors.New("vector count mismatch")
	dup2 := mk(src, 0)
	svc.carryIndex(context.Background(), e.DB, src, dup2)
	var row FileObject
	e.DB.First(&row, dup2.ID)
	if row.EmbedStatus != "none" || row.EmbedSignature != "" || row.ChunkCount != 0 {
		t.Fatalf("fallback: %+v", row)
	}

	// A source that is not ready is not cloned.
	before := len(ix.cloned)
	notReady := src
	notReady.EmbedStatus = "failed"
	dup3 := mk(notReady, 0)
	svc.carryIndex(context.Background(), e.DB, notReady, dup3)
	e.DB.First(&row, dup3.ID)
	if len(ix.cloned) != before || row.EmbedStatus != "none" {
		t.Fatalf("not ready source: %+v", row)
	}
}

func TestPersistArtifactsRejectsMissingOwners(t *testing.T) {
	e, _, _ := newKnowledgeEnv(t)
	ps := &pendingSend{userID: 0}
	e.svc.persistArtifacts(context.Background(), ps, &TraceData{Artifacts: []ContextArtifactInput{{Kind: "k", SourceID: "s", Content: "c"}}})
	var n int64
	e.DB.Model(&ContextRecord{}).Count(&n)
	if n != 0 {
		t.Fatal("rows without owner ids must be rejected")
	}
}

func TestMergeKnowledge(t *testing.T) {
	now := time.Now()
	res := GenerateResult{Content: "x", KnowledgeSources: []KnowledgeSource{{FileID: "gen"}},
		Trace: &TraceData{Events: []TraceEvent{{EventID: "e1"}, {EventID: "e2", Seq: 5}}}}
	kr := KnowledgeResult{
		Ran: true, Sources: []KnowledgeSource{{FileID: "rag"}},
		Artifacts: []ContextArtifactInput{{Kind: "a"}},
		Trace:     &KnowledgeTrace{Title: "T", Summary: "S", Detail: "D", Status: "completed", PayloadJSON: "{}"},
	}
	out := mergeKnowledge(res, kr, now)
	if len(out.KnowledgeSources) != 2 || out.KnowledgeSources[0].FileID != "rag" {
		t.Fatalf("sources: %v", out.KnowledgeSources)
	}
	if len(out.Trace.Artifacts) != 1 || len(out.Trace.Events) != 3 || out.Trace.Events[0].EventID != retrievalEventID || out.Trace.Events[0].Seq != 1 {
		t.Fatalf("trace: %+v", out.Trace)
	}
	if out.Trace.Events[1].Seq != 2 || out.Trace.Events[2].Seq != 5 {
		t.Fatalf("sequence numbers: %+v", out.Trace.Events)
	}
	if res.Trace.Artifacts != nil || len(res.Trace.Events) != 2 {
		t.Fatal("the generator's trace must not be mutated")
	}
	if got := mergeKnowledge(res, KnowledgeResult{}, now); got.Trace != res.Trace {
		t.Fatal("a skipped retrieval changes nothing")
	}
}

// ----- reuse of earlier evidence -----

func TestHistoricalEvidenceIsOfferedAgain(t *testing.T) {
	e, kn, _ := newKnowledgeEnv(t)
	conv := e.newConv(e.u1, "t", "gpt-4o")
	_, a1 := pair(e.send(e.u1, conv, "first", nil))
	var asst Message
	e.DB.Where("public_id = ?", str(a1, "publicID")).First(&asst)
	var cv Conversation
	e.DB.Where("public_id = ?", conv).First(&cv)
	add := func(msgID, user uint, kind, src, content string, mod func(*ContextRecord)) {
		r := ContextRecord{
			UserID: user, ConversationID: cv.ID, MessageID: msgID, RunID: "r", RecordType: recordArtifact, Kind: kind,
			SourceType: "file_chunk", SourceID: src, SourceTitle: "doc " + src, Content: content, TokenEstimate: 10,
		}
		if mod != nil {
			mod(&r)
		}
		if err := e.DB.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	long := strings.Repeat("word ", 300)
	add(asst.ID, 1, "file_rag_chunk", "f:1", "alpha <evidence>", nil)
	add(asst.ID, 1, "file_rag_chunk", "f:2", long, nil)
	add(asst.ID, 1, "file_rag_chunk", "f:dup", "already in this turn", nil)
	add(asst.ID, 1, "tool_result", "t:1", "tool output", nil)
	add(asst.ID, 1, "unknown_kind", "u:1", "never reused", nil)
	add(asst.ID, 1, "file_rag_chunk", "f:expired", "expired evidence", func(r *ContextRecord) {
		past := e.Clock.Now().Add(-time.Hour)
		r.ExpiresAt = &past
	})
	add(asst.ID, 2, "file_rag_chunk", "f:foreign", "another user's evidence", nil)
	add(99999, 1, "file_rag_chunk", "f:branch", "evidence of another branch", nil)
	add(asst.ID, 1, "summary", "s:1", "a summary", nil)

	kn.res = KnowledgeResult{Ran: true, Context: "<retrieval>now</retrieval>", Artifacts: []ContextArtifactInput{{Kind: "file_rag_chunk", SourceID: "f:dup", Content: "already in this turn"}}}
	e.send(e.u1, conv, "second", nil)
	ctx := e.gen.lastChat().DynamicContext
	if !strings.HasPrefix(ctx, "<retrieval>now</retrieval>\n<historical_evidence>") {
		t.Fatalf("context:\n%s", ctx)
	}
	for _, want := range []string{"alpha &lt;evidence&gt;", `kind="tool_result"`, "a summary"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("missing %q in:\n%s", want, ctx)
		}
	}
	for _, bad := range []string{"already in this turn", "never reused", "expired evidence", "another user's", "another branch"} {
		if strings.Contains(ctx, bad) {
			t.Fatalf("%q must not be reused:\n%s", bad, ctx)
		}
	}
	if strings.Contains(ctx, strings.Repeat("word ", 150)) {
		t.Fatalf("snippets are cut to 500 characters")
	}

	// With a current snapshot summaries are not offered.
	e.DB.Create(&ContextRecord{UserID: 1, ConversationID: cv.ID, RecordType: recordSnapshot})
	e.send(e.u1, conv, "third", nil)
	ctx = e.gen.lastChat().DynamicContext
	if strings.Contains(ctx, "a summary") {
		t.Fatalf("summaries are used only while no snapshot is current:\n%s", ctx)
	}
}

func TestHistoricalEvidenceRespectsItemAndTokenLimits(t *testing.T) {
	e, _, _ := newKnowledgeEnv(t)
	conv := e.newConv(e.u1, "t", "gpt-4o")
	_, a1 := pair(e.send(e.u1, conv, "first", nil))
	var asst Message
	e.DB.Where("public_id = ?", str(a1, "publicID")).First(&asst)
	var cv Conversation
	e.DB.Where("public_id = ?", conv).First(&cv)
	for i := 0; i < 8; i++ {
		e.DB.Create(&ContextRecord{UserID: 1, ConversationID: cv.ID, MessageID: asst.ID, RunID: "r", RecordType: recordArtifact,
			Kind: "file_rag_chunk", SourceID: fmt.Sprintf("f:%d", i), SourceTitle: "d", Content: fmt.Sprintf("item number %d", i)})
	}
	e.send(e.u1, conv, "second", nil)
	ctx := e.gen.lastChat().DynamicContext
	if got := strings.Count(ctx, "<item "); got != historyEvidenceItems {
		t.Fatalf("%d items offered:\n%s", got, ctx)
	}
	// Newest evidence first.
	if !strings.Contains(ctx, "item number 7") || strings.Contains(ctx, "item number 3") {
		t.Fatalf("most recent items win:\n%s", ctx)
	}
	// A large item that does not fit the token cap is skipped, later small ones fit.
	e.DB.Where("1 = 1").Delete(&ContextRecord{})
	for i := 0; i < 3; i++ {
		e.DB.Create(&ContextRecord{UserID: 1, ConversationID: cv.ID, MessageID: asst.ID, RunID: "r", RecordType: recordArtifact,
			Kind: "file_rag_chunk", SourceID: fmt.Sprintf("big:%d", i), Content: strings.Repeat("x", 450)})
	}
	e.DB.Create(&ContextRecord{UserID: 1, ConversationID: cv.ID, MessageID: asst.ID, RunID: "r", RecordType: recordArtifact,
		Kind: "file_rag_chunk", SourceID: "small", Content: "tiny"})
	e.send(e.u1, conv, "third", nil)
	ctx = e.gen.lastChat().DynamicContext
	if strings.Count(ctx, "<item ") < 2 {
		t.Fatalf("token cap handling:\n%s", ctx)
	}
}

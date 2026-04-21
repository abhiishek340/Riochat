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

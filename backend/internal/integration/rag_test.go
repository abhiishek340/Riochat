package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const ragDims = 64

// embedServer answers OpenAI-style embedding requests with hashed
// bag-of-words vectors, so texts that share words are close.
type embedServer struct {
	*httptest.Server
	fail  atomic.Bool
	calls atomic.Int32
}

func newEmbedServer(t *testing.T) *embedServer {
	t.Helper()
	es := &embedServer{}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		es.calls.Add(1)
		if es.fail.Load() {
			http.Error(w, `{"error":"secret provider failure text"}`, http.StatusBadGateway)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type item struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var data []item
		for i, text := range req.Input {
			v := make([]float64, ragDims)
			words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			for _, wd := range words {
				h := fnv.New32a()
				h.Write([]byte(wd))
				v[h.Sum32()%ragDims]++
			}
			if len(words) == 0 {
				v[0] = 1
			}
			data = append(data, item{Embedding: v, Index: i})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(es.Close)
	return es
}

// chatServer records the chat requests it receives and streams a fixed answer.
type chatServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newChatServer(t *testing.T) *chatServer {
	t.Helper()
	cs := &chatServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.bodies = append(cs.bodies, string(raw))
		cs.mu.Unlock()
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		if req["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	t.Cleanup(cs.Close)
	return cs
}

// lastChatBody returns the newest request that carries the question marker.
func (cs *chatServer) lastWith(marker string) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for i := len(cs.bodies) - 1; i >= 0; i-- {
		if strings.Contains(cs.bodies[i], marker) {
			return cs.bodies[i]
		}
	}
	return ""
}

func patchSettings(t *testing.T, env *testkit.Env, admin map[string]string, items ...[3]string) {
	t.Helper()
	var body []map[string]string
	for _, it := range items {
		body = append(body, map[string]string{"namespace": it[0], "key": it[1], "value": it[2]})
	}
	w := env.Do("PATCH", "/api/v1/admin/settings", admin, map[string]any{"items": body})
	testkit.Expect(t, w, 200, "")
}

func enableRAG(t *testing.T, env *testkit.Env, admin map[string]string, host string) {
	t.Helper()
	patchSettings(t, env, admin,
		[3]string{"file", "embedding_host", host},
		[3]string{"file", "rag_model", "test-embed"},
		[3]string{"file", "embedding_output_dimensions", "64"},
		[3]string{"file", "embedding_enabled", "true"},
		[3]string{"chat", "rag_enabled", "true"},
		[3]string{"chat", "rag_min_similarity", "0.2"},
		[3]string{"chat", "rag_retrieval_cache_ttl_seconds", "0"},
	)
}

func uploadText(t *testing.T, env *testkit.Env, hdr map[string]string, name, content string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", "text/plain")
	part, _ := mw.CreatePart(h)
	part.Write([]byte(content))
	mw.Close()
	headers := map[string]string{"Content-Type": mw.FormDataContentType()}
	for k, v := range hdr {
		headers[k] = v
	}
	w := env.Do("POST", "/api/v1/files", headers, &buf)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Data struct{ File struct{ FileID string } } `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.File.FileID
}

type fileProc struct {
	EmbedStatus         string
	EmbedError          string
	ChunkCount          int
	CanVectorize        bool
	VectorizationReason string
	RAGReady            bool
}

func processing(t *testing.T, env *testkit.Env, hdr map[string]string, id string) fileProc {
	t.Helper()
	w := env.Do("GET", "/api/v1/files/"+id+"/processing", hdr, nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Data fileProc }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

func waitState(t *testing.T, env *testkit.Env, hdr map[string]string, id, want string) fileProc {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var p fileProc
	for time.Now().Before(deadline) {
		p = processing(t, env, hdr, id)
		if p.EmbedStatus == want {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s never reached %q (last %+v)", id, want, p)
	return p
}

type embedStatus struct {
	ModelSignature                            string
	ReadyCount, StaleCount, PendingCount      int64
	FailedCount, EmptyCount, UnsupportedCount int64
	StalledCount, ActiveCount                 int64
	NeedsReindex, ReindexRunning              bool
}

func adminStatus(t *testing.T, env *testkit.Env, admin map[string]string) embedStatus {
	t.Helper()
	w := env.Do("GET", "/api/v1/admin/settings/embedding/status", admin, nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Data embedStatus }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

const notesText = "The capital of Zorkland is Velmora. Velmora sits on the river Quenth and exports amber."

func TestRAGIndexingAndAdminOperations(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "rag-user@example.com", "")
	admin := session(t, env, "rag-admin@example.com", "admin")
	es := newEmbedServer(t)

	// Without embedding configuration the admin endpoints answer their
	// documented states and files cannot be vectorized.
	w := env.Do("GET", "/api/v1/admin/settings/embedding/runtime", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"status":"unconfigured"`) {
		t.Fatalf("runtime: %s", w.Body.String())
	}
	fid := uploadText(t, env, user, "notes.txt", notesText)
	if p := processing(t, env, user, fid); p.EmbedStatus == "ready" || p.CanVectorize || p.VectorizationReason != "embedding_disabled" {
		t.Fatalf("before configuration: %+v", p)
	}
	testkit.Expect(t, env.Do("POST", "/api/v1/files/embeddings", user, map[string]any{"fileIDs": []string{fid}}), 503, "embedding.service_not_configured")
	testkit.Expect(t, env.Do("POST", "/api/v1/files/embeddings", user, map[string]any{"fileIDs": []string{" ", "  "}}), 200, "")

	// Enabling RAG while the service is not ready is refused.
	w = env.Do("PATCH", "/api/v1/admin/settings", admin, map[string]any{"items": []map[string]string{{"namespace": "chat", "key": "rag_enabled", "value": "true"}}})
	testkit.Expect(t, w, 400, "settings.embedding_invalid")

	enableRAG(t, env, admin, es.URL+"/v1")
	w = env.Do("GET", "/api/v1/admin/settings/embedding/runtime", admin, nil)
	if !strings.Contains(w.Body.String(), `"status":"running"`) || !strings.Contains(w.Body.String(), `"reachable":true`) {
		t.Fatalf("healthy runtime: %s", w.Body.String())
	}

	// The file uploaded before the configuration is submitted explicitly.
	p := processing(t, env, user, fid)
	if !p.CanVectorize {
		t.Fatalf("a text file must be vectorizable now: %+v", p)
	}
	w = env.Do("POST", "/api/v1/files/embeddings", user, map[string]any{"fileIDs": []string{fid, "nope"}})
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"submittedFileIDs":["`+fid+`"]`) || !strings.Contains(w.Body.String(), `"reason":"not_found"`) {
		t.Fatalf("submit: %s", w.Body.String())
	}
	done := waitState(t, env, user, fid, "ready")
	if done.ChunkCount == 0 || !done.RAGReady {
		t.Fatalf("indexed: %+v", done)
	}

	// A file uploaded now is indexed automatically after upload.
	auto := uploadText(t, env, user, "auto.txt", "Automatic indexing of this second document about amber trade.")
	waitState(t, env, user, auto, "ready")

	st := adminStatus(t, env, admin)
	if st.ReadyCount != 2 || st.NeedsReindex || st.ModelSignature == "" {
		t.Fatalf("status: %+v", st)
	}
	w = env.Do("GET", "/api/v1/admin/settings/embedding/tasks?bucket=ready&query=notes", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"fileName":"notes.txt"`) || strings.Contains(w.Body.String(), "auto.txt") {
		t.Fatalf("tasks: %s", w.Body.String())
	}
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/settings/embedding/tasks?bucket=bogus", admin, nil), 400, "request.invalid_query")

	// Changing the model makes the old index stale; a client-chosen signature
	// is overwritten by the server.
	patchSettings(t, env, admin, [3]string{"file", "rag_model", "other-embed"}, [3]string{"file", "embedding_model_signature", "forged@64"})
	st = adminStatus(t, env, admin)
	if st.StaleCount != 2 || !st.NeedsReindex || st.ModelSignature == "forged@64" {
		t.Fatalf("after model change: %+v", st)
	}
	if p := processing(t, env, user, fid); p.EmbedStatus != "stale" || p.VectorizationReason != "outdated_index" || !p.CanVectorize {
		t.Fatalf("stale file: %+v", p)
	}

	// Reindex rebuilds both files; a second call right away may report the
	// first still running, otherwise it has nothing to do.
	w = env.Do("POST", "/api/v1/admin/settings/embedding/reindex", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"submitted":2`) {
		t.Fatalf("reindex: %s", w.Body.String())
	}
	waitState(t, env, user, fid, "ready")
	waitState(t, env, user, auto, "ready")
	w = env.Do("POST", "/api/v1/admin/settings/embedding/reindex", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"submitted":0`) {
		t.Fatalf("nothing left to rebuild: %s", w.Body.String())
	}

	// Admin retry of a ready file is skipped; user cannot retry others'.
	w = env.Do("POST", "/api/v1/admin/settings/embedding/tasks/retry", admin, map[string]any{"fileIDs": []string{fid}})
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"reason":"already_ready"`) {
		t.Fatalf("retry: %s", w.Body.String())
	}
	other := session(t, env, "rag-other@example.com", "")
	w = env.Do("POST", "/api/v1/files/embeddings", other, map[string]any{"fileIDs": []string{fid}})
	if !strings.Contains(w.Body.String(), `"reason":"not_found"`) {
		t.Fatalf("a user cannot submit another user's file: %s", w.Body.String())
	}

	// Audit entries carry the documented fields.
	var n int64
	env.DB.Table("audit_logs").Where("action IN ?", []string{"settings.embedding_reindex", "settings.embedding_retry"}).Count(&n)
	if n != 3 {
		t.Fatalf("audit rows: %d", n)
	}

	// A provider outage ends the file failed without provider text.
	es.fail.Store(true)
	down := uploadText(t, env, user, "down.txt", "document uploaded while the provider is down")
	got := waitState(t, env, user, down, "failed")
	if strings.Contains(got.EmbedError, "secret") {
		t.Fatalf("provider text leaked: %q", got.EmbedError)
	}
	w = env.Do("GET", "/api/v1/admin/settings/embedding/runtime", admin, nil)
	if !strings.Contains(w.Body.String(), `"status":"unhealthy"`) || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unhealthy runtime: %s", w.Body.String())
	}
}

func chatEnv(t *testing.T) (env *testkit.Env, user, admin map[string]string, es *embedServer, cs *chatServer, conv string) {
	t.Helper()
	env = testkit.New(t, registry.Registered())
	user = session(t, env, "chat-user@example.com", "")
	admin = session(t, env, "chat-admin@example.com", "admin")
	es = newEmbedServer(t)
	cs = newChatServer(t)
	addModel(t, env, admin, cs.URL+"/v1", "rag-chat")
	enableRAG(t, env, admin, es.URL+"/v1")
	w := env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "rag-chat"})
	testkit.Expect(t, w, 200, "")
	var c struct{ Data struct{ PublicID string } }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	return env, user, admin, es, cs, c.Data.PublicID
}

func send(env *testkit.Env, user map[string]string, conv string, body map[string]any) *httptest.ResponseRecorder {
	body["contentType"] = "text"
	body["model"] = "rag-chat"
	return env.Do("POST", "/api/v1/conversations/"+conv+"/messages", user, body)
}

func TestRAGInjectsPassagesIntoTheChat(t *testing.T) {
	env, user, _, es, cs, conv := chatEnv(t)
	testkit.Expect(t, env.Do("PATCH", "/api/v1/user/settings", user, map[string]any{"settings": map[string]string{"chat.file_mode": "rag"}}), 200, "")
	fid := uploadText(t, env, user, "notes.txt", notesText)
	waitState(t, env, user, fid, "ready")

	w := send(env, user, conv, map[string]any{"content": "Q1 capital of Zorkland Velmora?", "fileIDs": []string{fid}})
	testkit.Expect(t, w, 200, "")
	body := cs.lastWith("Q1 capital")
	if !strings.Contains(body, "retrieval") || !strings.Contains(body, "Velmora") {
		t.Fatalf("passages were not injected:\n%s", body)
	}
	if !strings.Contains(w.Body.String(), `"knowledgeSources"`) || !strings.Contains(w.Body.String(), `"fileId":"`+fid+`"`) {
		t.Fatalf("answer lacks knowledge sources: %s", w.Body.String())
	}
	var rec struct {
		ID       uint
		UserID   uint
		Kind     string
		SourceID string
	}
	if err := env.DB.Table("chat_context_records").Where("kind = ?", "file_rag_chunk").Order("id").Take(&rec).Error; err != nil {
		t.Fatalf("no evidence stored: %v", err)
	}
	if rec.UserID == 0 || !strings.HasPrefix(rec.SourceID, fid+":") {
		t.Fatalf("evidence row: %+v", rec)
	}
	art := env.Do("GET", fmt.Sprintf("/api/v1/context-artifacts/%d", rec.ID), user, nil)
	testkit.Expect(t, art, 200, "")
	if !strings.Contains(art.Body.String(), "Velmora") {
		t.Fatalf("artifact: %s", art.Body.String())
	}
	other := session(t, env, "chat-other@example.com", "")
	testkit.Expect(t, env.Do("GET", fmt.Sprintf("/api/v1/context-artifacts/%d", rec.ID), other, nil), 404, "context_artifact.not_found")

	// The provider fails: the full text is injected instead and the evidence
	// is stored as a fallback.
	es.fail.Store(true)
	w = send(env, user, conv, map[string]any{"content": "Q2 capital of Zorkland?", "fileIDs": []string{fid}})
	testkit.Expect(t, w, 200, "")
	body = cs.lastWith("Q2 capital")
	if !strings.Contains(body, "full_text_files") || !strings.Contains(body, "Quenth") {
		t.Fatalf("full-text fallback missing:\n%s", body)
	}
	var fallbacks int64
	env.DB.Table("chat_context_records").Where("kind = ?", "file_rag_fallback").Count(&fallbacks)
	if fallbacks != 1 {
		t.Fatalf("fallback evidence rows: %d", fallbacks)
	}
}

func TestRAGKnowledgeBases(t *testing.T) {
	env, user, admin, es, cs, conv := chatEnv(t)
	patchSettings(t, env, admin, [3]string{"chat", "rag_min_similarity", "0.4"})
	fid := uploadText(t, env, user, "kbdoc.txt", notesText)
	waitState(t, env, user, fid, "ready")

	w := env.Do("POST", "/api/v1/knowledge-bases/mine", user, map[string]any{"name": "Atlas"})
	testkit.Expect(t, w, 200, "")
	var kb struct {
		Data struct{ KnowledgeBase struct{ PublicID string } }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &kb)
	kbID := kb.Data.KnowledgeBase.PublicID
	testkit.Expect(t, env.Do("POST", "/api/v1/knowledge-bases/mine/"+kbID+"/files", user, map[string]any{"fileIDs": []string{fid}}), 200, "")

	// A relevant question gets passages and sources.
	w = send(env, user, conv, map[string]any{"content": "K1 what is the capital of Zorkland Velmora river Quenth exports amber", "knowledgeBaseIDs": []string{kbID}})
	testkit.Expect(t, w, 200, "")
	if body := cs.lastWith("K1 what"); !strings.Contains(body, "Velmora") {
		t.Fatalf("knowledge base passages missing:\n%s", body)
	}
	if !strings.Contains(w.Body.String(), `"fileId":"`+fid+`"`) {
		t.Fatalf("sources: %s", w.Body.String())
	}

	// An unrelated question proceeds with the no-evidence notice and no source.
	w = send(env, user, conv, map[string]any{"content": "K2 zzzz yyyy xxxx", "knowledgeBaseIDs": []string{kbID}})
	testkit.Expect(t, w, 200, "")
	if body := cs.lastWith("K2 zzzz"); !strings.Contains(body, "Do not claim") {
		t.Fatalf("no-evidence notice missing:\n%s", body)
	}
	if strings.Contains(w.Body.String(), `"knowledgeSources":[{`) {
		t.Fatalf("no source expected: %s", w.Body.String())
	}

	// With the provider down an explicitly selected base aborts the send.
	es.fail.Store(true)
	w = send(env, user, conv, map[string]any{"content": "K3 capital?", "knowledgeBaseIDs": []string{kbID}})
	testkit.Expect(t, w, 503, "knowledge_base.unavailable")

	// A base with no ready file is refused before anything runs.
	empty := env.Do("POST", "/api/v1/knowledge-bases/mine", user, map[string]any{"name": "Empty"})
	var kb2 struct {
		Data struct{ KnowledgeBase struct{ PublicID string } }
	}
	_ = json.Unmarshal(empty.Body.Bytes(), &kb2)
	es.fail.Store(false)
	w = send(env, user, conv, map[string]any{"content": "K4", "knowledgeBaseIDs": []string{kb2.Data.KnowledgeBase.PublicID}})
	testkit.Expect(t, w, 409, "knowledge_base.not_ready")
}

func TestRAGDeletingAFileRemovesItsIndex(t *testing.T) {
	// A ready file removed by its owner leaves no chunks or vectors behind.
	env, user, _, _, _, _ := chatEnv(t)
	fid := uploadText(t, env, user, "gone.txt", notesText)
	waitState(t, env, user, fid, "ready")
	var before int64
	env.DB.Table("file_chunk_embeddings").Count(&before)
	if before == 0 {
		t.Fatal("expected vector rows")
	}
	testkit.Expect(t, env.Do("DELETE", "/api/v1/files/"+fid, user, nil), 200, "")
	var after, chunks int64
	env.DB.Table("file_chunk_embeddings").Count(&after)
	env.DB.Table("file_chunks").Count(&chunks)
	if after != 0 || chunks != 0 {
		t.Fatalf("deleting a file must remove its index: vectors %d chunks %d", after, chunks)
	}
}

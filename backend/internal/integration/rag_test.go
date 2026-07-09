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

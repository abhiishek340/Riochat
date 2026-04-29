package knowledgebase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ----- fakes for the collaborators -----

type fakeAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (a *fakeAuditor) Record(_ context.Context, e AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
}

func (a *fakeAuditor) byAction(action string) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []AuditEntry
	for _, e := range a.entries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

type fakeSettings struct {
	mu       sync.Mutex
	disabled bool
	maxBytes int64
}

func (s *fakeSettings) Enabled(context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disabled
}
func (s *fakeSettings) MaxUploadBytes(context.Context) int64 { return s.maxBytes }
func (s *fakeSettings) set(disabled bool) {
	s.mu.Lock()
	s.disabled = disabled
	s.mu.Unlock()
}

// fakeFiles plays reader, cleaner and uploader of the file subsystem.
type fakeFiles struct {
	te *tenv

	// kept holds file ids that are "referenced elsewhere" and survive cleanup.
	kept     map[string]bool
	deleted  []string
	cleanErr map[string]error

	readOwners []uint
	readErr    error

	uploadErr error
	uploads   []UploadRequest
	uploaded  []byte
	counter   int
}

func (f *fakeFiles) OpenFile(_ context.Context, owner uint, fileID string) (*FileContent, error) {
	f.readOwners = append(f.readOwners, owner)
	if f.readErr != nil {
		return nil, f.readErr
	}
	body := fmt.Sprintf("bytes-of-%s-owner-%d", fileID, owner)
	return &FileContent{Body: io.NopCloser(strings.NewReader(body)), Size: int64(len(body))}, nil
}

func (f *fakeFiles) DeleteUnreferenced(_ context.Context, owner uint, fileID string) (bool, error) {
	if err := f.cleanErr[fileID]; err != nil {
		return false, err
	}
	if f.kept[fileID] {
		return false, nil
	}
	res := f.te.DB.Where("file_id = ? AND user_id = ?", fileID, owner).Delete(&fileObject{})
	if res.RowsAffected > 0 {
		f.deleted = append(f.deleted, fileID)
	}
	return res.RowsAffected > 0, res.Error
}

func (f *fakeFiles) Upload(_ context.Context, req UploadRequest) (string, error) {
	if f.uploadErr != nil {
		return "", f.uploadErr
	}
	f.uploads = append(f.uploads, req)
	f.uploaded, _ = io.ReadAll(req.Body)
	f.counter++
	id := fmt.Sprintf("pool-upload-%d", f.counter)
	f.te.addFile(req.OwnerUserID, id, func(o *fileObject) {
		o.FileName = req.FileName
		o.Purpose = req.Purpose
		o.SizeBytes = int64(len(f.uploaded))
	})
	return id, nil
}

type fakeEmbedder struct {
	err    error
	owners []uint
	seen   [][]string
	skip   map[string]string
}

func (e *fakeEmbedder) SubmitFiles(_ context.Context, owner uint, ids []string) (EmbedResult, error) {
	if e.err != nil {
		return EmbedResult{}, e.err
	}
	e.owners = append(e.owners, owner)
	e.seen = append(e.seen, ids)
	var res EmbedResult
	for _, id := range ids {
		if why, ok := e.skip[id]; ok {
			res.Skipped = append(res.Skipped, SkippedFile{FileID: id, Reason: why})
			continue
		}
		res.Submitted = append(res.Submitted, id)
	}
	return res, nil
}

type fakeVectorizer struct{}

func (fakeVectorizer) CanVectorize(_ context.Context, f FileFacts) (bool, string) {
	if f.EmbedStatus == "ready" {
		return false, "already_embedded"
	}
	return true, "ok"
}

// ----- the test environment -----

type tenv struct {
	*testkit.Env
	t        *testing.T
	svc      *Service
	aud      *fakeAuditor
	settings *fakeSettings
	files    *fakeFiles
	embed    *fakeEmbedder
	nextFile uint
}

type setup struct {
	noCleaner, noReader, noUploader, noEmbedder, noVectorizer bool
}

func newTestEnv(t *testing.T, opts ...func(*setup)) *tenv {
	t.Helper()
	var st setup
	for _, o := range opts {
		o(&st)
	}
	te := &tenv{t: t, aud: &fakeAuditor{}, settings: &fakeSettings{}, embed: &fakeEmbedder{}}
	te.files = &fakeFiles{te: te, kept: map[string]bool{}, cleanErr: map[string]error{}}
	ports := Ports{Settings: te.settings, Auditor: te.aud}
	if !st.noReader {
		ports.Reader = te.files
	}
	if !st.noCleaner {
		ports.Cleaner = te.files
	}
	if !st.noUploader {
		ports.Uploader = te.files
	}
	if !st.noEmbedder {
		ports.Embedder = te.embed
	}
	if !st.noVectorizer {
		ports.Vectorizer = fakeVectorizer{}
	}
	te.Env = testkit.New(t, []features.Feature{{
		Name:       "knowledgebase",
		Migrations: Migrations,
		Mount: func(d deps.Deps) error {
			te.svc = NewService(d.DB, d.Clock, d.Logger, ports)
			return MountService(d, te.svc)
		},
	}})
	if err := te.DB.AutoMigrate(&fileObject{}); err != nil {
		t.Fatalf("file_objects: %v", err)
	}
	return te
}

func (te *tenv) user(id uint) map[string]string  { return te.Bearer(testkit.User(id)) }
func (te *tenv) admin(id uint) map[string]string { return te.Bearer(testkit.Admin(id)) }

// addFile inserts a ready-to-use active file object and returns it.
func (te *tenv) addFile(owner uint, fileID string, mods ...func(*fileObject)) fileObject {
	te.t.Helper()
	te.nextFile++
	o := fileObject{
		FileID: fileID, UserID: owner, Purpose: "chat", FileName: fileID + ".txt", MimeType: "text/plain",
		DetectedMime: "text/plain", FileCategory: "document", SizeBytes: 10, Status: fileStatusActive,
		ProcessingStatus: "ready", ExtractStatus: "ready", EmbedStatus: "idle",
		CreatedAt: te.Clock.Now().Add(time.Duration(te.nextFile) * time.Second), UpdatedAt: te.Clock.Now(),
	}
	for _, m := range mods {
		m(&o)
	}
	if err := te.DB.Create(&o).Error; err != nil {
		te.t.Fatalf("addFile %s: %v", fileID, err)
	}
	return o
}

func ragReady(o *fileObject) {
	o.ProcessingReady = true
	o.EmbedStatus = "ready"
	o.ChunkCount = 2
}

// createMine creates a personal base through the API and returns it.
func (te *tenv) createMine(uid uint, name string) kbOut {
	te.t.Helper()
	w := te.Do("POST", "/api/v1/knowledge-bases/mine", te.user(uid), map[string]any{"name": name})
	testkit.Expect(te.t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(te.t, w, &out)
	return out.KnowledgeBase
}

func (te *tenv) createBuiltin(name string, enabled bool) kbOut {
	te.t.Helper()
	w := te.Do("POST", "/api/v1/admin/knowledge-bases", te.admin(1), map[string]any{"name": name, "enabled": enabled})
	testkit.Expect(te.t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(te.t, w, &out)
	return out.KnowledgeBase
}

// link attaches files to a base directly in the database.
func (te *tenv) link(kb kbOut, files ...fileObject) {
	te.t.Helper()
	var base KnowledgeBase
	if err := te.DB.Where("public_id = ?", kb.PublicID).Take(&base).Error; err != nil {
		te.t.Fatalf("link: %v", err)
	}
	for i, f := range files {
		l := KnowledgeBaseFile{KnowledgeBaseID: base.ID, FileObjectID: f.ID, SortOrder: i + 1, CreatedAt: te.Clock.Now()}
		if err := te.DB.Create(&l).Error; err != nil {
			te.t.Fatalf("link: %v", err)
		}
	}
}

func (te *tenv) reload(kb kbOut) KnowledgeBase {
	te.t.Helper()
	var base KnowledgeBase
	if err := te.DB.Where("public_id = ?", kb.PublicID).Take(&base).Error; err != nil {
		te.t.Fatalf("reload: %v", err)
	}
	return base
}

func (te *tenv) baseExists(kb kbOut) bool {
	var n int64
	te.DB.Model(&KnowledgeBase{}).Where("public_id = ?", kb.PublicID).Count(&n)
	return n > 0
}

// ----- response shapes -----

type kbOut struct {
	PublicID            string
	Scope               string
	Name                string
	Description         string
	Enabled             bool
	SortOrder           int
	Revision            uint64
	FileCount           int64
	ReadyFileCount      int64
	ProcessingFileCount int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type kbPage struct {
	Total   int64
	Results []kbOut
}

type fileOut struct {
	FileID              string
	FileName            string
	ProcessingStatus    string
	Processing          bool
	ProcessingReady     bool
	EmbedStatus         string
	ChunkCount          int
	CanVectorize        bool
	VectorizationReason string
}

type filePage struct {
	Total   int64
	Results []fileOut
}

func (te *tenv) listKB(path string, hdr map[string]string) kbPage {
	te.t.Helper()
	w := te.Do("GET", path, hdr, nil)
	testkit.Expect(te.t, w, 200, "")
	var p kbPage
	testkit.Decode(te.t, w, &p)
	return p
}

func names(p kbPage) []string {
	out := make([]string, len(p.Results))
	for i, r := range p.Results {
		out[i] = r.Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// multipartBody builds a multipart request body with one file part.
func multipartBody(field, filename string, content []byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
		h.Set("Content-Type", "text/plain")
		part, _ := mw.CreatePart(h)
		part.Write(content)
	} else {
		mw.WriteField("other", "x")
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func (te *tenv) upload(hdr map[string]string, field, filename string, content []byte) *httptest.ResponseRecorder {
	te.t.Helper()
	body, ctype := multipartBody(field, filename, content)
	h := map[string]string{"Content-Type": ctype}
	for k, v := range hdr {
		h[k] = v
	}
	return te.Do("POST", "/api/v1/admin/knowledge-bases/files", h, body)
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }

package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

func pool() *outbound.Pool { return outbound.NewPool(outbound.PoolOptions{Proxy: outbound.NoProxy}) }

type seen struct {
	mu     sync.Mutex
	method string
	path   string
	header http.Header
	body   []byte
	fields map[string]string
	files  map[string]string
}

func (s *seen) capture(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.method, s.path, s.header = r.Method, r.URL.Path, r.Header.Clone()
	s.body, _ = io.ReadAll(r.Body)
	s.fields, s.files = map[string]string{}, map[string]string{}
	if mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil && strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(bytes.NewReader(s.body), params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			if p.FileName() != "" {
				s.files[p.FormName()] = p.FileName() + "|" + p.Header.Get("Content-Type") + "|" + string(data)
			} else {
				s.fields[p.FormName()] = string(data)
			}
		}
	}
}

func serve(t *testing.T, status int, ctype, body string) (*httptest.Server, *seen) {
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.capture(r)
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func TestNormalizeText(t *testing.T) {
	if got := NormalizeText("  a  \r\n\n\t b\t\n   \nc"); got != "a\nb\nc" {
		t.Fatalf("%q", got)
	}
	if NormalizeText(" \n ") != "" {
		t.Fatal("blank")
	}
}

func TestErrorFormatting(t *testing.T) {
	if (&Error{Code: "x_http_500"}).Error() != "x_http_500" || (&Error{Code: "x_unprocessable", Detail: "bad"}).Error() != "x_unprocessable: bad" {
		t.Fatal("rendering")
	}
	if !IsCode(fmt.Errorf("wrapped: %w", coded("a_b", "")), "a_b") || IsCode(errors.New("x"), "a_b") {
		t.Fatal("IsCode")
	}
}

func TestTika(t *testing.T) {
	srv, s := serve(t, 200, "text/plain", "  hello  \n\n  world \n")
	res, err := NewTika(pool(), srv.URL+"/", "TOK", 0).Extract(context.Background(), Request{Name: "a.docx", MIME: "application/x", Data: []byte("BIN")})
	if err != nil || res.Text != "hello\nworld" || res.Engine != "tika" {
		t.Fatalf("%v %+v", err, res)
	}
	if s.method != "PUT" || s.path != "/tika" || s.header.Get("Accept") != "text/plain" || s.header.Get("Content-Type") != "application/x" ||
		s.header.Get("X-Tika-Filename") != "a.docx" || s.header.Get("Authorization") != "Bearer TOK" || string(s.body) != "BIN" {
		t.Fatalf("%+v", s)
	}
	cases := []struct {
		status int
		body   string
		code   string
	}{
		{204, "", "tika_empty_content"}, {200, "  \n ", "tika_empty_content"}, {401, "", "tika_unauthorized"}, {403, "", "tika_forbidden"},
		{415, "", "tika_unsupported_media_type"}, {422, "corrupt file", "tika_unprocessable: corrupt file"}, {500, "oops", "tika_http_500: oops"}, {502, "", "tika_http_502"},
	}
	for _, tc := range cases {
		srv, _ := serve(t, tc.status, "", tc.body)
		_, err := NewTika(pool(), srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")})
		if err == nil || err.Error() != tc.code {
			t.Errorf("%d: got %v want %s", tc.status, err, tc.code)
		}
	}
	dead, _ := serve(t, 200, "", "")
	dead.Close()
	_, err = NewTika(pool(), dead.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")})
	if !IsCode(err, "tika_unavailable") {
		t.Fatalf("%v", err)
	}
	ok, _ := NewTika(pool(), srv.URL, "", 0).Probe(context.Background())
	if !ok {
		t.Fatal("probe")
	}
	if ok, _ := NewTika(pool(), dead.URL, "", 0).Probe(context.Background()); ok {
		t.Fatal("probe of a dead server")
	}
}

func TestDocling(t *testing.T) {
	bodies := map[string]string{
		`{"text":"top"}`:                         "top",
		`{"full_text":"ft"}`:                     "ft",
		`{"content":"c"}`:                        "c",
		`{"markdown":"# md"}`:                    "# md",
		`{"data":{"markdown":"nested data"}}`:    "nested data",
		`{"result":{"text":"nested result"}}`:    "nested result",
		"plain text body":                        "plain text body",
		`{"data":[{"text":"p1"},{"text":"p2"}]}`: "p1\np2",
	}
	for body, want := range bodies {
		srv, s := serve(t, 200, "application/json", body)
		res, err := NewDocling(pool(), srv.URL+"/ocr", "TOK", 0).Extract(context.Background(), Request{Name: "f.pdf", MIME: "application/pdf", Data: []byte("PDF")})
		if err != nil || res.Text != want {
			t.Errorf("%s → %v %+v", body, err, res)
		}
		if s.fields["file_name"] != "f.pdf" || s.fields["mime_type"] != "application/pdf" || s.files["file"] != "f.pdf|application/pdf|PDF" {
			t.Errorf("multipart: %+v %+v", s.fields, s.files)
		}
		for _, h := range []string{"Authorization", "X-Api-Key", "Token"} {
			if s.header.Get(h) == "" {
				t.Errorf("auth header %s missing", h)
			}
		}
	}
	for status, code := range map[int]string{401: "docling_unauthorized", 403: "docling_forbidden", 422: "docling_unprocessable: bad", 500: "docling_http_500: bad", 415: "docling_http_415: bad"} {
		srv, _ := serve(t, status, "", "bad")
		_, err := NewDocling(pool(), srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")})
		if err == nil || err.Error() != code {
			t.Errorf("%d: %v want %s", status, err, code)
		}
	}
	srv, _ := serve(t, 200, "", `{"text":"   "}`)
	if _, err := NewDocling(pool(), srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")}); !IsCode(err, "docling_empty_content") {
		t.Fatalf("%v", err)
	}
	if healthURL("http://h:8005/ocr") != "http://h:8005/healthz" || healthURL("http://h:1/") != "http://h:1/healthz" {
		t.Fatal("health url derivation")
	}
}

func TestOCRServices(t *testing.T) {
	srv, s := serve(t, 200, "application/json", `{"data":{"pages":[{"text":"page one"},{"text":"page two"}]}}`)
	res, err := NewOCR(pool(), "rapidocr", srv.URL+"/ocr", "T", 0).Extract(context.Background(),
		Request{Name: "scan.png", MIME: "image/png", Data: []byte("IMG"), Pages: "1,3-5", Model: "m", Prompt: "p"})
	if err != nil || res.Text != "page one\npage two" || len(res.PageTexts) != 2 || res.Pages != 2 || res.Engine != "rapidocr" {
		t.Fatalf("%v %+v", err, res)
	}
	want := map[string]string{"name": "scan.png", "mime_type": "image/png", "pages": "1,3-5", "model": "m", "prompt": "p"}
	for k, v := range want {
		if s.fields[k] != v {
			t.Errorf("%s=%q want %q", k, s.fields[k], v)
		}
	}
	for status, code := range map[int]string{401: "ocr_unauthorized", 403: "ocr_forbidden", 422: "ocr_unprocessable: x", 502: "ocr_http_502: x"} {
		srv, _ := serve(t, status, "", "x")
		_, err := NewOCR(pool(), "tesseract", srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")})
		if err == nil || err.Error() != code {
			t.Errorf("%d: %v", status, err)
		}
	}
	srv, _ = serve(t, 200, "", "plain ocr text")
	if r, err := NewOCR(pool(), "paddle", srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")}); err != nil || r.Text != "plain ocr text" {
		t.Fatalf("%v", err)
	}
	srv, _ = serve(t, 200, "", "")
	if _, err := NewOCR(pool(), "paddle", srv.URL, "", 0).Extract(context.Background(), Request{Data: []byte("x")}); !IsCode(err, "ocr_empty_content") {
		t.Fatalf("%v", err)
	}
}

func TestMistralOCR(t *testing.T) {
	srv, s := serve(t, 200, "application/json", `{"pages":[{"index":1,"markdown":"second"},{"index":0,"markdown":"first"}]}`)
	m := NewMistralOCR(pool(), srv.URL, "KEY", "", 0)
	res, err := m.Extract(context.Background(), Request{Name: "a.pdf", MIME: "application/pdf", Data: []byte("PDF"), Pages: "1,3-4"})
	if err != nil || res.Text != "first\nsecond" || res.Pages != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	if s.header.Get("Authorization") != "Bearer KEY" {
		t.Fatal("bearer")
	}
	body := string(s.body)
	for _, want := range []string{`"model":"mistral-ocr-latest"`, `"document_url":"data:application/pdf;base64,UERG"`, `"pages":[0,2,3]`, `"include_image_base64":false`, `"include_blocks":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if _, err := m.Extract(context.Background(), Request{Name: "a.png", MIME: "image/png", Data: []byte("IMG")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s.body), `"image_url":"data:image/png;base64,SU1H"`) || strings.Contains(string(s.body), "pages") {
		t.Fatalf("%s", s.body)
	}
	if NewMistralOCR(pool(), "", "k", "", 0).b.baseURL != DefaultMistralURL {
		t.Fatal("default url")
	}
	if got := parsePages("1, 3-5,3,x,0,9-2"); len(got) != 4 || got[0] != 0 || got[3] != 4 {
		t.Fatalf("%v", got)
	}
}

// ---- MinerU ---------------------------------------------------------------

func zipOf(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		io.WriteString(w, content)
	}
	zw.Close()
	return buf.Bytes()
}

func TestMinerUCloudFlow(t *testing.T) {
	polls := 0
	var uploaded []byte
	var mux http.ServeMux
	srv := httptest.NewServer(&mux)
	defer srv.Close()
	mux.HandleFunc("/api/v4/file-urls/batch", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"language":"ch"`) || !strings.Contains(string(b), `"is_ocr":true`) || !strings.Contains(string(b), `"name":"doc.pdf"`) || r.Header.Get("Authorization") != "Bearer TOK" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprintf(w, `{"code":0,"data":{"batch_id":"B1","file_urls":["%s/upload/1"]}}`, srv.URL)
	})
	mux.HandleFunc("/upload/1", func(w http.ResponseWriter, r *http.Request) {
		uploaded, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/v4/extract-results/batch/B1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 3 {
			io.WriteString(w, `{"data":{"extract_result":[{"state":"running"}]}}`)
			return
		}
		fmt.Fprintf(w, `{"data":{"extract_result":[{"state":"done","full_zip_url":"%s/zip"}]}}`, srv.URL)
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipOf(map[string]string{"images/a.png": "x", "notes.md": "other", "full.md": "# Title\n\n  body  "}))
	})
	var sleeps []time.Duration
	// The cloud flow uses the strict policy; with no enforcement configured it
	// reaches the loopback test server.
	m := NewMinerU(pool(), "cloud", srv.URL+"/api/v4", "TOK", 0, func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil })
	res, err := m.Extract(context.Background(), Request{Name: "doc.pdf", MIME: "application/pdf", Data: []byte("PDFDATA")})
	if err != nil || res.Text != "# Title\nbody" {
		t.Fatalf("%v %+v", err, res)
	}
	if string(uploaded) != "PDFDATA" || polls != 3 || len(sleeps) != 3 || sleeps[0] != 2*time.Second {
		t.Fatalf("uploaded=%q polls=%d sleeps=%v", uploaded, polls, sleeps)
	}
}

func TestMinerUFailuresAndFallback(t *testing.T) {
	mk := func(state, msg string) *MinerU {
		var mux http.ServeMux
		srv := httptest.NewServer(&mux)
		t.Cleanup(srv.Close)
		mux.HandleFunc("/file-urls/batch", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":{"batch_id":"B","file_urls":["%s/up"]}}`, srv.URL)
		})
		mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {})
		mux.HandleFunc("/extract-results/batch/B", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":{"extract_result":[{"state":%q,"err_msg":%q}]}}`, state, msg)
		})
		return NewMinerU(pool(), "cloud", srv.URL, "", 0, func(context.Context, time.Duration) error { return nil })
	}
	_, err := mk("failed", "bad pdf").Extract(context.Background(), Request{Name: "a.pdf", Data: []byte("x")})
	if err == nil || err.Error() != "mineru_unprocessable: bad pdf" {
		t.Fatalf("%v", err)
	}
	_, err = mk("error", "").Extract(context.Background(), Request{Name: "a.pdf", Data: []byte("x")})
	if !IsCode(err, "mineru_failed") {
		t.Fatalf("%v", err)
	}

	// Self-hosted configured, endpoint missing (404) → falls back to the batch flow.
	var mux http.ServeMux
	srv := httptest.NewServer(&mux)
	defer srv.Close()
	mux.HandleFunc("/file-urls/batch", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":{"batch_id":"B","file_urls":["%s/up"]}}`, srv.URL)
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/extract-results/batch/B", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":{"extract_result":[{"state":"done","full_zip_url":"%s/z"}]}}`, srv.URL)
	})
	mux.HandleFunc("/z", func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipOf(map[string]string{"out/full.md": "from batch"}))
	})
	m := NewMinerU(pool(), "self_hosted", srv.URL, "", 0, func(context.Context, time.Duration) error { return nil })
	res, err := m.Extract(context.Background(), Request{Name: "a.pdf", Data: []byte("x")})
	if err != nil || res.Text != "from batch" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestMinerUSelfHosted(t *testing.T) {
	srv, s := serve(t, 200, "application/json", `{"results":{"doc":{"md_content":"# Parsed\n\ntext"}}}`)
	m := NewMinerU(pool(), "self_hosted", srv.URL, "T", 0, nil)
	res, err := m.Extract(context.Background(), Request{Name: "doc.pdf", MIME: "application/pdf", Data: []byte("PDF")})
	if err != nil || res.Text != "# Parsed\ntext" {
		t.Fatalf("%v %+v", err, res)
	}
	if s.path != "/file_parse" || !strings.HasPrefix(s.files["files"], "doc.pdf|") {
		t.Fatalf("%s %v", s.path, s.files)
	}
	for status, code := range map[int]string{401: "mineru_unauthorized", 403: "mineru_forbidden", 500: "mineru_http_500: e"} {
		srv, _ := serve(t, status, "", "e")
		_, err := NewMinerU(pool(), "self_hosted", srv.URL, "", 0, nil).Extract(context.Background(), Request{Name: "a", Data: []byte("x")})
		if err == nil || err.Error() != code {
			t.Errorf("%d: %v", status, err)
		}
	}
	srv, _ = serve(t, 200, "", "not json")
	if _, err := NewMinerU(pool(), "self_hosted", srv.URL, "", 0, nil).Extract(context.Background(), Request{Name: "a", Data: []byte("x")}); !IsCode(err, "mineru_invalid_response") {
		t.Fatalf("%v", err)
	}
	if NewMinerU(pool(), "", "", "", 0, nil).b.baseURL != DefaultMinerUBase || NewMinerU(pool(), "", "", "", 0, nil).b.timeout != 180*time.Second {
		t.Fatal("cloud defaults")
	}
}

// ---- built-in parsers ------------------------------------------------------

func docxBytes() []byte {
	return zipOf(map[string]string{"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="x"><w:body>
<w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:tab/><w:t>world</w:t></w:r></w:p>
<w:p><w:r><w:t>Second</w:t></w:r><w:r><w:br/><w:t>line</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B1</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`})
}

func xlsxBytes() []byte {
	return zipOf(map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="r"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>Name</t></si><si><t>Qty</t></si><si><r><t>Ap</t></r><r><t>ple</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2"><v>5</v></c></row>
<row r="3"></row></sheetData></worksheet>`,
	})
}

func TestBuiltinParsers(t *testing.T) {
	b := &Builtin{}
	ctx := context.Background()
	res, err := b.Extract(ctx, Request{Name: "n.txt", Data: []byte("\xef\xbb\xbf hello \n world \xff")})
	if err != nil || !strings.HasPrefix(res.Text, "hello") || res.Engine != "builtin" {
		t.Fatalf("%v %+v", err, res)
	}
	res, err = b.Extract(ctx, Request{Name: "main.go", Data: []byte("package main")})
	if err != nil || res.Text != "package main" {
		t.Fatalf("by extension: %v", err)
	}
	res, err = b.Extract(ctx, Request{Name: "d.docx", Data: docxBytes()})
	if err != nil || res.Text != "Hello\tworld\nSecond\nline\nA1\tB1" {
		t.Fatalf("%v %q", err, res.Text)
	}
	res, err = b.Extract(ctx, Request{Name: "s.xlsx", Data: xlsxBytes()})
	if err != nil || res.Text != "Sheet: Data\nName |  | Qty\nApple |  | 5" {
		t.Fatalf("%v %q", err, res.Text)
	}
	res, err = b.Extract(ctx, Request{Name: "t.csv", Data: []byte("a,b\n\n\"x,y\",2\n")})
	if err != nil || res.Text != "Sheet: t\na | b\nx,y | 2" {
		t.Fatalf("%v %q", err, res.Text)
	}
	for _, name := range []string{"x.pdf", "x.xls", "x.bin"} {
		if _, err := b.Extract(ctx, Request{Name: name, Data: []byte("x")}); !IsCode(err, "builtin_unsupported") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := b.Extract(ctx, Request{Name: "bad.docx", Data: []byte("not a zip")}); !IsCode(err, "builtin_unprocessable") {
		t.Fatalf("%v", err)
	}
	if _, err := b.Extract(ctx, Request{Name: "e.txt", Data: []byte("  ")}); !IsCode(err, "builtin_empty_content") {
		t.Fatalf("%v", err)
	}
	if ok, _ := b.Probe(ctx); !ok || b.Name() != "builtin" {
		t.Fatal("probe")
	}
}

func TestBuiltinLegacyDoc(t *testing.T) {
	var used string
	b := &Builtin{
		LookPath: func(n string) (string, error) {
			if n == "textutil" {
				return "/usr/bin/textutil", nil
			}
			return "", errors.New("not found")
		},
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			used = name
			return []byte("  converted \n\n text "), nil
		},
	}
	res, err := b.Extract(context.Background(), Request{Name: "old.doc", Data: []byte("DOC")})
	if err != nil || res.Text != "converted\ntext" || res.Engine != "textutil" || used != "textutil" {
		t.Fatalf("the result reports the engine that produced it: %v %+v", err, res)
	}
	none := &Builtin{LookPath: func(string) (string, error) { return "", errors.New("no") }}
	if _, err := none.Extract(context.Background(), Request{Name: "old.doc", Data: []byte("x")}); !IsCode(err, "builtin_unsupported") {
		t.Fatalf("%v", err)
	}
}

// ---- model OCR --------------------------------------------------------------

type fakeRenderer struct {
	images map[int][]byte
	errs   map[int]error
	err    error
	asked  []int
}

func (f *fakeRenderer) Render(ctx context.Context, pdf []byte, pages []int) (map[int][]byte, map[int]error, error) {
	f.asked = pages
	return f.images, f.errs, f.err
}

func llmServer(t *testing.T, status int) (*httptest.Server, *[]string) {
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"nope"}}`)
			return
		}
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"  text of call %d  "}}]}`, n)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestLLMOCRImageAndPDF(t *testing.T) {
	srv, bodies := llmServer(t, 200)
	client := llm.NewClient(llm.ClientOptions{HTTP: pool()})
	rend := &fakeRenderer{images: map[int][]byte{2: []byte("J2"), 1: []byte("J1")}}
	e := NewLLMOCR(client, llm.Route{BaseURL: srv.URL, Model: "vision", APIKey: "k"}, "", rend, 0)
	res, err := e.Extract(context.Background(), Request{Name: "a.png", MIME: "image/png", Data: []byte("IMG")})
	if err != nil || res.Text != "text of call 1" || res.Pages != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.Contains((*bodies)[0], "image_url") || !strings.Contains((*bodies)[0], "reading order") {
		t.Fatalf("one image part and the default prompt: %s", (*bodies)[0])
	}
	res, err = e.Extract(context.Background(), Request{Name: "a.pdf", MIME: "application/pdf", Data: []byte("PDF"), Pages: "2-3"})
	if err != nil || res.Text != "text of call 2\n\ntext of call 3" || res.Pages != 2 || len(res.PageTexts) != 2 {
		t.Fatalf("pages are recognized in order: %v %+v", err, res)
	}
	if len(rend.asked) != 2 || rend.asked[0] != 2 || rend.asked[1] != 3 {
		t.Fatalf("asked pages (1-based): %v", rend.asked)
	}
	// Failures.
	if _, err := e.Extract(context.Background(), Request{Name: "a.pdf", MIME: "application/pdf", Data: []byte("x"), Pages: "zero"}); err == nil || err.Error() != "ocr_unprocessable: no target pages" {
		t.Fatalf("%v", err)
	}
	empty := &fakeRenderer{images: map[int][]byte{}}
	e2 := NewLLMOCR(client, llm.Route{BaseURL: srv.URL, Model: "v"}, "", empty, 0)
	if _, err := e2.Extract(context.Background(), Request{Name: "a.pdf", MIME: "application/pdf", Data: []byte("x")}); err == nil || err.Error() != "ocr_unprocessable: no page images found" {
		t.Fatalf("%v", err)
	}
	failing := &fakeRenderer{images: map[int][]byte{}, errs: map[int]error{3: errors.New("bad page")}}
	e3 := NewLLMOCR(client, llm.Route{BaseURL: srv.URL, Model: "v"}, "", failing, 0)
	if _, err := e3.Extract(context.Background(), Request{Name: "a.pdf", MIME: "application/pdf", Data: []byte("x"), Pages: "3"}); err == nil || !strings.Contains(err.Error(), "page 3: bad page") {
		t.Fatalf("render failures are listed per page: %v", err)
	}
	if ok, _ := e.Probe(context.Background()); !ok {
		t.Fatal("probe")
	}
}

func TestLLMOCRErrorMapping(t *testing.T) {
	for status, code := range map[int]string{401: "ocr_unauthorized", 403: "ocr_forbidden", 400: "ocr_unprocessable: nope", 422: "ocr_unprocessable: nope", 500: "ocr_http_500: nope"} {
		srv, _ := llmServer(t, status)
		client := llm.NewClient(llm.ClientOptions{HTTP: pool()})
		e := NewLLMOCR(client, llm.Route{BaseURL: srv.URL, Model: "v"}, "", &fakeRenderer{}, 0)
		_, err := e.Extract(context.Background(), Request{Name: "a.png", MIME: "image/png", Data: []byte("x")})
		if err == nil || err.Error() != code {
			t.Errorf("%d: %v want %s", status, err, code)
		}
	}
	srv, _ := llmServer(t, 200)
	srv.Close()
	client := llm.NewClient(llm.ClientOptions{HTTP: pool()})
	e := NewLLMOCR(client, llm.Route{BaseURL: srv.URL, Model: "v"}, "", &fakeRenderer{}, 0)
	if _, err := e.Extract(context.Background(), Request{Name: "a.png", MIME: "image/png", Data: []byte("x")}); !IsCode(err, "ocr_unavailable") {
		t.Fatalf("transport failures: %v", err)
	}
	un := NewLLMOCR(client, llm.Route{}, "", &fakeRenderer{}, 0)
	if _, err := un.Extract(context.Background(), Request{Name: "a.png", MIME: "image/png", Data: []byte("x")}); !IsCode(err, "ocr_unavailable") {
		t.Fatalf("%v", err)
	}
}

func TestPopplerRendererCommandShape(t *testing.T) {
	var calls [][]string
	p := &PopplerRenderer{Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, errors.New("tool missing")
	}}
	imgs, errs, err := p.Render(context.Background(), []byte("%PDF"), []int{2})
	if err != nil || len(imgs) != 0 || errs[2] == nil {
		t.Fatalf("%v %v %v", err, imgs, errs)
	}
	joined := strings.Join(calls[0], " ")
	if !strings.HasPrefix(joined, "pdftoppm -jpeg -r 150 -f 2 -l 2 -singlefile") {
		t.Fatalf("%s", joined)
	}
	if _, _, err := p.Render(context.Background(), []byte("%PDF"), nil); err == nil {
		t.Fatal("a failing renderer for all pages reports the failure")
	}
}

// ---- factory ----------------------------------------------------------------

type fakeResolver struct{ ok map[string]bool }

func (f fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if f.ok[host] {
		return []string{"10.0.0.2"}, nil
	}
	return nil, errors.New("no such host")
}

func TestManagedResolution(t *testing.T) {
	yes := fakeResolver{ok: map[string]bool{ManagedTikaHost: true}}
	no := fakeResolver{}
	if ResolveManaged(context.Background(), yes, ManagedTikaHost, ManagedTikaURL, LocalTikaURL) != ManagedTikaURL ||
		ResolveManaged(context.Background(), no, ManagedTikaHost, ManagedTikaURL, LocalTikaURL) != LocalTikaURL {
		t.Fatal("managed resolution")
	}
	if c := ManagedCandidates(context.Background(), yes, ManagedTikaHost, ManagedTikaURL, LocalTikaURL); len(c) != 2 || c[0] != ManagedTikaURL || c[1] != LocalTikaURL {
		t.Fatalf("%v", c)
	}
	if c := ManagedCandidates(context.Background(), no, ManagedTikaHost, ManagedTikaURL, LocalTikaURL); len(c) != 1 || c[0] != LocalTikaURL {
		t.Fatalf("%v", c)
	}
}

func TestFactoryAvailability(t *testing.T) {
	ctx := context.Background()
	d := Deps{HTTP: pool(), Resolver: fakeResolver{ok: map[string]bool{ManagedRapidOCRHost: true}}}
	e, err := New(ctx, "builtin", Config{}, d)
	if err != nil || e.Name() != "builtin" {
		t.Fatal(err)
	}
	if e, _ := New(ctx, "", Config{}, d); e.Name() != "builtin" {
		t.Fatal("empty means builtin")
	}
	missing := map[string]string{
		"tika": "tika_unavailable", "docling": "docling_unavailable", "rapidocr": "ocr_unavailable", "tesseract": "ocr_unavailable",
		"paddle": "ocr_unavailable", "mistral": "ocr_unavailable", "llm": "ocr_unavailable", "tencent": "ocr_unavailable", "aliyun": "ocr_unavailable", "nonsense": "nonsense_unavailable",
	}
	for kind, code := range missing {
		if _, err := New(ctx, kind, Config{}, d); !IsCode(err, code) {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if _, err := New(ctx, "mineru", Config{Source: "self_hosted"}, d); !IsCode(err, "mineru_unavailable") {
		t.Fatalf("%v", err)
	}
	if e, err := New(ctx, "mineru", Config{}, d); err != nil || e.Name() != "mineru" {
		t.Fatalf("cloud needs no url: %v", err)
	}
	e, err = New(ctx, "rapidocr", Config{Source: "managed"}, d)
	if err != nil || e.(*OCR).b.baseURL != ManagedRapidOCRURL {
		t.Fatalf("%v", err)
	}
	e, _ = New(ctx, "tika", Config{Source: "managed"}, d)
	if e.(*Tika).b.baseURL != LocalTikaURL {
		t.Fatal("unresolvable managed host falls back to localhost")
	}
	if e, err := New(ctx, "mistral", Config{Token: "k"}, d); err != nil || e.Name() != "mistral" {
		t.Fatal(err)
	}
}

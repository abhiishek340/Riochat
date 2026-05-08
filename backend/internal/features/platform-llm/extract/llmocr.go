package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
)

// DefaultOCRPrompt asks for plain text in reading order.
const DefaultOCRPrompt = "Extract all text from this image as plain text, in reading order. Output only the text, without commentary."

// PageRenderer renders PDF pages to JPEG.
type PageRenderer interface {
	// Render returns JPEG bytes per 1-based page. pages empty means all pages.
	// Per-page failures are reported in errs; err means nothing could run.
	Render(ctx context.Context, pdf []byte, pages []int) (images map[int][]byte, errs map[int]error, err error)
}

// PopplerRenderer renders with the pdftoppm command-line tool.
type PopplerRenderer struct {
	Bin string // default "pdftoppm"
	DPI int    // default 150
	Run CommandRunner
}

// Render implements PageRenderer.
func (p *PopplerRenderer) Render(ctx context.Context, pdf []byte, pages []int) (map[int][]byte, map[int]error, error) {
	bin := p.Bin
	if bin == "" {
		bin = "pdftoppm"
	}
	dpi := p.DPI
	if dpi <= 0 {
		dpi = 150
	}
	run := p.Run
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	dir, err := os.MkdirTemp("", "riochat-pdf-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(src, pdf, 0o600); err != nil {
		return nil, nil, err
	}
	images := map[int][]byte{}
	errs := map[int]error{}
	renderOne := func(args []string, prefix string) error {
		full := append([]string{"-jpeg", "-r", strconv.Itoa(dpi)}, args...)
		full = append(full, src, filepath.Join(dir, prefix))
		out, err := run(ctx, bin, full...)
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if len(pages) == 0 {
		if err := renderOne(nil, "p"); err != nil {
			return nil, nil, err
		}
		entries, _ := filepath.Glob(filepath.Join(dir, "p-*.jpg"))
		sort.Strings(entries)
		for _, e := range entries {
			base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(e), "p-"), ".jpg")
			n, err := strconv.Atoi(base)
			if err != nil {
				continue
			}
			if b, err := os.ReadFile(e); err == nil {
				images[n] = b
			}
		}
		return images, errs, nil
	}
	for _, pg := range pages {
		prefix := "pg" + strconv.Itoa(pg)
		if err := renderOne([]string{"-f", strconv.Itoa(pg), "-l", strconv.Itoa(pg), "-singlefile"}, prefix); err != nil {
			errs[pg] = err
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, prefix+".jpg"))
		if err != nil {
			errs[pg] = err
			continue
		}
		images[pg] = b
	}
	return images, errs, nil
}

// LLMOCR recognizes text with a vision model through the provider client.
type LLMOCR struct {
	Client   *llm.Client
	Route    llm.Route
	Prompt   string
	Renderer PageRenderer
	Timeout  time.Duration
}

// NewLLMOCR returns the engine. A route without base URL or model is
// unavailable (see Extract).
func NewLLMOCR(c *llm.Client, route llm.Route, prompt string, renderer PageRenderer, timeout time.Duration) *LLMOCR {
	if renderer == nil {
		renderer = &PopplerRenderer{}
	}
	if route.Protocol == "" {
		route.Protocol = string(llm.OpenAIChatCompletions)
	}
	return &LLMOCR{Client: c, Route: route, Prompt: prompt, Renderer: renderer, Timeout: timeout}
}

// Name implements Engine.
func (l *LLMOCR) Name() string { return "llm" }

// Probe reports configuration state; the model endpoint is not pinged.
func (l *LLMOCR) Probe(context.Context) (bool, string) {
	if strings.TrimSpace(l.Route.BaseURL) == "" || strings.TrimSpace(l.Route.Model) == "" {
		return false, "not configured"
	}
	return true, "configured"
}

func (l *LLMOCR) ocrCall(ctx context.Context, jpeg []byte, mime, prompt string) (string, error) {
	res, err := l.Client.Generate(ctx, l.Route, &llm.Request{
		Ephemeral: true,
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{
			{Kind: llm.PartImage, MIME: mime, Data: jpeg},
			{Kind: llm.PartText, Text: prompt},
		}}},
	})
	if err != nil {
		return "", mapLLMError(err)
	}
	return res.Text, nil
}

func mapLLMError(err error) error {
	var le *llm.Error
	if errors.As(err, &le) {
		switch {
		case le.Kind == llm.KindUpstream && le.Status == http.StatusUnauthorized:
			return coded("ocr_unauthorized", "")
		case le.Kind == llm.KindUpstream && le.Status == http.StatusForbidden:
			return coded("ocr_forbidden", "")
		case le.Kind == llm.KindUpstream && (le.Status == http.StatusBadRequest || le.Status == http.StatusUnprocessableEntity):
			return coded("ocr_unprocessable", le.Message)
		case le.Kind == llm.KindUpstream:
			return coded(fmt.Sprintf("ocr_http_%d", le.Status), le.Message)
		}
	}
	return &Error{Code: "ocr_unavailable", Detail: err.Error(), Cause: err}
}

// Extract recognizes an image directly, or a PDF page by page.
func (l *LLMOCR) Extract(ctx context.Context, r Request) (*Result, error) {
	if strings.TrimSpace(l.Route.BaseURL) == "" || strings.TrimSpace(l.Route.Model) == "" {
		return nil, coded("ocr_unavailable", "model ocr is not configured")
	}
	if l.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, l.Timeout)
		defer cancel()
	}
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(l.Prompt)
	}
	if prompt == "" {
		prompt = DefaultOCRPrompt
	}
	mime := strings.ToLower(strings.TrimSpace(r.MIME))
	if strings.HasPrefix(mime, "image/") {
		text, err := l.ocrCall(ctx, r.Data, mime, prompt)
		if err != nil {
			return nil, err
		}
		if text = NormalizeText(text); text == "" {
			return nil, coded("ocr_empty_content", "")
		}
		return &Result{Text: text, Pages: 1, PageTexts: []string{text}, Engine: "llm"}, nil
	}
	if mime != "application/pdf" && !strings.HasSuffix(strings.ToLower(r.Name), ".pdf") {
		return nil, coded("ocr_unprocessable", "unsupported file type")
	}
	var pages []int
	if strings.TrimSpace(r.Pages) != "" {
		for _, p := range parsePages(r.Pages) {
			pages = append(pages, p+1)
		}
		if len(pages) == 0 {
			return nil, coded("ocr_unprocessable", "no target pages")
		}
	}
	images, errs, err := l.Renderer.Render(ctx, r.Data, pages)
	if err != nil {
		return nil, coded("ocr_unprocessable", "pdf rendering failed: "+err.Error())
	}
	if len(images) == 0 {
		if len(errs) > 0 {
			return nil, coded("ocr_unprocessable", renderFailures(errs))
		}
		return nil, coded("ocr_unprocessable", "no page images found")
	}
	nums := make([]int, 0, len(images))
	for n := range images {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var texts []string
	var all []string
	for _, n := range nums {
		t, err := l.ocrCall(ctx, images[n], "image/jpeg", prompt)
		if err != nil {
			return nil, err
		}
		t = NormalizeText(t)
		texts = append(texts, t)
		if t != "" {
			all = append(all, t)
		}
	}
	text := strings.Join(all, "\n\n")
	if text == "" {
		return nil, coded("ocr_empty_content", "")
	}
	res := &Result{Text: text, Pages: len(nums), PageTexts: texts, Engine: "llm"}
	if len(errs) > 0 {
		res.Text += "\n\n" + "[" + renderFailures(errs) + "]"
	}
	return res, nil
}

func renderFailures(errs map[int]error) string {
	keys := make([]int, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("page %d: %v", k, errs[k]))
	}
	return "render failures: " + strings.Join(parts, "; ")
}

var _ = bytes.MinRead

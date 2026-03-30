package llm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
)

// imageAdapter serves the OpenAI, OpenRouter and xAI image protocols.
type imageAdapter struct{}

const (
	openRouterMaxReferences = 16
	xaiMaxEditImages        = 3
)

var (
	orAspectRatios = map[string]bool{"1:1": true, "1:2": true, "1:4": true, "1:8": true, "2:1": true, "2:3": true, "3:2": true,
		"3:4": true, "4:1": true, "4:3": true, "4:5": true, "5:4": true, "8:1": true, "9:16": true, "16:9": true,
		"9:19.5": true, "19.5:9": true, "9:20": true, "20:9": true, "9:21": true, "21:9": true, "auto": true}
	orResolutions = map[string]bool{"512": true, "1K": true, "2K": true, "4K": true}
	orQualities   = map[string]bool{"auto": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	orFormats     = map[string]bool{"png": true, "jpeg": true, "webp": true}
	orBackgrounds = map[string]bool{"auto": true, "transparent": true, "opaque": true}
	orSortNames   = map[string]bool{"price": true, "throughput": true, "latency": true, "exacto": true}
	pixelSizeRe   = regexp.MustCompile(`^\d{2,5}x\d{2,5}$`)
)

func isGPTImage(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-image-")
}

func isGPTImageOrChatGPT(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-image-") || m == "chatgpt-image-latest"
}

func isDallE(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return m == "dall-e-2" || m == "dall-e-3"
}

func (imageAdapter) build(x *exchange) (*call, error) {
	if x.model == "" {
		return nil, invalid(ErrInvalidRequest, "model is required")
	}
	switch x.proto {
	case OpenAIImageGenerations:
		return buildOpenAIImageGen(x)
	case OpenAIImageEdits:
		return buildOpenAIImageEdit(x)
	case OpenRouterImages:
		return buildOpenRouterImages(x)
	case XAIImage, XAIImageEdits:
		return buildXAIImages(x)
	}
	return nil, invalid(ErrUnsupportedProtocol, "unsupported image protocol %q", x.proto)
}

func requirePrompt(x *exchange) (string, error) {
	p := imagePrompt(x.req.Messages)
	if p == "" {
		return "", invalid(ErrInvalidRequest, "image request requires a prompt")
	}
	return p, nil
}

func buildOpenAIImageGen(x *exchange) (*call, error) {
	o := x.opts
	prompt, err := requirePrompt(x)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": x.model, "prompt": prompt}
	if isDallE(x.model) {
		rf := "b64_json"
		if s, ok := o.str("response_format"); ok && (s == "url" || s == "b64_json") {
			rf = s
		}
		body["response_format"] = rf
	}
	if s, ok := o.str("quality"); ok {
		body["quality"] = s
	}
	if s, ok := o.str("size"); ok {
		body["size"] = s
	}
	if s, ok := o.str("user"); ok {
		body["user"] = s
	}
	if n, ok := o.positiveInt("n"); ok {
		body["n"] = n
	}
	if isGPTImage(x.model) {
		for _, k := range []string{"background", "moderation", "output_format"} {
			if s, ok := o.str(k); ok {
				body[k] = s
			}
		}
		if n, ok := o.positiveInt("output_compression"); ok {
			body["output_compression"] = n
		}
	}
	if strings.EqualFold(strings.TrimSpace(x.model), "dall-e-3") {
		if s, ok := o.str("style"); ok {
			body["style"] = s
		}
	}
	if x.stream {
		body["stream"] = true
		if n, ok := o.positiveInt("partial_images"); ok {
			body["partial_images"] = n
		}
	}
	// Image endpoints reject unknown fields, so nothing else is forwarded.
	u, err := x.apiURL("v1", "/images/generations")
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, u, body)
}

func fileNameFor(p Part, i int) string {
	if strings.TrimSpace(p.FileName) != "" {
		return p.FileName
	}
	return fmt.Sprintf("image-%02d.%s", i+1, mimeExt(imageMIMEDefaultPNG(p.MIME)))
}

func imageMIMEDefaultPNG(m string) string {
	if strings.TrimSpace(m) == "" {
		return "image/png"
	}
	return imageMIME(m)
}

func buildOpenAIImageEdit(x *exchange) (*call, error) {
	o := x.opts
	prompt, err := requirePrompt(x)
	if err != nil {
		return nil, err
	}
	images := inputImages(x.req.Messages)
	if len(images) == 0 {
		return nil, invalid(ErrInvalidRequest, "image edit requires at least one input image")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	field := func(k, v string) { _ = w.WriteField(k, v) }
	field("model", x.model)
	field("prompt", prompt)
	file := func(name, fname, mime string, data []byte) error {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, fname))
		h.Set("Content-Type", mime)
		pw, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		_, err = pw.Write(data)
		return err
	}
	for i, im := range images {
		if err := file("image[]", fileNameFor(im, i), imageMIMEDefaultPNG(im.MIME), im.Data); err != nil {
			return nil, invalid(ErrInvalidRequest, "cannot encode image: %v", err)
		}
	}
	if m := x.req.Mask; m != nil && len(m.Data) > 0 {
		if err := file("mask", fileNameFor(*m, 0), imageMIMEDefaultPNG(m.MIME), m.Data); err != nil {
			return nil, invalid(ErrInvalidRequest, "cannot encode mask: %v", err)
		}
	}
	if n, ok := o.positiveInt("n"); ok {
		field("n", fmt.Sprint(n))
	}
	for _, k := range []string{"size", "quality", "user"} {
		if s, ok := o.str(k); ok {
			field(k, s)
		}
	}
	if strings.EqualFold(strings.TrimSpace(x.model), "dall-e-2") {
		if s, ok := o.str("response_format"); ok && (s == "url" || s == "b64_json") {
			field("response_format", s)
		}
	}
	if isGPTImageOrChatGPT(x.model) {
		for _, k := range []string{"background", "input_fidelity", "moderation", "output_format"} {
			if s, ok := o.str(k); ok {
				field(k, s)
			}
		}
		if n, ok := o.positiveInt("output_compression"); ok {
			field("output_compression", fmt.Sprint(n))
		}
	}
	if x.stream {
		field("stream", "true")
		if n, ok := o.positiveInt("partial_images"); ok {
			field("partial_images", fmt.Sprint(n))
		}
	}
	if err := w.Close(); err != nil {
		return nil, invalid(ErrInvalidRequest, "cannot encode form: %v", err)
	}
	u, err := x.apiURL("v1", "/images/edits")
	if err != nil {
		return nil, err
	}
	return &call{method: http.MethodPost, url: u, body: buf.Bytes(), contentType: w.FormDataContentType()}, nil
}

func buildOpenRouterImages(x *exchange) (*call, error) {
	o := x.opts
	prompt, err := requirePrompt(x)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": x.model, "prompt": prompt}
	imgs := inputImages(x.req.Messages)
	if len(imgs) > openRouterMaxReferences {
		return nil, invalid(ErrInvalidRequest, "openrouter image requests accept at most %d reference images", openRouterMaxReferences)
	}
	if len(imgs) > 0 {
		refs := make([]any, len(imgs))
		for i, im := range imgs {
			refs[i] = map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(imageMIME(im.MIME), im.Data)}}
		}
		body["input_references"] = refs
	}
	if x.stream {
		body["stream"] = true
	}
	if n, ok := o.int("n"); ok && n >= 1 && n <= 10 {
		body["n"] = n
	}
	if s, ok := o.str("aspect_ratio"); ok && orAspectRatios[s] {
		body["aspect_ratio"] = s
	}
	if s, ok := o.str("resolution"); ok && orResolutions[strings.ToUpper(s)] {
		body["resolution"] = strings.ToUpper(s)
	}
	if s, ok := o.str("size"); ok {
		if orResolutions[strings.ToUpper(s)] {
			body["size"] = strings.ToUpper(s)
		} else if pixelSizeRe.MatchString(strings.ToLower(s)) {
			body["size"] = strings.ToLower(s)
		}
	}
	if s, ok := o.str("quality"); ok && orQualities[strings.ToLower(s)] {
		body["quality"] = strings.ToLower(s)
	}
	if s, ok := o.str("output_format"); ok && orFormats[strings.ToLower(s)] {
		body["output_format"] = strings.ToLower(s)
	}
	if s, ok := o.str("background"); ok && orBackgrounds[strings.ToLower(s)] {
		body["background"] = strings.ToLower(s)
	}
	if n, ok := o.int("output_compression"); ok && n >= 0 && n <= 100 {
		body["output_compression"] = n
	}
	if n, ok := o.int("seed"); ok {
		body["seed"] = n
	}
	if s, ok := o.str("user"); ok {
		body["user"] = s
	}
	if p, ok := o.object("provider"); ok {
		if pr := sanitizeORProvider(p); len(pr) > 0 {
			body["provider"] = pr
		}
	}
	u, err := x.apiURL("v1", "/images")
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, u, body)
}

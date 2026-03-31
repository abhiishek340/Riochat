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

func sanitizeORProvider(p map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"only", "order", "ignore"} {
		if l, ok := p[k].([]any); ok {
			out[k] = cloneJSON(l)
		}
	}
	if b, ok := p["allow_fallbacks"].(bool); ok {
		out["allow_fallbacks"] = b
	}
	if m, ok := p["options"].(map[string]any); ok {
		out["options"] = cloneJSON(m)
	}
	switch s := p["sort"].(type) {
	case string:
		if orSortNames[s] {
			out["sort"] = s
		}
	case map[string]any:
		by := strOf(s["by"])
		if orSortNames[by] {
			sort := map[string]any{"by": by}
			if part := strOf(s["partition"]); part == "model" || part == "none" {
				sort["partition"] = part
			}
			out["sort"] = sort
		}
	}
	return out
}

func buildXAIImages(x *exchange) (*call, error) {
	o := x.opts
	prompt, err := requirePrompt(x)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": x.model, "prompt": prompt}
	if s, ok := o.str("response_format"); ok && (s == "url" || s == "b64_json") {
		body["response_format"] = s
	}
	if s, ok := o.str("aspect_ratio"); ok {
		body["aspect_ratio"] = s
	}
	if s, ok := o.str("resolution"); ok {
		body["resolution"] = s
	}
	if n, ok := o.positiveInt("n"); ok {
		body["n"] = n
	}
	path := "/images/generations"
	if x.proto == XAIImageEdits {
		path = "/images/edits"
		imgs := inputImages(x.req.Messages)
		if len(imgs) == 0 {
			return nil, invalid(ErrInvalidRequest, "image edit requires at least one input image")
		}
		if len(imgs) > xaiMaxEditImages {
			return nil, invalid(ErrInvalidRequest, "xai image edits accept at most %d input images", xaiMaxEditImages)
		}
		ref := func(p Part) map[string]any {
			return map[string]any{"url": dataURL(imageMIME(p.MIME), p.Data), "type": "image_url"}
		}
		if len(imgs) == 1 {
			body["image"] = ref(imgs[0])
		} else {
			arr := make([]any, len(imgs))
			for i, p := range imgs {
				arr[i] = ref(p)
			}
			body["images"] = arr
		}
	}
	u, err := x.apiURL("v1", path)
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, u, body)
}

// ---- parsing --------------------------------------------------------------

func (imageAdapter) parse(x *exchange, body []byte) (*Result, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err, Accepted: true}
	}
	if e, ok := m["error"].(map[string]any); ok && e != nil {
		return nil, upstreamFromObject(m, m)
	}
	return parseImagesDoc(m, x.proto)
}

func imageItemMIME(item map[string]any, outputFormat string, proto Protocol) string {
	for _, k := range []string{"media_type", "mime_type"} {
		if s := strings.ToLower(strOf(item[k])); strings.HasPrefix(s, "image/") {
			return s
		}
	}
	if proto == XAIImage || proto == XAIImageEdits {
		return "image/jpeg"
	}
	switch strings.ToLower(outputFormat) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	}
	return "image/png"
}

func parseImagesDoc(m map[string]any, proto Protocol) (*Result, error) {
	res := &Result{ID: strOf(m["id"])}
	outFmt := strOf(m["output_format"])
	for i, d := range asList(m["data"]) {
		item, _ := d.(map[string]any)
		if item == nil {
			continue
		}
		img := Image{Index: i, MIME: imageItemMIME(item, outFmt, proto), RevisedPrompt: strOf(item["revised_prompt"])}
		img.URL = strOf(item["url"])
		if fo, ok := item["file_output"].(map[string]any); ok && img.URL == "" {
			img.URL = strOf(fo["public_url"])
		}
		if b := strOf(item["b64_json"]); b != "" {
			data, err := base64.StdEncoding.DecodeString(b)
			if err != nil {
				return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid base64 image", Cause: err, Accepted: true}
			}
			img.Data = data
		}
		if len(img.Data) == 0 && img.URL == "" {
			continue
		}
		if img.URL != "" {
			res.Citations = addCitation(res.Citations, Citation{URL: img.URL})
		}
		res.Images = append(res.Images, img)
		if res.RevisedPrompt == "" {
			res.RevisedPrompt = img.RevisedPrompt
		}
	}
	usage, _ := m["usage"].(map[string]any)
	res.Usage = openAIUsage(usage, proto.isXAI())
	res.FinishReason = "stop"
	return res, nil
}

type imageStream struct {
	x       *exchange
	frames  int
	partial map[int]Image
	final   []Image
	usage   Usage
	prompt  string
	id      string
}

func (imageAdapter) stream(x *exchange) streamer {
	return &imageStream{x: x, partial: map[int]Image{}}
}

func (s *imageStream) frame(f sseFrame, emit func(Event) error) error {
	s.frames++
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	typ := strOf(m["type"])
	if typ == "" {
		typ = f.Event
	}
	if u, ok := m["usage"].(map[string]any); ok && u != nil {
		nu := openAIUsage(u, false)
		mergeUsage(&s.usage, nu)
		cp := s.usage
		defer func() { _ = emit(Event{Type: EventUsage, Usage: &cp}) }()
	}
	b := strOf(m["b64_json"])
	if b == "" && strOf(m["partial_image_b64"]) != "" {
		b = strOf(m["partial_image_b64"])
	}
	switch {
	case strings.HasSuffix(typ, "partial_image"):
		raw, err := base64.StdEncoding.DecodeString(b)
		if err != nil || len(raw) == 0 {
			return nil
		}
		idx, _ := toInt(m["partial_image_index"])
		img := Image{Index: int(idx), Data: raw, MIME: imageItemMIME(m, strOf(m["output_format"]), s.x.proto), Partial: true}
		s.partial[img.Index] = img
		return emit(Event{Type: EventImage, Index: img.Index, Image: &img})
	case strings.HasSuffix(typ, ".completed") || strings.HasSuffix(typ, ".final") || typ == "completed":
		raw, err := base64.StdEncoding.DecodeString(b)
		if err == nil && len(raw) > 0 {
			img := Image{Index: len(s.final), Data: raw, MIME: imageItemMIME(m, strOf(m["output_format"]), s.x.proto),
				RevisedPrompt: strOf(m["revised_prompt"])}
			s.final = append(s.final, img)
			cp := img
			return emit(Event{Type: EventImage, Index: img.Index, Image: &cp})
		}
		if u := strOf(m["url"]); u != "" {
			img := Image{Index: len(s.final), URL: u, MIME: imageItemMIME(m, strOf(m["output_format"]), s.x.proto)}
			s.final = append(s.final, img)
			cp := img
			return emit(Event{Type: EventImage, Index: img.Index, Image: &cp})
		}
	case typ == "response.created" || typ == "created":
		if id := strOf(m["id"]); id != "" {
			s.id = id
		}
	}
	return nil
}

func (s *imageStream) result() (*Result, error) {
	if s.frames == 0 {
		return &Result{}, &Error{Kind: KindParse, Message: "upstream stream ended before any event", Accepted: true}
	}
	res := &Result{ID: s.id, Usage: s.usage, FinishReason: "stop"}
	res.Images = s.final
	if len(res.Images) == 0 {
		// Keep the highest-index partial as the final image.
		best := -1
		for i := range s.partial {
			if i > best {
				best = i
			}
		}
		if best >= 0 {
			img := s.partial[best]
			img.Partial = false
			res.Images = []Image{img}
		}
	}
	for _, im := range res.Images {
		if im.RevisedPrompt != "" {
			res.RevisedPrompt = im.RevisedPrompt
			break
		}
	}
	return res, nil
}

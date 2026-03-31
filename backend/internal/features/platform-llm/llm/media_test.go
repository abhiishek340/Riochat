package llm

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func imgRoute(p Protocol, f *fakeUpstream, model string) Route { return routeFor(p, f, model) }

func TestOpenAIImageGenerationRequest(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"created":1,"output_format":"webp","data":[{"b64_json":"SU1H","revised_prompt":"rp"},{"url":"https://img.example/a.png","media_type":"image/jpeg"}],"usage":{"input_tokens":10,"output_tokens":20,"input_tokens_details":{"text_tokens":10}}}`))
	req := &Request{Messages: []Message{{Role: RoleUser, Content: "old"}, {Role: RoleUser, Content: "a red fox"}},
		Options: map[string]any{"quality": "high", "size": "1024x1024", "n": float64(2), "user": "u1", "background": "transparent", "moderation": "low",
			"output_format": "webp", "output_compression": float64(80), "style": "vivid", "response_format": "url", "partial_images": float64(2), "bogus": 1}}
	res, err := testClient(t).Generate(context.Background(), imgRoute(OpenAIImageGenerations, f, "gpt-image-1"), req)
	if err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Path != "/v1/images/generations" {
		t.Fatal(r.Path)
	}
	b := r.JSON(t)
	mustEq(t, b["prompt"], "a red fox", "last user message")
	mustEq(t, b["quality"], "high", "quality")
	mustEq(t, b["n"], "2", "n")
	mustEq(t, b["background"], "transparent", "gpt-image option")
	mustEq(t, b["output_compression"], "80", "compression")
	for _, k := range []string{"response_format", "style", "stream", "partial_images", "bogus"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s must not be sent for gpt-image-1 non-stream", k)
		}
	}
	if len(res.Images) != 2 || string(res.Images[0].Data) != "IMG" || res.Images[0].MIME != "image/webp" || res.Images[0].RevisedPrompt != "rp" || res.RevisedPrompt != "rp" {
		t.Fatalf("%+v", res.Images)
	}
	if res.Images[1].URL != "https://img.example/a.png" || res.Images[1].MIME != "image/jpeg" || len(res.Citations) != 1 {
		t.Fatalf("%+v", res.Images[1])
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 20 {
		t.Fatalf("%+v", res.Usage)
	}

	// dall-e-3: response_format b64_json default; style allowed; gpt-image options not.
	if _, err := testClient(t).Generate(context.Background(), imgRoute(OpenAIImageGenerations, f, "dall-e-3"), &Request{Messages: userMsg("x"),
		Options: map[string]any{"style": "vivid", "background": "transparent"}}); err != nil {
		t.Fatal(err)
	}
	b = f.last(t).JSON(t)
	mustEq(t, b["response_format"], "b64_json", "default response format")
	mustEq(t, b["style"], "vivid", "style for dall-e-3")
	if _, ok := b["background"]; ok {
		t.Fatal("background is gpt-image only")
	}
	// Prompt is required.
	if _, err := testClient(t).Generate(context.Background(), imgRoute(OpenAIImageGenerations, f, "dall-e-3"), &Request{Messages: userMsg("   ")}); err == nil {
		t.Fatal("empty prompt")
	}
}

func TestOpenAIImageEditMultipart(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"data":[{"b64_json":"SU1H"}]}`))
	req := &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{
		{Kind: PartText, Text: "make it blue"}, {Kind: PartImage, Data: []byte("ONE")}, {Kind: PartImage, MIME: "image/jpeg", FileName: "two.jpg", Data: []byte("TWO")}}}},
		Mask:    &Part{Kind: PartImage, Data: []byte("MASK")},
		Options: map[string]any{"input_fidelity": "high", "n": float64(1), "size": "auto", "output_format": "png"}}
	if _, err := testClient(t).Generate(context.Background(), imgRoute(OpenAIImageEdits, f, "gpt-image-1"), req); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("%s %v", mt, err)
	}
	mr := multipart.NewReader(strings.NewReader(string(r.Body)), params["boundary"])
	fields := map[string]string{}
	var files []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(p)
		if p.FileName() != "" {
			files = append(files, p.FormName()+":"+p.FileName()+":"+p.Header.Get("Content-Type")+":"+string(data))
		} else {
			fields[p.FormName()] = string(data)
		}
	}
	if len(files) != 3 || files[0] != "image[]:image-01.png:image/png:ONE" || files[1] != "image[]:two.jpg:image/jpeg:TWO" || files[2] != "mask:image-01.png:image/png:MASK" {
		t.Fatalf("%v", files)
	}
	if fields["model"] != "gpt-image-1" || fields["prompt"] != "make it blue" || fields["input_fidelity"] != "high" || fields["n"] != "1" || fields["output_format"] != "png" {
		t.Fatalf("%v", fields)
	}
	if r.Path != "/v1/images/edits" {
		t.Fatal(r.Path)
	}
	// No input image → error, and nothing is sent.
	n := len(f.all())
	_, err = testClient(t).Generate(context.Background(), imgRoute(OpenAIImageEdits, f, "gpt-image-1"), &Request{Messages: userMsg("edit")})
	if err == nil || !strings.Contains(err.Error(), "input image") || len(f.all()) != n {
		t.Fatalf("%v", err)
	}
	// Empty prompt → error as well.
	_, err = testClient(t).Generate(context.Background(), imgRoute(OpenAIImageEdits, f, "gpt-image-1"), &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartImage, Data: []byte("x")}}}}})
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("%v", err)
	}
	// dall-e-2 edits send response_format, not gpt-image fields.
	if _, err := testClient(t).Generate(context.Background(), imgRoute(OpenAIImageEdits, f, "dall-e-2"), &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "x"}, {Kind: PartImage, Data: []byte("x")}}}},
		Options: map[string]any{"response_format": "url", "input_fidelity": "high"}}); err != nil {
		t.Fatal(err)
	}
	if s := string(f.last(t).Body); !strings.Contains(s, "response_format") || strings.Contains(s, "input_fidelity") {
		t.Fatal("dall-e-2 edit fields")
	}
}

func TestImageStreamingPartialsAndFinal(t *testing.T) {
	f := newUpstream(t, sseReply(
		ev("image_generation.partial_image", `{"type":"image_generation.partial_image","b64_json":"UDE=","partial_image_index":0,"output_format":"png"}`),
		ev("image_generation.partial_image", `{"type":"image_generation.partial_image","b64_json":"UDI=","partial_image_index":1}`),
		ev("image_generation.completed", `{"type":"image_generation.completed","b64_json":"RklOQUw=","output_format":"jpeg","usage":{"input_tokens":5,"output_tokens":9}}`),
	))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), imgRoute(OpenAIImageGenerations, f, "gpt-image-1"), &Request{Messages: userMsg("x"), Options: map[string]any{"partial_images": float64(2)}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	b := f.last(t).JSON(t)
	mustEq(t, b["stream"], true, "stream")
	mustEq(t, b["partial_images"], "2", "partial_images")
	imgs := eventsOf(*evs, EventImage)
	if len(imgs) != 3 || !imgs[0].Image.Partial || imgs[1].Image.Index != 1 || imgs[2].Image.Partial {
		t.Fatalf("%+v", imgs)
	}
	if len(res.Images) != 1 || string(res.Images[0].Data) != "FINAL" || res.Images[0].MIME != "image/jpeg" || res.Usage.InputTokens != 5 || res.Usage.OutputTokens != 9 {
		t.Fatalf("%+v", res)
	}
	// Without a completed event the last partial is promoted.
	f2 := newUpstream(t, sseReply(ev("image_generation.partial_image", `{"type":"image_generation.partial_image","b64_json":"UDE=","partial_image_index":0}`)))
	res, err = testClient(t).Stream(context.Background(), imgRoute(OpenAIImageGenerations, f2, "gpt-image-1"), &Request{Messages: userMsg("x")}, sink)
	if err != nil || len(res.Images) != 1 || res.Images[0].Partial {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestImageStreamFallsBackToBufferedJSON(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"data":[{"b64_json":"SU1H"}],"usage":{"input_tokens":1,"output_tokens":2}}`))
	sink, evs := collect()
	res, err := testClient(t).Stream(context.Background(), imgRoute(OpenAIImageGenerations, f, "gpt-image-1"), &Request{Messages: userMsg("x")}, sink)
	if err != nil || len(res.Images) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if len(eventsOf(*evs, EventImage)) != 1 || len(eventsOf(*evs, EventUsage)) != 1 {
		t.Fatalf("an upstream that ignores streaming still yields images and exactly one usage event: %+v", *evs)
	}
	// Even without usage in the body, one usage event is emitted for images.
	f2 := newUpstream(t, jsonReply(`{"data":[{"b64_json":"SU1H"}]}`))
	sink2, evs2 := collect()
	if _, err := testClient(t).Stream(context.Background(), imgRoute(OpenAIImageGenerations, f2, "gpt-image-1"), &Request{Messages: userMsg("x")}, sink2); err != nil {
		t.Fatal(err)
	}
	if len(eventsOf(*evs2, EventUsage)) != 1 {
		t.Fatal("one usage event expected")
	}
}

func TestOpenRouterImages(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"data":[{"b64_json":"SU1H"}]}`))
	c := testClient(t)
	mk := func(refs int, opts map[string]any) *Request {
		parts := []Part{{Kind: PartText, Text: "paint"}}
		for i := 0; i < refs; i++ {
			parts = append(parts, Part{Kind: PartImage, MIME: "image/png", Data: []byte("r")})
		}
		return &Request{Messages: []Message{{Role: RoleUser, Parts: parts}}, Options: opts}
	}
	route := imgRoute(OpenRouterImages, f, "m")
	_, err := c.Generate(context.Background(), route, mk(17, nil))
	if err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("17 references must fail: %v", err)
	}
	if _, err := c.Generate(context.Background(), route, mk(16, nil)); err != nil {
		t.Fatalf("16 references are fine: %v", err)
	}
	b := f.last(t).JSON(t)
	if f.last(t).Path != "/v1/images" || len(b["input_references"].([]any)) != 16 {
		t.Fatalf("%s", f.last(t).Path)
	}
	mustEq(t, at(b, "input_references.0.type"), "image_url", "reference block")
	mustEq(t, at(b, "input_references.0.image_url.url"), "data:image/png;base64,cg==", "data url")

	run := func(opts map[string]any) map[string]any {
		if _, err := c.Generate(context.Background(), route, mk(0, opts)); err != nil {
			t.Fatal(err)
		}
		return f.last(t).JSON(t)
	}
	b = run(map[string]any{"size": "2048x2048", "output_format": "svg", "n": float64(11), "aspect_ratio": "9:19.5", "resolution": "2k", "quality": "xhigh",
		"background": "opaque", "output_compression": float64(101), "seed": float64(7), "user": "u"})
	mustEq(t, b["size"], "2048x2048", "pixel size kept")
	if _, ok := b["output_format"]; ok {
		t.Fatal("svg is dropped")
	}
	if _, ok := b["n"]; ok {
		t.Fatal("n above 10 is dropped")
	}
	mustEq(t, b["aspect_ratio"], "9:19.5", "aspect ratio")
	mustEq(t, b["resolution"], "2K", "resolution upper-cased")
	mustEq(t, b["quality"], "xhigh", "quality")
	if _, ok := b["output_compression"]; ok {
		t.Fatal("compression above 100 dropped")
	}
	mustEq(t, b["seed"], "7", "seed")
	b = run(map[string]any{"size": "huge", "n": float64(3), "aspect_ratio": "7:7", "background": "weird"})
	for _, k := range []string{"size", "aspect_ratio", "background"} {
		if _, ok := b[k]; ok {
			t.Errorf("invalid %s dropped", k)
		}
	}
	mustEq(t, b["n"], "3", "n")
	b = run(map[string]any{"size": "4K", "provider": map[string]any{"only": []any{"a"}, "order": []any{"b"}, "allow_fallbacks": false, "sort": map[string]any{"by": "price", "partition": "model"}, "data_collection": "deny"}})
	mustEq(t, b["size"], "4K", "tier size")
	mustEq(t, at(b, "provider.only.0"), "a", "provider.only")
	mustEq(t, at(b, "provider.allow_fallbacks"), false, "allow_fallbacks")
	mustEq(t, at(b, "provider.sort.partition"), "model", "sort partition")
	if at(b, "provider.data_collection") != nil {
		t.Fatal("provider is limited to the documented keys")
	}
	b = run(map[string]any{"provider": map[string]any{"sort": "latency"}})
	mustEq(t, at(b, "provider.sort"), "latency", "sort strategy")
	b = run(map[string]any{"provider": map[string]any{"sort": "chaos"}})
	if b["provider"] != nil {
		t.Fatal("unknown sort strategy dropped")
	}
	// Masks are not sent.
	req := mk(1, nil)
	req.Mask = &Part{Kind: PartImage, Data: []byte("m")}
	c.Generate(context.Background(), route, req)
	if strings.Contains(string(f.last(t).Body), "mask") {
		t.Fatal("masks are not supported")
	}
}

func TestXAIImages(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"data":[{"url":"https://x.ai/a.jpg"},{"b64_json":"SU1H","mime_type":"image/png"},{"file_output":{"public_url":"https://x.ai/b.jpg"}}],"usage":{"input_tokens":1,"output_tokens":7}}`))
	c := testClient(t)
	res, err := c.Generate(context.Background(), imgRoute(XAIImage, f, "grok-img"), &Request{Messages: userMsg("cat"), Options: map[string]any{"n": float64(3), "aspect_ratio": "1:1", "resolution": "1k", "response_format": "url"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.last(t).Path != "/v1/images/generations" {
		t.Fatal(f.last(t).Path)
	}
	b := f.last(t).JSON(t)
	mustEq(t, b["n"], "3", "n")
	mustEq(t, b["resolution"], "1k", "resolution")
	if len(res.Images) != 3 || res.Images[0].MIME != "image/jpeg" || res.Images[1].MIME != "image/png" || res.Images[2].URL != "https://x.ai/b.jpg" {
		t.Fatalf("%+v", res.Images)
	}
	if res.Usage.OutputTokens != 7 {
		t.Fatalf("%+v", res.Usage)
	}
	img := func(n int) []Part {
		out := []Part{{Kind: PartText, Text: "edit"}}
		for i := 0; i < n; i++ {
			out = append(out, Part{Kind: PartImage, MIME: "image/png", Data: []byte("i")})
		}
		return out
	}
	if _, err := c.Generate(context.Background(), imgRoute(XAIImageEdits, f, "m"), &Request{Messages: []Message{{Role: RoleUser, Parts: img(1)}}}); err != nil {
		t.Fatal(err)
	}
	b = f.last(t).JSON(t)
	if f.last(t).Path != "/v1/images/edits" {
		t.Fatal(f.last(t).Path)
	}
	mustEq(t, at(b, "image.type"), "image_url", "single image object")
	mustEq(t, at(b, "image.url"), "data:image/png;base64,aQ==", "data url")
	if _, err := c.Generate(context.Background(), imgRoute(XAIImageEdits, f, "m"), &Request{Messages: []Message{{Role: RoleUser, Parts: img(3)}}}); err != nil {
		t.Fatal(err)
	}
	mustEq(t, at(f.last(t).JSON(t), "images.2.type"), "image_url", "several images")
	if _, err := c.Generate(context.Background(), imgRoute(XAIImageEdits, f, "m"), &Request{Messages: []Message{{Role: RoleUser, Parts: img(4)}}}); err == nil {
		t.Fatal("more than three images must fail")
	}
	if _, err := c.Generate(context.Background(), imgRoute(XAIImageEdits, f, "m"), &Request{Messages: userMsg("no images")}); err == nil {
		t.Fatal("an edit needs an image")
	}
}

// ---- xAI video ------------------------------------------------------------

func videoUpstream(t *testing.T, statuses []string, retryAfter string) (*fakeUpstream, *[]time.Duration) {
	i := 0
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			io.WriteString(w, `{"request_id":"r1"}`)
			return
		}
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		s := statuses[i]
		if i < len(statuses)-1 {
			i++
		}
		io.WriteString(w, s)
	})
	var sleeps []time.Duration
	return f, &sleeps
}

func videoClient(t *testing.T, sleeps *[]time.Duration) *Client {
	return testClient(t, func(o *ClientOptions) {
		o.Sleep = func(ctx context.Context, d time.Duration) error { *sleeps = append(*sleeps, d); return ctx.Err() }
	})
}

func TestXAIVideoPollingAndResult(t *testing.T) {
	f, sleeps := videoUpstream(t, []string{
		`{"status":"pending"}`,
		`{"status":"done","video":{"url":"https://x.ai/v.mp4","respect_moderation":true,"duration":"5.3s"}}`,
	}, "3")
	c := videoClient(t, sleeps)
	req := &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "a dog surfing"}, {Kind: PartImage, MIME: "image/gif", Data: []byte("IMG")}}}},
		Options: map[string]any{"aspect_ratio": "16:9", "duration": float64(8), "resolution": "720p"}}
	res, err := c.Generate(context.Background(), routeFor(XAIVideo, f, "grok-video"), req)
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.all()
	if reqs[0].Path != "/v1/videos/generations" || reqs[1].Path != "/v1/videos/r1" || reqs[1].Method != "GET" {
		t.Fatalf("%+v", reqs)
	}
	b := reqs[0].JSON(t)
	mustEq(t, b["prompt"], "a dog surfing", "prompt")
	mustEq(t, at(b, "image.url"), "data:image/jpeg;base64,SU1H", "gif falls back to jpeg")
	mustEq(t, b["aspect_ratio"], "16:9", "aspect")
	mustEq(t, b["duration"], "8", "duration")
	mustEq(t, b["resolution"], "720p", "resolution")
	if len(res.Videos) != 1 || res.Videos[0].URL != "https://x.ai/v.mp4" || res.Videos[0].MIME != "video/mp4" || res.Videos[0].DurationSec != 6 || res.Videos[0].FileName == "" {
		t.Fatalf("%+v", res.Videos)
	}
	if len(*sleeps) != 2 || (*sleeps)[0] != time.Second || (*sleeps)[1] != 3*time.Second {
		t.Fatalf("first poll waits the default second, then Retry-After: %v", *sleeps)
	}
}

func TestXAIVideoFailureModes(t *testing.T) {
	run := func(statuses []string, ra string) (*Result, error, []time.Duration) {
		f, sleeps := videoUpstream(t, statuses, ra)
		res, err := videoClient(t, sleeps).Generate(context.Background(), routeFor(XAIVideo, f, "m"), &Request{Messages: userMsg("p")})
		return res, err, *sleeps
	}
	_, err, _ := run([]string{`{"status":"failed","error":{"code":"x","message":"model exploded"}}`}, "")
	e, ok := AsError(err)
	if !ok || e.Code != "x" || e.Message != "model exploded" || !e.Accepted {
		t.Fatalf("%+v", e)
	}
	_, err, _ = run([]string{`{"status":"done","video":{"url":"https://x/v.mp4","respect_moderation":false}}`}, "")
	e, ok = AsError(err)
	if !ok || !strings.Contains(e.Message, "moderation") || !e.Accepted {
		t.Fatalf("%v", err)
	}
	_, err, _ = run([]string{`{"status":"done","video":{}}`}, "")
	if err == nil || !IsAccepted(err) {
		t.Fatalf("a finished video needs a url: %v", err)
	}
	res, err, sl := run([]string{`{"status":"pending"}`, `{"status":"done","video":{"file_output":{"public_url":"https://x/f.mp4"}}}`}, "99")
	if err != nil || res.Videos[0].URL != "https://x/f.mp4" || sl[1] != 10*time.Second {
		t.Fatalf("Retry-After clamps to 10s: %v %v", err, sl)
	}
	_, err, sl = run([]string{`{"status":"pending"}`, `{"status":"done","video":{"url":"https://x/v.mp4"}}`}, "0")
	if err != nil || sl[1] != time.Second {
		t.Fatalf("Retry-After clamps to 1s: %v", sl)
	}
}

func TestXAIVideoValidationAndSubmission(t *testing.T) {
	f, sleeps := videoUpstream(t, []string{`{"status":"done","video":{"url":"https://x/v.mp4"}}`}, "")
	c := videoClient(t, sleeps)
	two := &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "p"}, {Kind: PartImage, Data: []byte("a")}, {Kind: PartImage, Data: []byte("b")}}}}}
	if _, err := c.Generate(context.Background(), routeFor(XAIVideo, f, "m"), two); err == nil {
		t.Fatal("two input images must fail")
	}
	if _, err := c.Generate(context.Background(), routeFor(XAIVideo, f, "m"), &Request{}); err == nil {
		t.Fatal("a prompt or an image is required")
	}
	if len(f.all()) != 0 {
		t.Fatal("validation precedes any network call")
	}
	// An image alone is enough; invalid options are dropped.
	_, err := c.Generate(context.Background(), routeFor(XAIVideo, f, "m"), &Request{Messages: []Message{{Role: RoleUser, Parts: []Part{{Kind: PartImage, MIME: "image/png", Data: []byte("a")}}}},
		Options: map[string]any{"duration": float64(16), "aspect_ratio": "5:1", "resolution": "huge"}})
	if err != nil {
		t.Fatal(err)
	}
	b := f.all()[0].JSON(t)
	for _, k := range []string{"duration", "aspect_ratio", "resolution", "prompt"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s should be absent", k)
		}
	}
	mustEq(t, at(b, "image.url"), "data:image/png;base64,YQ==", "png kept")
	// Missing request_id → accepted parse error.
	f2 := newUpstream(t, jsonReply(`{}`))
	_, err = c.Generate(context.Background(), routeFor(XAIVideo, f2, "m"), &Request{Messages: userMsg("p")})
	if err == nil || !IsAccepted(err) || !strings.Contains(err.Error(), "request_id") {
		t.Fatalf("%v", err)
	}
	// Streaming is not supported.
	sink, _ := collect()
	if _, err := c.Stream(context.Background(), routeFor(XAIVideo, f, "m"), &Request{Messages: userMsg("p")}, sink); !errors.Is(err, ErrUnsupportedStream) {
		t.Fatal(err)
	}
}

func TestXAIVideoExtensions(t *testing.T) {
	f, sleeps := videoUpstream(t, []string{`{"status":"done","video":{"url":"https://x/e.mp4"}}`}, "")
	c := videoClient(t, sleeps)
	route := routeFor(XAIVideoExtensions, f, "m")
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("continue")}); err == nil {
		t.Fatal("an MP4 source is required")
	}
	if _, err := c.Generate(context.Background(), route, &Request{VideoSource: &Part{Kind: PartVideo, MIME: "video/webm", Data: []byte("v")}, Messages: userMsg("continue")}); err == nil {
		t.Fatal("source must be MP4")
	}
	if _, err := c.Generate(context.Background(), route, &Request{VideoSource: &Part{Kind: PartVideo, MIME: "video/mp4", Data: []byte("v")}}); err == nil {
		t.Fatal("a prompt is required")
	}
	req := &Request{VideoSource: &Part{Kind: PartVideo, MIME: "video/mp4", Data: []byte("v")}, Messages: userMsg("continue"),
		Options: map[string]any{"duration": float64(6), "aspect_ratio": "16:9"}}
	if _, err := c.Generate(context.Background(), route, req); err != nil {
		t.Fatal(err)
	}
	r := f.all()[0]
	if r.Path != "/v1/videos/extensions" {
		t.Fatal(r.Path)
	}
	b := r.JSON(t)
	mustEq(t, b["duration"], "6", "duration 2-10")
	mustEq(t, at(b, "video.url"), "data:video/mp4;base64,dg==", "source")
	if _, ok := b["aspect_ratio"]; ok {
		t.Fatal("extensions accept only duration")
	}
	req.Options = map[string]any{"duration": float64(11)}
	c.Generate(context.Background(), route, req)
	if _, ok := f.all()[len(f.all())-2].JSON(t)["duration"]; ok {
		t.Fatal("duration above 10 dropped")
	}
}

func TestParseDurationSeconds(t *testing.T) {
	for in, want := range map[any]int{"5s": 5, "5.2": 6, 4.0: 4, 0.1: 1, "  7S ": 7} {
		if got, ok := parseDurationSeconds(in); !ok || got != want {
			t.Errorf("%v → %d,%v want %d", in, got, ok, want)
		}
	}
	for _, in := range []any{"abc", nil, "-3", true} {
		if _, ok := parseDurationSeconds(in); ok {
			t.Errorf("%v should not parse", in)
		}
	}
}

func TestModelListing(t *testing.T) {
	// OpenAI-compatible.
	f := newUpstream(t, jsonReply(`{"data":[{"id":"a","owned_by":"x"},{"id":"  "},{"id":"b"}]}`))
	ms, err := testClient(t).ListModels(context.Background(), routeFor(OpenAIChatCompletions, f, ""))
	if err != nil || len(ms) != 2 || ms[0].ID != "a" || ms[0].OwnedBy != "x" || ms[1].ID != "b" {
		t.Fatalf("%v %+v", err, ms)
	}
	if f.last(t).Path != "/v1/models" || f.last(t).Method != "GET" {
		t.Fatal(f.last(t).Path)
	}
	// OpenRouter images.
	ms, _ = testClient(t).ListModels(context.Background(), routeFor(OpenRouterImages, f, ""))
	if f.last(t).Path != "/v1/images/models" {
		t.Fatal(f.last(t).Path)
	}
	// Anthropic pagination.
	pages := map[string]string{
		"":   `{"data":[{"id":"c1"}],"has_more":true,"last_id":"c1"}`,
		"c1": `{"data":[{"id":"c2"}],"has_more":false}`,
	}
	fa := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		io.WriteString(w, pages[r.URL.Query().Get("after_id")])
	})
	ms, err = testClient(t).ListModels(context.Background(), routeFor(AnthropicMessages, fa, ""))
	if err != nil || len(ms) != 2 || ms[0].OwnedBy != "anthropic" {
		t.Fatalf("%v %+v", err, ms)
	}
	if q := fa.all()[0].RawQuery; q != "limit=1000" {
		t.Fatal(q)
	}
	// Missing last_id, repeated cursor → errors (and the fallback fails too).
	fb := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.URL.Query().Get("limit") == "" {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{"data":[],"has_more":true}`)
	})
	_, err = testClient(t).ListModels(context.Background(), routeFor(AnthropicMessages, fb, ""))
	if err == nil || !strings.Contains(err.Error(), "last_id") || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("%v", err)
	}
	fc := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.URL.Query().Get("limit") == "" {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, `{"data":[],"has_more":true,"last_id":"same"}`)
	})
	_, err = testClient(t).ListModels(context.Background(), routeFor(AnthropicMessages, fc, ""))
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("loop protection: %v", err)
	}
	// Google pagination with prefix stripping.
	fg := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.URL.Query().Get("pageToken") == "" {
			io.WriteString(w, `{"models":[{"name":"models/gemini-a"}],"nextPageToken":"t2"}`)
			return
		}
		io.WriteString(w, `{"models":[{"name":"models/gemini-b"}]}`)
	})
	ms, err = testClient(t).ListModels(context.Background(), routeFor(GoogleGenerateContent, fg, ""))
	if err != nil || len(ms) != 2 || ms[0].ID != "gemini-a" || ms[1].OwnedBy != "google" {
		t.Fatalf("%v %+v", err, ms)
	}
	if r := fg.all()[0]; r.Path != "/v1beta/models" || r.RawQuery != "pageSize=1000" {
		t.Fatalf("%+v", r)
	}
	// Fallback to the OpenAI-compatible listing.
	fd := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.URL.Path == "/v1/models" && r.URL.Query().Get("limit") == "" {
			io.WriteString(w, `{"data":[{"id":"proxy-model"}]}`)
			return
		}
		w.WriteHeader(404)
	})
	ms, err = testClient(t).ListModels(context.Background(), routeFor(AnthropicMessages, fd, ""))
	if err != nil || len(ms) != 1 || ms[0].ID != "proxy-model" {
		t.Fatalf("fallback: %v %+v", err, ms)
	}
	// No fallback on OpenRouter hosts.
	c := NewClient(ClientOptions{HTTP: rewriteDoer{target: fd.URL}})
	_, err = c.ListModels(context.Background(), Route{Protocol: string(AnthropicMessages), BaseURL: "https://openrouter.ai/api"})
	if err == nil {
		t.Fatal("openrouter hosts do not fall back")
	}
}

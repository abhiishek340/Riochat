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

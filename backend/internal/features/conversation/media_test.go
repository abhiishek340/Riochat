package conversation

import (
	"context"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func mediaGen(files []GeneratedFile) func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
	return func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskTitle || req.Task == taskLabels {
			return defaultGenerate(ctx, req, emit)
		}
		return GenerateResult{Content: "generated: " + req.Media.Prompt, Usage: Usage{Input: 2, Output: 3}, Files: files,
			Route: RouteInfo{PlatformModelName: "img-model"}}, nil
	}
}

func TestMediaImageGeneration(t *testing.T) {
	e := newEnv(t)
	e.cat.available["img-model"] = true
	e.gen.set(mediaGen([]GeneratedFile{{FileName: "out.png", MimeType: "image/png", Kind: "image", Data: pngBytes(40)}}))
	id := e.newConv(e.u1, "t", "img-model")
	w := e.call("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "a cat", "clientRunID": "m1"})
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "completed" {
		t.Fatalf("lines: %v", types(lines))
	}
	a := last["data"].(map[string]any)["assistantMessage"].(map[string]any)
	var snaps []map[string]any
	if err := jsonUnmarshal(str(a, "attachments"), &snaps); err != nil || len(snaps) != 1 || snaps[0]["file_category"] != "image" {
		t.Fatalf("generated attachment: %v %v", a["attachments"], err)
	}
	req := e.gen.requests()[0]
	if req.Task != taskImageGeneration || req.Media == nil || req.Media.Prompt != "a cat" {
		t.Fatalf("task: %+v", req)
	}
	// the file is stored for the user and consumes quota
	lf := e.ok("GET", "/files", e.u1, nil)
	if num(lf, "total") != 1 {
		t.Fatalf("files: %v", lf)
	}
	runs := e.call("GET", "/conversations/"+id+"/runs", e.u1, nil)
	if _, rs := results(t, runs); str(rs[0], "taskType") != "image_generation" {
		t.Fatalf("run task: %v", rs)
	}
}

func TestMediaVideoGenerationRecordsDuration(t *testing.T) {
	e := newEnv(t)
	e.gen.set(mediaGen([]GeneratedFile{{FileName: "clip.mp4", MimeType: "video/mp4", Data: []byte("\x00\x00\x00\x18ftypmp42video-bytes"), DurationSeconds: 4.5}}))
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/media/videos/generations/stream", e.u1, map[string]any{"prompt": "waves"})
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "completed" {
		t.Fatalf("lines: %v", lines)
	}
	a := last["data"].(map[string]any)["assistantMessage"].(map[string]any)
	var snaps []map[string]any
	_ = jsonUnmarshal(str(a, "attachments"), &snaps)
	if len(snaps) != 1 || snaps[0]["duration_seconds"] != 4.5 {
		t.Fatalf("duration: %v", a["attachments"])
	}
}

func TestMediaValidation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	img := e.uploadOK(e.u1, "in.png", "image/png", pngBytes(20))["file"].(map[string]any)
	txt := e.uploadOK(e.u1, "in.txt", "text/plain", []byte("text"))["file"].(map[string]any)
	base := "/conversations/" + id + "/media/"
	cases := []struct {
		path   string
		body   map[string]any
		status int
		code   string
	}{
		{"images/generations/stream", map[string]any{}, 400, "request.invalid_body"},
		{"images/generations/stream", map[string]any{"prompt": "   "}, 400, "media.image_prompt_required"},
		{"images/generations/stream", map[string]any{"prompt": "x", "fileIDs": []string{str(img, "fileID")}}, 400, "media.image_generation_rejects_inputs"},
		{"images/edits/stream", map[string]any{"prompt": "x"}, 400, "media.image_edit_input_required"},
		{"images/edits/stream", map[string]any{"prompt": " "}, 400, "media.image_prompt_required"},
		{"images/edits/stream", map[string]any{"prompt": "x", "fileIDs": []string{str(txt, "fileID")}}, 400, "media.image_edit_input_invalid"},
		{"images/edits/stream", map[string]any{"prompt": "x", "fileIDs": []string{str(img, "fileID")}, "maskFileID": str(txt, "fileID")}, 400, "media.image_edit_input_invalid"},
		{"images/edits/stream", map[string]any{"prompt": "x", "fileIDs": []string{"file_missing"}}, 404, "file.not_found"},
		{"images/edits/stream", map[string]any{"prompt": "x", "fileIDs": make([]string, 21)}, 400, "request.invalid_body"},
		{"videos/generations/stream", map[string]any{}, 400, "request.invalid_body"},
		{"videos/generations/stream", map[string]any{"prompt": "x", "fileIDs": []string{str(img, "fileID"), str(img, "fileID")}}, 400, "media.video_too_many_inputs"},
		{"videos/generations/stream", map[string]any{"prompt": "x", "fileIDs": []string{str(txt, "fileID")}}, 400, "media.video_input_invalid"},
		{"videos/extensions/stream", map[string]any{"prompt": "x"}, 400, "request.invalid_body"},
		{"videos/extensions/stream", map[string]any{"prompt": "x", "sourceVideoFileID": str(txt, "fileID")}, 400, "media.video_input_invalid"},
		{"videos/extensions/stream", map[string]any{"prompt": "x", "sourceVideoFileID": strings200()}, 400, "request.invalid_body"},
	}
	for _, c := range cases {
		w := e.call("POST", base+c.path, e.u1, c.body)
		testkit.Expect(t, w, c.status, c.code)
	}
	// ownership
	e.expectErr("POST", "/conversations/nope/media/images/generations/stream", e.u1, map[string]any{"prompt": "x"}, 404, "conversation.not_found")
	e.expectErr("POST", base+"images/generations/stream", e.u2, map[string]any{"prompt": "x"}, 404, "conversation.not_found")
	// no run, no billing reservation consumed by invalid requests
	var n int64
	e.DB.Model(&Run{}).Count(&n)
	if n != 0 || e.bill.reserved != 0 {
		t.Fatalf("validation failures create nothing: runs=%d reserved=%d", n, e.bill.reserved)
	}
}

func strings200() string {
	b := make([]byte, 129)
	for i := range b {
		b[i] = 'v'
	}
	return string(b)
}

func TestMediaEditUsesInputFiles(t *testing.T) {
	e := newEnv(t)
	e.gen.set(mediaGen([]GeneratedFile{{FileName: "edited.png", MimeType: "image/png", Data: pngBytes(60)}}))
	img := e.uploadOK(e.u1, "in.png", "image/png", pngBytes(20))["file"].(map[string]any)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/media/images/edits/stream", e.u1, map[string]any{"prompt": "make it blue", "fileIDs": []string{str(img, "fileID")}})
	lines := ndjson(t, w)
	if str(lines[len(lines)-1], "type") != "completed" {
		t.Fatalf("edit: %v", lines)
	}
	req := e.gen.requests()[0]
	if req.Task != taskImageEdit || len(req.Media.InputFileIDs) != 1 || req.Media.InputFileIDs[0] != str(img, "fileID") {
		t.Fatalf("media request: %+v", req.Media)
	}
	// the input image is attached to the user message
	rows := e.list(id)
	var snaps []map[string]any
	_ = jsonUnmarshal(str(rows[0], "attachments"), &snaps)
	if len(snaps) != 1 {
		t.Fatalf("input attachment: %v", rows[0]["attachments"])
	}
}

func TestMediaStorageQuotaChecked(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.DB.Create(&StorageQuota{UserID: 1, QuotaBytes: 100, UsedBytes: 100})
	e.expectErr("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "x"}, 409, "quota.exceeded")
	if e.bill.reserved != 0 || len(e.gen.requests()) != 0 {
		t.Fatal("quota failure must precede budget reservation")
	}
}

func TestMediaErrorsAndCancel(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, ErrMediaStreamConfig
	})
	w := e.call("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "x"})
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || str(last, "errorCode") != "media.image_stream_unsupported" || num(last, "status") != 502 {
		t.Fatalf("stream config error: %v", last)
	}
	e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, ErrMediaRouteProtocol
	})
	lines = ndjson(t, e.call("POST", "/conversations/"+id+"/media/videos/generations/stream", e.u1, map[string]any{"prompt": "x"}))
	if last = lines[len(lines)-1]; str(last, "errorCode") != "media.route_protocol_mismatch" || num(last, "status") != 503 {
		t.Fatalf("protocol: %v", last)
	}
	// budget rejection is an HTTP error
	e.bill.reserveE = ErrInsufficientFunds
	e.expectErr("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "x"}, 402, "billing.insufficient_funds")
	e.bill.reserveE = nil
	// duplicate run id
	e.gen.set(nil)
	e.ok("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"clientRunID": "dup"}))
	e.expectErr("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "x", "clientRunID": "dup"}, 409, "message_generation_run.already_exists")
}

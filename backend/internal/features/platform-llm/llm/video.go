package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// videoAdapter serves xai_video and xai_video_extensions.
type videoAdapter struct{}

var (
	xaiVideoRatios = map[string]bool{"1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true, "3:2": true, "2:3": true}
	videoResRe     = regexp.MustCompile(`^(480|720|1080)p$`)
)

const (
	maxVideoPolls  = 1800
	minPollDelay   = time.Second
	maxPollDelay   = 10 * time.Second
	maxXAIVideoDur = 15
)

func (videoAdapter) build(x *exchange) (*call, error) {
	o := x.opts
	if x.model == "" {
		return nil, invalid(ErrInvalidRequest, "model is required")
	}
	prompt := imagePrompt(x.req.Messages)
	imgs := inputImages(x.req.Messages)
	body := map[string]any{"model": x.model}
	if prompt != "" {
		body["prompt"] = prompt
	}
	path := "/videos/generations"
	if x.proto == XAIVideoExtensions {
		path = "/videos/extensions"
		src := x.req.VideoSource
		if src == nil || len(src.Data) == 0 {
			return nil, invalid(ErrInvalidRequest, "video extension requires an MP4 source video")
		}
		if m := strings.ToLower(strings.TrimSpace(src.MIME)); m != "" && m != "video/mp4" {
			return nil, invalid(ErrInvalidRequest, "video extension requires an MP4 source video")
		}
		if prompt == "" {
			return nil, invalid(ErrInvalidRequest, "video extension requires a prompt")
		}
		body["video"] = map[string]any{"url": dataURL("video/mp4", src.Data)}
		if n, ok := o.int("duration"); ok && n >= 2 && n <= 10 {
			body["duration"] = n
		}
	} else {
		if len(imgs) > 1 {
			return nil, invalid(ErrInvalidRequest, "video generation accepts at most one input image")
		}
		if prompt == "" && len(imgs) == 0 {
			return nil, invalid(ErrInvalidRequest, "video generation requires a prompt or an input image")
		}
		if len(imgs) == 1 {
			mime := strings.ToLower(strings.TrimSpace(imgs[0].MIME))
			switch mime {
			case "image/png", "image/webp", "image/jpeg":
			default:
				mime = "image/jpeg"
			}
			body["image"] = map[string]any{"url": dataURL(mime, imgs[0].Data)}
		}
		if s, ok := o.str("aspect_ratio"); ok && xaiVideoRatios[s] {
			body["aspect_ratio"] = s
		}
		if n, ok := o.int("duration"); ok && n >= 1 && n <= maxXAIVideoDur {
			body["duration"] = n
		}
		if s, ok := o.str("resolution"); ok && videoResRe.MatchString(strings.ToLower(s)) {
			body["resolution"] = strings.ToLower(s)
		}
	}
	u, err := x.apiURL("v1", path)
	if err != nil {
		return nil, err
	}
	return jsonCall(http.MethodPost, u, body)
}

func (videoAdapter) parse(*exchange, []byte) (*Result, error) {
	return nil, invalid(ErrUnsupportedStream, "video protocols are driven by generate")
}

func (videoAdapter) stream(*exchange) streamer { return nil }

// parseDurationSeconds reads a duration in seconds from a number or text such
// as "5s" and rounds up to whole seconds.
func parseDurationSeconds(v any) (int, bool) {
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(x)), "s"))
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 {
			return 0, false
		}
		return int(math.Ceil(f)), true
	case nil:
		return 0, false
	}
	if f, ok := toFloat(v); ok && f >= 0 {
		return int(math.Ceil(f)), true
	}
	return 0, false
}

func retryAfterDelay(h http.Header) time.Duration {
	d := minPollDelay
	if s := strings.TrimSpace(h.Get("Retry-After")); s != "" {
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			d = time.Duration(n * float64(time.Second))
		}
	}
	if d < minPollDelay {
		d = minPollDelay
	}
	if d > maxPollDelay {
		d = maxPollDelay
	}
	return d
}

// generate submits the job and polls until it finishes. Every failure after a
// successful submission is marked accepted so routing does not re-submit.
func (a videoAdapter) generate(ctx context.Context, x *exchange, sink Sink) (*Result, error) {
	cl, err := a.build(x)
	if err != nil {
		return nil, err
	}
	_, body, hdr, dbg, err := x.c.doBuffered(ctx, x, cl, true)
	if err != nil {
		return nil, err
	}
	var sub map[string]any
	if json.Unmarshal(body, &sub) != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Accepted: true, Debug: dbg}
	}
	rid := strOf(sub["request_id"])
	if rid == "" {
		return nil, &Error{Kind: KindParse, Message: "video submission did not return a request_id", Accepted: true, Debug: dbg}
	}
	requested := 0
	var reqBody map[string]any
	if json.Unmarshal(cl.body, &reqBody) == nil {
		if n, ok := toInt(reqBody["duration"]); ok {
			requested = int(n)
		}
	}
	pollURL, err := x.apiURL("v1", "/videos/"+url.PathEscape(rid))
	if err != nil {
		return nil, err
	}
	delay := retryAfterDelay(hdr)
	for i := 0; i < maxVideoPolls; i++ {
		if err := x.c.sleep(ctx, delay); err != nil {
			e := transportErr(err, true)
			e.Debug = dbg
			return nil, e
		}
		_, pb, ph, pdbg, err := x.c.doBuffered(ctx, x, &call{method: http.MethodGet, url: pollURL}, false)
		if err != nil {
			if e, ok := AsError(err); ok {
				e.Accepted = true
			}
			return nil, err
		}
		dbg = pdbg
		delay = retryAfterDelay(ph)
		var st map[string]any
		if json.Unmarshal(pb, &st) != nil {
			return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Accepted: true, Debug: dbg}
		}
		switch status := strings.ToLower(strOf(st["status"])); status {
		case "failed", "error", "expired":
			e, _ := st["error"].(map[string]any)
			msg := firstNonEmpty(strOf(e["message"]), strOf(st["message"]), "video generation "+status)
			code := firstNonEmpty(strOf(e["code"]), strOf(st["code"]), status)
			return nil, &Error{Kind: KindUpstream, Status: http.StatusBadGateway, Code: code, Message: msg, Accepted: true, Debug: dbg}
		case "done", "completed", "succeeded":
			return finishVideo(st, rid, requested, dbg)
		}
	}
	return nil, &Error{Kind: KindUpstream, Status: http.StatusGatewayTimeout, Message: "video generation did not finish in time", Accepted: true, Debug: dbg}
}

func finishVideo(st map[string]any, rid string, requested int, dbg *Debug) (*Result, error) {
	vid, _ := st["video"].(map[string]any)
	if rm, ok := vid["respect_moderation"].(bool); ok && !rm {
		return nil, &Error{Kind: KindUpstream, Status: http.StatusBadRequest, Code: "content_moderation",
			Message: "the generated video was blocked by content moderation", Accepted: true, Debug: dbg}
	}
	u := strOf(vid["url"])
	if u == "" {
		if fo, ok := vid["file_output"].(map[string]any); ok {
			u = strOf(fo["public_url"])
		}
	}
	if u == "" {
		return nil, &Error{Kind: KindParse, Message: "finished video has no downloadable url", Accepted: true, Debug: dbg}
	}
	dur := requested
	for _, src := range []map[string]any{vid, st} {
		for _, k := range []string{"duration", "duration_seconds", "duration_secs"} {
			if n, ok := parseDurationSeconds(src[k]); ok && n > 0 {
				dur = n
				goto done
			}
		}
	}
done:
	res := &Result{ID: rid, FinishReason: "stop", Debug: dbg}
	res.Videos = []Video{{URL: u, MIME: "video/mp4", FileName: fmt.Sprintf("video-%s.mp4", sanitizeID(rid)), DurationSec: dur}}
	if usage, ok := st["usage"].(map[string]any); ok {
		res.Usage = openAIUsage(usage, true)
	}
	return res, nil
}

package integration

import (
	"context"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/mediadl"
)

const (
	maxImageDownload = 64 << 20
	maxVideoDownload = 512 << 20
)

// generateMedia runs an image or video task and returns the produced files.
func (g *generator) generateMedia(ctx context.Context, req conversation.GenerateRequest) (conversation.GenerateResult, error) {
	client, err := llmClient()
	if err != nil {
		return conversation.GenerateResult{}, conversation.ErrModelRouteMissing.WithCause(err)
	}
	if req.Media == nil {
		return conversation.GenerateResult{}, conversation.ErrMediaInvalidTask
	}
	started := time.Now()

	msg := llm.Message{Role: llm.RoleUser, Content: req.Media.Prompt}
	inputImages := 0
	for _, id := range req.Media.InputFileIDs {
		data, mt, lerr := g.loadFile(ctx, req.UserID, id)
		if lerr != nil {
			return conversation.GenerateResult{}, conversation.ErrMediaEditInvalid.WithCause(lerr)
		}
		msg.Parts = append(msg.Parts, llm.Part{Kind: llm.PartImage, MIME: mt, Data: data})
		inputImages++
	}
	request := &llm.Request{
		RequestID: req.RequestID, ConversationID: req.ConversationPublicID,
		Messages: []llm.Message{msg}, Options: map[string]any{}, Ephemeral: req.Temporary,
	}
	mergeInto(request.Options, req.Options)
	if req.Media.MaskFileID != "" {
		data, mt, lerr := g.loadFile(ctx, req.UserID, req.Media.MaskFileID)
		if lerr != nil {
			return conversation.GenerateResult{}, conversation.ErrMediaEditInvalid.WithCause(lerr)
		}
		request.Mask = &llm.Part{Kind: llm.PartImage, MIME: mt, Data: data}
	}
	if req.Media.SourceVideoFileID != "" {
		data, mt, lerr := g.loadFile(ctx, req.UserID, req.Media.SourceVideoFileID)
		if lerr != nil {
			return conversation.GenerateResult{}, conversation.ErrMediaVideoInput.WithCause(lerr)
		}
		request.VideoSource = &llm.Part{Kind: llm.PartVideo, MIME: mt, Data: data}
	}

	var exclude []uint
	var lastErr error
	for attempt := 0; attempt < maxRouteAttempts; attempt++ {
		route, rerr := g.route(ctx, req, exclude)
		if rerr != nil {
			if lastErr != nil {
				return conversation.GenerateResult{}, mapError(lastErr)
			}
			return conversation.GenerateResult{}, mapError(rerr)
		}
		res, gerr := client.Generate(ctx, toLLMRoute(route), request)
		if gerr != nil {
			if !route.External && route.RouteID != 0 && channel.ShouldFailOver(gerr) && ctx.Err() == nil {
				g.h.channel.MarkFailure(ctx, route, gerr)
				exclude = append(exclude, route.RouteID)
				lastErr = gerr
				continue
			}
			return conversation.GenerateResult{Route: routeInfo(route, req.Model)}, mapError(gerr)
		}
		out := conversation.GenerateResult{
			Content: res.RevisedPrompt, Route: routeInfo(route, req.Model),
			Usage: conversation.Usage{
				Input: res.Usage.InputTokens, Output: res.Usage.OutputTokens, CacheRead: res.Usage.CacheReadTokens,
				CacheWrite: res.Usage.CacheWriteTokens, Reasoning: res.Usage.ReasoningTokens,
			},
			TotalLatencyMS: time.Since(started).Milliseconds(), Billable: true,
		}
		ex := &runExtras{InputImages: inputImages, Speed: res.Usage.Speed, ServiceTier: res.ServiceTier}
		files, seconds, ferr := g.collectMedia(ctx, route, res, req.Media.Kind)
		if ferr != nil {
			g.h.putExtras(req.RunID, ex)
			return out, conversation.ErrMediaArtifact.WithCause(ferr)
		}
		out.Files = files
		ex.DurationSeconds = seconds
		ex.MediaType = mediaKind(req.Task)
		g.h.putExtras(req.RunID, ex)
		g.h.channel.MarkSuccess(ctx, route)
		return out, nil
	}
	return conversation.GenerateResult{}, mapError(lastErr)
}

func mediaKind(task string) string {
	if strings.HasPrefix(task, "video") {
		return "video"
	}
	return "image"
}

func (g *generator) loadFile(ctx context.Context, userID uint, fileID string) ([]byte, string, error) {
	if g.h.files == nil {
		return nil, "", errFilesUnavailable
	}
	f, err := g.h.files.Open(ctx, userID, fileID)
	if err != nil {
		return nil, "", err
	}
	defer f.Body.Close()
	data, err := io.ReadAll(f.Body)
	return data, f.MimeType, err
}

// collectMedia gathers the images and videos of a result as files, fetching
// artifacts that the provider returned by URL.
func (g *generator) collectMedia(ctx context.Context, route *channel.Route, res *llm.Result, kind string) ([]conversation.GeneratedFile, int64, error) {
	var files []conversation.GeneratedFile
	var seconds int64
	plat, _ := platformllm.Current()
	download := func(url string, k mediadl.Kind, max int64) (*mediadl.Media, error) {
		if plat == nil || plat.Media == nil {
			return nil, fmt.Errorf("media downloader unavailable")
		}
		return plat.Media.Download(ctx, mediadl.Request{
			URL: url, Kind: k, MaxBytes: max, EndpointURL: route.BaseURL, APIKey: route.APIKey,
			Untrusted: route.UntrustedEndpoint, GoogleKey: strings.HasPrefix(route.Protocol, "google") || strings.HasPrefix(route.Protocol, "gemini"),
		})
	}
	n := 0
	for _, img := range res.Images {
		if img.Partial {
			continue
		}
		data, mt := img.Data, img.MIME
		if len(data) == 0 && img.URL != "" {
			m, err := download(img.URL, mediadl.Image, maxImageDownload)
			if err != nil {
				return nil, 0, err
			}
			data, mt = m.Data, firstNonEmpty(mt, m.MIME)
		}
		if len(data) == 0 {
			continue
		}
		mt = firstNonEmpty(mt, "image/png")
		n++
		files = append(files, conversation.GeneratedFile{FileName: fmt.Sprintf("image-%d%s", n, extFor(mt, ".png")), MimeType: mt, Kind: "image", Data: data})
	}
	for _, vid := range res.Videos {
		data, mt := vid.Data, vid.MIME
		if len(data) == 0 && vid.URL != "" {
			m, err := download(vid.URL, mediadl.Video, maxVideoDownload)
			if err != nil {
				return nil, 0, err
			}
			data, mt = m.Data, firstNonEmpty(mt, m.MIME)
		}
		if len(data) == 0 {
			continue
		}
		mt = firstNonEmpty(mt, "video/mp4")
		n++
		name := vid.FileName
		if name == "" {
			name = fmt.Sprintf("video-%d%s", n, extFor(mt, ".mp4"))
		}
		files = append(files, conversation.GeneratedFile{FileName: name, MimeType: mt, Kind: "video", Data: data, DurationSeconds: float64(vid.DurationSec)})
		seconds += int64(vid.DurationSec)
	}
	if len(files) == 0 {
		return nil, 0, fmt.Errorf("the model returned no %s", kind)
	}
	return files, seconds, nil
}

func extFor(mimeType, def string) string {
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

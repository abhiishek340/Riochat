package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/personalprovider"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/user"
)

// llmClient returns the shared model-provider client once the platform layer is
// mounted.
func llmClient() (*llm.Client, error) {
	p, ok := platformllm.Current()
	if !ok || p.LLM == nil {
		return nil, errors.New("integration: model transport is not available")
	}
	return p.LLM, nil
}

func llmRoute(call channel.UpstreamCall, model string) llm.Route {
	return llm.Route{
		Protocol: call.Protocol, BaseURL: call.BaseURL, APIKey: call.APIKey, HeadersJSON: call.HeadersJSON,
		ConnectTimeoutMS: int(call.ConnectTimeout / time.Millisecond), ReadTimeoutMS: int(call.ReadTimeout / time.Millisecond),
		OpenRouterReferer: call.WebBaseURL, OpenRouterTitle: call.AppName, Model: model,
	}
}

// modelClient adapts the provider transport to the channel area's discovery
// and probe port.
type modelClient struct{}

func (modelClient) ListModels(ctx context.Context, call channel.UpstreamCall) ([]channel.RemoteModel, error) {
	c, err := llmClient()
	if err != nil {
		return nil, err
	}
	models, err := c.ListModels(ctx, llmRoute(call, ""))
	if err != nil {
		return nil, toUpstreamError(err)
	}
	out := make([]channel.RemoteModel, 0, len(models))
	for _, m := range models {
		out = append(out, channel.RemoteModel{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return out, nil
}

func (modelClient) Probe(ctx context.Context, call channel.UpstreamCall, req channel.ProbeRequest) (*channel.ProbeExchange, error) {
	c, err := llmClient()
	if err != nil {
		return nil, err
	}
	r := &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: req.Prompt}},
		Options:  map[string]any{},
	}
	if req.MaxTokens > 0 {
		r.Options["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		r.Options["temperature"] = req.Temperature
	}
	res, err := c.Generate(ctx, llmRoute(call, req.UpstreamModel), r)
	if err != nil {
		var le *llm.Error
		if errors.As(err, &le) && le.Kind == llm.KindParse {
			return &channel.ProbeExchange{Incompatible: true, Debug: toProbeDebug(le.Debug)}, nil
		}
		return nil, toUpstreamError(err)
	}
	return &channel.ProbeExchange{Debug: toProbeDebug(res.Debug)}, nil
}

func toProbeDebug(d *llm.Debug) *channel.ProbeDebug {
	if d == nil {
		return nil
	}
	return &channel.ProbeDebug{
		Request:  channel.ProbeDebugRequest{Method: d.Method, Path: d.Path, Headers: d.RequestHeaders, Body: d.RequestBody},
		Response: channel.ProbeDebugResponse{StatusCode: d.Status, Headers: d.ResponseHeaders, Body: d.ResponseBody},
	}
}

func toUpstreamError(err error) error {
	var le *llm.Error
	if !errors.As(err, &le) {
		return err
	}
	return &channel.UpstreamError{StatusCode: le.Status, Header: http.Header{}, Err: err, Accepted: le.Accepted, Debug: toProbeDebug(le.Debug)}
}

// ---- personal models in the catalog ----

type personalModels struct{ h *Hub }

func (p personalModels) PersonalModels(ctx context.Context, userID uint) ([]channel.PublicModel, error) {
	if p.h.personal == nil || p.h.channel == nil {
		return nil, nil
	}
	list, err := p.h.personal.ActiveModels(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]channel.PublicModel, 0, len(list))
	for _, m := range list {
		out = append(out, p.h.channel.BuildExternalModel(channel.ExternalModelSpec{
			Provider: channel.ExternalProvider{
				Name: m.ProviderName, Icon: m.ProviderIcon, Host: m.ProviderHost, Protocol: m.ProviderProtocol,
			},
			Model: m.UpstreamName, Reference: m.Reference, Protocols: m.Protocols,
		}))
	}
	return out, nil
}

// ---- user counts ----

type userCounter struct{ db *gorm.DB }

func (u userCounter) CountAllUsers(ctx context.Context) (int64, error) {
	var n int64
	err := u.db.WithContext(ctx).Model(&user.Account{}).Count(&n).Error
	return n, err
}

// ---- conversation: model availability ----

type convCatalog struct{ h *Hub }

func (c convCatalog) ChatModelAvailable(ctx context.Context, userID uint, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if c.h.channel == nil {
		return true
	}
	list, err := c.h.channel.UserModels(ctx, userID)
	if err != nil {
		return false
	}
	for _, m := range list {
		if m.PlatformModelName != model {
			continue
		}
		return containsKind(m.KindsJSON, "chat")
	}
	return false
}

// ModelUnpriced says whether a model is hidden from the user only because the
// billing mode requires a price it lacks.
func (c convCatalog) ModelUnpriced(ctx context.Context, userID uint, model string) bool {
	if c.h.channel == nil {
		return false
	}
	ok, err := c.h.channel.UnpricedForUser(ctx, userID, strings.TrimSpace(model))
	return err == nil && ok
}

// ModelRoutable says whether the model exists and is switched on.
func (c convCatalog) ModelRoutable(ctx context.Context, model string) bool {
	if c.h.channel == nil {
		return true
	}
	state, err := c.h.channel.ModelRouteState(ctx, model)
	return err != nil || state == "present"
}

func containsKind(kindsJSON, kind string) bool {
	return strings.Contains(kindsJSON, `"`+kind+`"`)
}

var _ = conversation.DefaultRuntimeSettings
var _ = personalprovider.FormatReference

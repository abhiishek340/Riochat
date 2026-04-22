package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/mcp"
	"github.com/riochat/riochat/backend/internal/features/memory"
	"github.com/riochat/riochat/backend/internal/features/personalprovider"
	platformllm "github.com/riochat/riochat/backend/internal/features/platform-llm"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	maxRouteAttempts = 3
	// fallbackTokenEstimate approximates tokens as characters divided by four.
	charsPerToken = 4
)

// generator is the conversation area's model layer: it picks a route through
// the channel area, speaks to the upstream through the provider transport,
// runs the MCP tool loop and reports usage back for billing.
type generator struct {
	h   *Hub
	log *logx.Logger
}

func newGenerator(h *Hub, log *logx.Logger) *generator { return &generator{h: h, log: log} }

// Generate implements conversation.Generator.
func (g *generator) Generate(ctx context.Context, req conversation.GenerateRequest, emit func(conversation.GenerateEvent)) (conversation.GenerateResult, error) {
	if _, ok := platformllm.Current(); !ok || g.h.channel == nil {
		return conversation.GenerateResult{}, conversation.ErrModelRouteMissing
	}
	if emit == nil {
		emit = func(conversation.GenerateEvent) {}
	}
	switch req.Task {
	case "image_generation", "image_edit", "video_generation", "video_extension":
		return g.generateMedia(ctx, req)
	}
	return g.generateChat(ctx, req, emit)
}

// ---- routing ----

// taskKind maps the conversation task names to the channel's task names.
func taskKind(task string) string {
	switch task {
	case "image_generation":
		return channel.TaskImageGeneration
	case "image_edit":
		return channel.TaskImageEdit
	case "video_generation":
		return channel.TaskVideoGeneration
	case "video_extension":
		return channel.TaskVideoExtension
	}
	return channel.TaskChat
}

// route resolves one route for the request, skipping the ones in exclude.
func (g *generator) route(ctx context.Context, req conversation.GenerateRequest, exclude []uint) (*channel.Route, error) {
	task := taskKind(req.Task)
	if _, _, ok := personalprovider.ParseReference(req.Model); ok {
		if g.h.personal == nil {
			return nil, conversation.ErrModelRouteMissing
		}
		pr, err := g.h.personal.Resolve(ctx, personalprovider.ResolveRequest{
			UserID: req.UserID, Reference: req.Model, Task: task, ExcludedRoutes: len(exclude) > 0,
		})
		if err != nil {
			return nil, err
		}
		return g.h.channel.BuildExternalRoute(ctx, channel.ExternalRouteSpec{
			Provider: channel.ExternalProvider{
				Name: pr.ProviderName, Icon: pr.ProviderIcon, Host: pr.Host, Protocol: pr.ProviderProtocol,
				BaseURL: pr.BaseURL, APIKey: pr.APIKey,
			},
			Model: pr.UpstreamModel, Reference: pr.Reference, Protocols: pr.Protocols, Task: task,
		})
	}
	scope := channel.ScopeUser
	if req.Task == "title" || req.Task == "title_labels" {
		scope = channel.ScopeInternal
	}
	return g.h.channel.ResolveRoute(ctx, channel.RouteRequest{
		Model: req.Model, Task: task, Scope: scope, UserID: req.UserID, RequestID: req.RequestID, Exclude: exclude,
	})
}

func toLLMRoute(r *channel.Route) llm.Route {
	return llm.Route{
		Protocol: r.Protocol, BaseURL: r.BaseURL, APIKey: r.APIKey, HeadersJSON: r.HeadersJSON,
		ConnectTimeoutMS: int(r.ConnectTimeout / time.Millisecond), ReadTimeoutMS: int(r.ReadTimeout / time.Millisecond),
		StreamIdleTimeoutMS: int(r.StreamIdleTimeout / time.Millisecond), Model: r.UpstreamModelName,
		Untrusted: r.UntrustedEndpoint,
	}
}

// endpointLabel names the API surface a run used. The upstream base URL is
// never recorded because runs are visible to the users who made them.
func endpointLabel(protocol string) string {
	switch protocol {
	case "openai_chat_completions":
		return "chat_completions"
	case "openai_responses":
		return "responses"
	case "anthropic_messages":
		return "messages"
	}
	return protocol
}

func routeInfo(r *channel.Route, requested string) conversation.RouteInfo {
	return conversation.RouteInfo{
		Provider: r.Vendor, ProviderProtocol: r.Protocol, Endpoint: endpointLabel(r.Protocol), UpstreamID: r.UpstreamID,
		UpstreamModelID: r.UpstreamModelID, UpstreamName: r.UpstreamName, RequestedModelName: requested,
		PlatformModelName: r.PlatformModelName, RoutedBindingCode: r.BindingCode, ModelVendor: r.Vendor,
		ModelIcon: r.Icon, UpstreamModelName: r.UpstreamModelName,
	}
}

// failureDetail carries the operator-facing wording of an upstream failure
// next to the typed public error: the text stored on the failed messages.
type failureDetail struct {
	detail string
	broken bool
	cause  error
}

func (f *failureDetail) Error() string         { return f.cause.Error() }
func (f *failureDetail) Unwrap() error         { return f.cause }
func (f *failureDetail) FailureDetail() string { return f.detail }
func (f *failureDetail) StreamBroken() bool    { return f.broken }

// describeFailure words an upstream failure the way it is stored on messages.
func describeFailure(le *llm.Error) (string, bool) {
	text := strings.TrimSpace(le.Message)
	if text == "" && le.Cause != nil {
		text = le.Cause.Error()
	}
	switch le.Kind {
	case llm.KindUpstream:
		if le.Status > 0 {
			return fmt.Sprintf("Model call failed (HTTP %d)\nReason: %s", le.Status, text), false
		}
		return "Model call failed\nReason: " + text, false
	case llm.KindParse:
		return "The model response could not be parsed (HTTP 200)\nReason: " + text, true
	case llm.KindTransport:
		if le.Accepted {
			return "The model response could not be parsed (HTTP 200)\nReason: " + text, true
		}
	}
	return "Model call failed\nReason: " + text, false
}

// mapError turns transport and routing failures into the typed errors the
// conversation area reports to clients.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if _, typed := apperr.Find(err); typed {
		return err
	}
	var le *llm.Error
	if errors.As(err, &le) {
		detail, broken := describeFailure(le)
		cause := &failureDetail{detail: detail, broken: broken, cause: err}
		if le.Status == 429 {
			return conversation.ErrUpstreamRateLimited.WithCause(cause)
		}
		return conversation.ErrUpstream.WithCause(cause)
	}
	return conversation.ErrUpstream.WithCause(err)
}

// ---- chat ----

func (g *generator) generateChat(ctx context.Context, req conversation.GenerateRequest, emit func(conversation.GenerateEvent)) (conversation.GenerateResult, error) {
	client, err := llmClient()
	if err != nil {
		return conversation.GenerateResult{}, conversation.ErrModelRouteMissing.WithCause(err)
	}
	started := time.Now()

	var tools *mcp.RunTools
	if len(req.ToolIDs) > 0 && g.h.mcp != nil {
		if tools, err = g.h.mcp.ResolveRunTools(ctx, req.ToolIDs); err != nil {
			return conversation.GenerateResult{}, err
		}
	}
	prompt := g.buildPrompt(ctx, req, tools)

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
		run := &chatRun{g: g, client: client, req: req, route: route, tools: tools, prompt: prompt, emit: emit, started: started}
		res, err := run.execute(ctx)
		if err == nil {
			g.h.channel.MarkSuccess(ctx, route)
			return res, nil
		}
		if run.delivered == 0 && !route.External && route.RouteID != 0 && channel.ShouldFailOver(err) && ctx.Err() == nil {
			g.h.channel.MarkFailure(ctx, route, err)
			exclude = append(exclude, route.RouteID)
			lastErr = err
			continue
		}
		return res, mapError(err)
	}
	return conversation.GenerateResult{}, mapError(lastErr)
}

// promptParts is everything that precedes the conversation itself.
type promptParts struct {
	instructions string
	memoryBlock  string
	skillCount   int
}

func (g *generator) buildPrompt(ctx context.Context, req conversation.GenerateRequest, tools *mcp.RunTools) promptParts {
	rt := g.h.runtime()
	var sections []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			sections = append(sections, s)
		}
	}
	add(rt.Chat.DefaultSystemPrompt)
	add(req.System)

	var out promptParts
	if req.Task == "chat" && g.h.memory != nil && !req.Temporary {
		last := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				last = req.Messages[i].Content
				break
			}
		}
		pc, err := g.h.memory.BuildPromptContext(ctx, req.UserID, last, memory.DefaultEstimate)
		if err == nil {
			add(pc.Prefs)
			out.memoryBlock = pc.Mems
		}
	}
	if len(req.SkillIDs) > 0 && g.h.skills != nil {
		if skills, err := g.h.skills.Resolve(ctx, req.UserID, req.SkillIDs, rt.MCP.MaxSelectedToolsPerMessage); err == nil && len(skills) > 0 {
			add(skill.BuildContext(skills, rt.Chat.SkillsPrompt))
			out.skillCount = len(skills)
		}
	}
	if tools != nil && tools.HasTools() {
		add(tools.Guidance())
	}
	if len(req.UIComponentIDs) > 0 && g.h.ui != nil {
		if comps, err := g.h.ui.Resolve(ctx, req.UserID, req.UIComponentIDs); err == nil && len(comps) > 0 {
			add(g.h.ui.Catalog(ctx, comps, req.UIBlockVersion).Text)
		}
	}
	if len(req.UIStates) > 0 {
		var b strings.Builder
		b.WriteString("Current state of interactive blocks shown earlier in this conversation (advisory):\n")
		for _, st := range req.UIStates {
			raw, _ := json.Marshal(st.State)
			fmt.Fprintf(&b, "- block %s (%s): %s\n", st.BlockID, st.Title, raw)
		}
		add(b.String())
	}
	if req.HTMLVisualPrompt {
		add("When a visual layout would explain the answer better than prose, you may include one self-contained HTML document in a fenced html code block.")
	}
	out.instructions = strings.Join(sections, "\n\n")
	return out
}

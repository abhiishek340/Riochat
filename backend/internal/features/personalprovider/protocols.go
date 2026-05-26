package personalprovider

import (
	"sort"
	"strings"
)

// Protocol identifiers.
const (
	ProtoOpenAIChat       = "openai_chat_completions"
	ProtoOpenAIResponses  = "openai_responses"
	ProtoOpenAIImageGen   = "openai_image_generations"
	ProtoOpenAIImageEdit  = "openai_image_edits"
	ProtoAnthropic        = "anthropic_messages"
	ProtoGoogleGenerate   = "google_generate_content"
	ProtoGoogleImage      = "google_image_generation"
	ProtoGeminiInteract   = "gemini_interactions"
	ProtoXAIResponses     = "xai_responses"
	ProtoXAIImage         = "xai_image"
	ProtoXAIImageEdits    = "xai_image_edits"
	ProtoXAIVideo         = "xai_video"
	ProtoXAIVideoExtend   = "xai_video_extensions"
	ProtoOpenRouterChat   = "openrouter_chat_completions"
	ProtoOpenRouterResp   = "openrouter_responses"
	ProtoOpenRouterImages = "openrouter_images"
)

// providerProtocols are the protocols a provider may be registered with, in
// the order the API reports them.
var providerProtocols = []string{
	ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropic, ProtoGoogleGenerate,
	ProtoGeminiInteract, ProtoXAIResponses, ProtoOpenRouterChat, ProtoOpenRouterResp,
}

// modelProtocols are the protocols selectable per model, in reporting order.
var modelProtocols = []string{
	ProtoOpenAIChat, ProtoOpenAIResponses, ProtoOpenAIImageGen, ProtoOpenAIImageEdit,
	ProtoAnthropic, ProtoGoogleGenerate, ProtoGoogleImage, ProtoGeminiInteract,
	ProtoXAIResponses, ProtoXAIImage, ProtoXAIImageEdits, ProtoXAIVideo, ProtoXAIVideoExtend,
	ProtoOpenRouterChat, ProtoOpenRouterResp, ProtoOpenRouterImages,
}

// companionSets are the only multi-protocol combinations a model may carry.
var companionSets = [][2]string{
	{ProtoOpenAIImageGen, ProtoOpenAIImageEdit},
	{ProtoXAIImage, ProtoXAIImageEdits},
	{ProtoXAIVideo, ProtoXAIVideoExtend},
}

// ProviderProtocols returns a copy of the ordered provider protocol list.
func ProviderProtocols() []string { return append([]string(nil), providerProtocols...) }

// ModelProtocols returns a copy of the ordered model protocol list.
func ModelProtocols() []string { return append([]string(nil), modelProtocols...) }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// normalizeProviderProtocol lower-cases and checks a provider protocol.
func normalizeProviderProtocol(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if !contains(providerProtocols, p) {
		return "", ErrInvalidProtocol
	}
	return p, nil
}

// normalizeModelProtocols lower-cases, trims and de-duplicates the protocols
// of one model and checks that they are known and form an allowed combination.
func normalizeModelProtocols(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		if !contains(modelProtocols, p) {
			return nil, ErrInvalidModelProto
		}
		seen[p] = true
		out = append(out, p)
	}
	switch len(out) {
	case 0:
		return nil, ErrInvalidModelProto
	case 1:
		return out, nil
	case 2:
		for _, pair := range companionSets {
			if seen[pair[0]] && seen[pair[1]] {
				return []string{pair[0], pair[1]}, nil
			}
		}
	}
	return nil, ErrInvalidModelProto
}

// Task kinds a caller may ask a personal route to serve.
const (
	TaskChat            = "chat"
	TaskImageGeneration = "image_generation"
	TaskImageEdit       = "image_edit"
	TaskVideoGeneration = "video_generation"
	TaskVideoExtension  = "video_extension"
)

var taskProtocols = map[string][]string{
	TaskChat: {ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropic, ProtoGoogleGenerate,
		ProtoGeminiInteract, ProtoXAIResponses, ProtoOpenRouterChat, ProtoOpenRouterResp},
	TaskImageGeneration: {ProtoOpenAIImageGen, ProtoGoogleImage, ProtoXAIImage, ProtoOpenRouterImages},
	TaskImageEdit:       {ProtoOpenAIImageEdit, ProtoXAIImageEdits},
	TaskVideoGeneration: {ProtoXAIVideo},
	TaskVideoExtension:  {ProtoXAIVideoExtend},
}

// ProtocolForTask picks the first of protocols that can serve task. An empty
// task means chat.
func ProtocolForTask(protocols []string, task string) (string, bool) {
	if task == "" {
		task = TaskChat
	}
	for _, p := range protocols {
		if contains(taskProtocols[task], p) {
			return p, true
		}
	}
	return "", false
}

type modelKind int

const (
	kindChat modelKind = iota
	kindImage
	kindVideo
)

// guessKind infers what a model produces from its name.
func guessKind(name string) (kind modelKind, xai bool, editable bool) {
	n := strings.ToLower(name)
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	switch {
	case strings.Contains(n, "grok-imagine") && strings.Contains(n, "video"):
		return kindVideo, true, true
	case strings.Contains(n, "grok") && strings.Contains(n, "video"):
		return kindVideo, true, true
	case strings.Contains(n, "grok-imagine"), strings.Contains(n, "grok") && strings.Contains(n, "image"):
		return kindImage, true, true
	case strings.Contains(n, "video"), strings.HasPrefix(n, "sora"), strings.HasPrefix(n, "veo"):
		return kindVideo, false, false
	case strings.Contains(n, "image"), strings.HasPrefix(n, "dall-e"), strings.HasPrefix(n, "imagen"),
		strings.Contains(n, "flux"), strings.Contains(n, "stable-diffusion"):
		// dall-e-3 only generates; the others also edit.
		return kindImage, false, !strings.HasPrefix(n, "dall-e-3")
	}
	return kindChat, false, false
}

// suggestProtocols derives the protocols to offer for a catalog model from its
// name and the provider protocol. It never returns an empty list.
func suggestProtocols(providerProtocol, modelName string) []string {
	fallback := []string{providerProtocol}
	kind, xai, editable := guessKind(modelName)
	if kind == kindChat {
		return fallback
	}
	if xai {
		if kind == kindVideo {
			return []string{ProtoXAIVideo, ProtoXAIVideoExtend}
		}
		return []string{ProtoXAIImage, ProtoXAIImageEdits}
	}
	switch providerProtocol {
	case ProtoOpenAIChat, ProtoOpenAIResponses:
		if kind == kindImage {
			if editable {
				return []string{ProtoOpenAIImageGen, ProtoOpenAIImageEdit}
			}
			return []string{ProtoOpenAIImageGen}
		}
	case ProtoGoogleGenerate, ProtoGeminiInteract:
		if kind == kindImage {
			return []string{ProtoGoogleImage}
		}
	case ProtoOpenRouterChat, ProtoOpenRouterResp:
		if kind == kindImage {
			return []string{ProtoOpenRouterImages}
		}
	}
	return fallback
}

// sortedUnique returns the sorted distinct elements of in.
func sortedUnique(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

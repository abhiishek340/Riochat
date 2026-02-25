// Package llm is the uniform model-provider transport. It turns a
// provider-neutral Request into the wire format of one of sixteen protocols,
// performs the HTTP exchange (buffered or streaming) and returns a
// provider-neutral Result with normalized token usage.
package llm

import (
	"fmt"
	"strings"
)

// Protocol names a provider wire format.
type Protocol string

// The sixteen supported protocols.
const (
	OpenAIResponses        Protocol = "openai_responses"
	OpenAIChatCompletions  Protocol = "openai_chat_completions"
	OpenAIImageGenerations Protocol = "openai_image_generations"
	OpenAIImageEdits       Protocol = "openai_image_edits"
	OpenRouterChat         Protocol = "openrouter_chat_completions"
	OpenRouterResponses    Protocol = "openrouter_responses"
	OpenRouterImages       Protocol = "openrouter_images"
	AnthropicMessages      Protocol = "anthropic_messages"
	GoogleGenerateContent  Protocol = "google_generate_content"
	GoogleImageGeneration  Protocol = "google_image_generation"
	GeminiInteractions     Protocol = "gemini_interactions"
	XAIResponses           Protocol = "xai_responses"
	XAIImage               Protocol = "xai_image"
	XAIImageEdits          Protocol = "xai_image_edits"
	XAIVideo               Protocol = "xai_video"
	XAIVideoExtensions     Protocol = "xai_video_extensions"
)

// AllProtocols lists every protocol.
var AllProtocols = []Protocol{
	OpenAIResponses, OpenAIChatCompletions, OpenAIImageGenerations, OpenAIImageEdits,
	OpenRouterChat, OpenRouterResponses, OpenRouterImages, AnthropicMessages,
	GoogleGenerateContent, GoogleImageGeneration, GeminiInteractions, XAIResponses,
	XAIImage, XAIImageEdits, XAIVideo, XAIVideoExtensions,
}

var legacyAliases = map[string]Protocol{
	"openai":                  OpenAIResponses,
	"openrouter":              OpenRouterResponses,
	"anthropic":               AnthropicMessages,
	"claude":                  AnthropicMessages,
	"xai":                     XAIResponses,
	"grok":                    XAIResponses,
	"google":                  GoogleGenerateContent,
	"gemini":                  GoogleGenerateContent,
	"gemini_generate_content": GoogleGenerateContent,
}

// NormalizeProtocol trims and lower-cases a protocol name. An empty name means
// openai_responses; legacy aliases are accepted; anything else fails with an
// unsupported-protocol error naming it.
func NormalizeProtocol(name string) (Protocol, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return OpenAIResponses, nil
	}
	for _, p := range AllProtocols {
		if string(p) == n {
			return p, nil
		}
	}
	if p, ok := legacyAliases[n]; ok {
		return p, nil
	}
	return "", &Error{Kind: KindUnsupported, Sentinel: ErrUnsupportedProtocol, Message: fmt.Sprintf("unsupported protocol %q", strings.TrimSpace(name))}
}

// SupportsStream reports whether the protocol can stream.
func (p Protocol) SupportsStream() bool {
	switch p {
	case XAIImage, XAIImageEdits, XAIVideo, XAIVideoExtensions:
		return false
	}
	return true
}

// SupportsImageStream reports whether image output can stream for a model.
func (p Protocol) SupportsImageStream(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	switch p {
	case OpenAIImageGenerations:
		return strings.HasPrefix(m, "gpt-image-")
	case OpenAIImageEdits:
		return strings.HasPrefix(m, "gpt-image-") || m == "chatgpt-image-latest"
	case GoogleImageGeneration, GeminiInteractions, OpenRouterImages:
		return true
	}
	return false
}

// IsImageGeneration reports the image-generation capability class.
func (p Protocol) IsImageGeneration() bool {
	switch p {
	case OpenAIImageGenerations, GoogleImageGeneration, GeminiInteractions, XAIImage, OpenRouterImages:
		return true
	}
	return false
}

// IsImageEdit reports the image-edit capability class.
func (p Protocol) IsImageEdit() bool {
	switch p {
	case OpenAIImageEdits, GoogleImageGeneration, GeminiInteractions, XAIImageEdits, OpenRouterImages:
		return true
	}
	return false
}

// IsVideoGeneration reports the video-generation capability class.
func (p Protocol) IsVideoGeneration() bool {
	switch p {
	case GeminiInteractions, XAIVideo, XAIVideoExtensions:
		return true
	}
	return false
}

// isImageProtocol is true for protocols whose primary output is an image.
func (p Protocol) isImageProtocol() bool {
	switch p {
	case OpenAIImageGenerations, OpenAIImageEdits, OpenRouterImages, XAIImage, XAIImageEdits:
		return true
	}
	return false
}

// SupportsPreviousResponse reports whether stateful continuation by previous
// response id is allowed.
func (p Protocol) SupportsPreviousResponse() bool {
	return p == OpenAIResponses || p == GeminiInteractions
}

// PolicyKey is the key used by the option allow/deny documents.
func (p Protocol) PolicyKey() string {
	if p == GoogleGenerateContent {
		return "gemini_generate_content"
	}
	for _, k := range AllProtocols {
		if k == p {
			return string(p)
		}
	}
	return string(OpenAIResponses)
}

// PolicyKeyFor maps any name, including legacy aliases, to its policy key;
// unknown names map to openai_responses.
func PolicyKeyFor(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return string(OpenAIResponses)
	}
	if p, err := NormalizeProtocol(n); err == nil {
		return p.PolicyKey()
	}
	return string(OpenAIResponses)
}

func (p Protocol) isGoogle() bool { return p == GoogleGenerateContent || p == GoogleImageGeneration }

func (p Protocol) isResponses() bool {
	return p == OpenAIResponses || p == OpenRouterResponses || p == XAIResponses
}

func (p Protocol) isChat() bool { return p == OpenAIChatCompletions || p == OpenRouterChat }

func (p Protocol) isXAI() bool {
	return p == XAIResponses || p == XAIImage || p == XAIImageEdits || p == XAIVideo || p == XAIVideoExtensions
}

package llm

import (
	"errors"
	"testing"
)

func TestNormalizeProtocol(t *testing.T) {
	cases := map[string]Protocol{
		"": OpenAIResponses, "  OPENAI_CHAT_COMPLETIONS ": OpenAIChatCompletions, "xai_video": XAIVideo,
		"claude": AnthropicMessages, "gemini": GoogleGenerateContent, "gemini_generate_content": GoogleGenerateContent,
		"grok": XAIResponses, "openrouter": OpenRouterResponses, "google_image_generation": GoogleImageGeneration,
	}
	for in, want := range cases {
		got, err := NormalizeProtocol(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q,%v want %q", in, got, err, want)
		}
	}
	_, err := NormalizeProtocol(" Telepathy ")
	if !errors.Is(err, ErrUnsupportedProtocol) || err.Error() != `unsupported protocol "Telepathy"` {
		t.Fatalf("got %v", err)
	}
	if len(AllProtocols) != 16 {
		t.Fatalf("sixteen protocols expected, have %d", len(AllProtocols))
	}
}

func TestCapabilityClasses(t *testing.T) {
	for _, p := range []Protocol{XAIImage, XAIImageEdits, XAIVideo, XAIVideoExtensions} {
		if p.SupportsStream() {
			t.Errorf("%s must not stream", p)
		}
	}
	if !OpenAIChatCompletions.SupportsStream() || !GeminiInteractions.SupportsStream() {
		t.Fatal("stream support")
	}
	if !OpenAIImageGenerations.SupportsImageStream("gpt-image-1") || OpenAIImageGenerations.SupportsImageStream("dall-e-3") ||
		OpenAIImageGenerations.SupportsImageStream("chatgpt-image-latest") {
		t.Fatal("openai image generation streaming is gpt-image-* only")
	}
	if !OpenAIImageEdits.SupportsImageStream("chatgpt-image-latest") || !OpenAIImageEdits.SupportsImageStream("gpt-image-1") || OpenAIImageEdits.SupportsImageStream("dall-e-2") {
		t.Fatal("openai image edit streaming")
	}
	if !GoogleImageGeneration.SupportsImageStream("anything") || !OpenRouterImages.SupportsImageStream("x") || !GeminiInteractions.SupportsImageStream("") {
		t.Fatal("always-streaming image protocols")
	}
	gen := []Protocol{OpenAIImageGenerations, GoogleImageGeneration, GeminiInteractions, XAIImage, OpenRouterImages}
	for _, p := range gen {
		if !p.IsImageGeneration() {
			t.Errorf("%s image generation", p)
		}
	}
	edit := []Protocol{OpenAIImageEdits, GoogleImageGeneration, GeminiInteractions, XAIImageEdits, OpenRouterImages}
	for _, p := range edit {
		if !p.IsImageEdit() {
			t.Errorf("%s image edit", p)
		}
	}
	if !XAIVideo.IsVideoGeneration() || !XAIVideoExtensions.IsVideoGeneration() || !GeminiInteractions.IsVideoGeneration() || OpenAIResponses.IsVideoGeneration() {
		t.Fatal("video class")
	}
	if !OpenAIResponses.SupportsPreviousResponse() || OpenAIChatCompletions.SupportsPreviousResponse() || XAIResponses.SupportsPreviousResponse() {
		t.Fatal("previous response continuation")
	}
}

func TestPolicyKeys(t *testing.T) {
	cases := map[string]string{
		"google_generate_content": "gemini_generate_content", "openai": "openai_responses", "openrouter": "openrouter_responses",
		"anthropic": "anthropic_messages", "claude": "anthropic_messages", "xai": "xai_responses", "grok": "xai_responses",
		"google": "gemini_generate_content", "gemini": "gemini_generate_content", "xai_video": "xai_video",
		"nonsense": "openai_responses", "": "openai_responses", "openai_image_edits": "openai_image_edits",
	}
	for in, want := range cases {
		if got := PolicyKeyFor(in); got != want {
			t.Errorf("PolicyKeyFor(%q)=%q want %q", in, got, want)
		}
	}
}

func TestUnsupportedProtocolAndStreamErrors(t *testing.T) {
	c := testClient(t)
	_, err := c.Generate(nil, Route{Protocol: "bogus", BaseURL: "http://x"}, &Request{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("got %v", err)
	}
	sink, _ := collect()
	_, err = c.Stream(nil, Route{Protocol: string(XAIVideo), BaseURL: "http://x", Model: "m"}, &Request{}, sink)
	if !errors.Is(err, ErrUnsupportedStream) {
		t.Fatalf("got %v", err)
	}
	_, err = c.Stream(nil, Route{Protocol: string(OpenAIImageGenerations), BaseURL: "http://x", Model: "dall-e-3"}, &Request{}, sink)
	if !errors.Is(err, ErrUnsupportedStream) {
		t.Fatalf("dall-e-3 stream: %v", err)
	}
}

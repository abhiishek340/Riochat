package channel

import (
	"encoding/json"
	"sort"
	"strings"
)

// Kinds of platform model.
const (
	KindChat           = "chat"
	KindAudio          = "audio"
	KindImageGen       = "image_gen"
	KindImageEdit      = "image_edit"
	KindVideoGen       = "video_gen"
	KindVideoExtension = "video_extension"
)

// kindOrder is the canonical order of kinds; it also defines the "primary
// kind" of a model (the first one it contains).
var kindOrder = []string{KindChat, KindAudio, KindImageGen, KindImageEdit, KindVideoGen, KindVideoExtension}

// Task types a request may ask for.
const (
	TaskChat            = "chat"
	TaskImageGeneration = "image_generation"
	TaskImageEdit       = "image_edit"
	TaskVideoGeneration = "video_generation"
	TaskVideoExtension  = "video_extension"
)

// Wire protocol identifiers.
const (
	ProtoOpenAIResponses     = "openai_responses"
	ProtoOpenAIChat          = "openai_chat_completions"
	ProtoOpenRouterChat      = "openrouter_chat_completions"
	ProtoOpenRouterResponses = "openrouter_responses"
	ProtoOpenRouterImages    = "openrouter_images"
	ProtoAnthropicMessages   = "anthropic_messages"
	ProtoGoogleGenerate      = "google_generate_content"
	ProtoGoogleImage         = "google_image_generation"
	ProtoGeminiInteractions  = "gemini_interactions"
	ProtoXAIResponses        = "xai_responses"
	ProtoOpenAIImageGen      = "openai_image_generations"
	ProtoOpenAIImageEdit     = "openai_image_edits"
	ProtoOpenAIVideoGen      = "openai_video_generations"
	ProtoXAIImage            = "xai_image"
	ProtoXAIImageEdit        = "xai_image_edits"
	ProtoXAIVideo            = "xai_video"
	ProtoXAIVideoExtension   = "xai_video_extensions"
)

// Upstream styles ("compatible").
const (
	StyleOpenAI     = "openai"
	StyleAnthropic  = "anthropic"
	StyleGoogle     = "google"
	StyleXAI        = "xai"
	StyleOpenRouter = "openrouter"
	StyleCustom     = "custom"
)

var allStyles = []string{StyleOpenAI, StyleAnthropic, StyleGoogle, StyleXAI, StyleOpenRouter, StyleCustom}

func validStyle(s string) bool {
	for _, v := range allStyles {
		if v == s {
			return true
		}
	}
	return false
}

var knownProtocols = map[string]bool{
	ProtoOpenAIResponses: true, ProtoOpenAIChat: true, ProtoOpenRouterChat: true, ProtoOpenRouterResponses: true,
	ProtoOpenRouterImages: true, ProtoAnthropicMessages: true, ProtoGoogleGenerate: true, ProtoGoogleImage: true,
	ProtoGeminiInteractions: true, ProtoXAIResponses: true, ProtoOpenAIImageGen: true, ProtoOpenAIImageEdit: true,
	ProtoOpenAIVideoGen: true, ProtoXAIImage: true, ProtoXAIImageEdit: true, ProtoXAIVideo: true, ProtoXAIVideoExtension: true,
}

// isKnownProtocol reports whether p is one of the 17 protocol identifiers.
func isKnownProtocol(p string) bool { return knownProtocols[p] }

// isImplementedProtocol reports whether a transport exists for p. The video
// protocol of the OpenAI style is accepted in configuration but never served.
func isImplementedProtocol(p string) bool { return knownProtocols[p] && p != ProtoOpenAIVideoGen }

// chatProtocols are the text protocols; gemini_interactions is chat-capable
// only (not audio).
var chatProtocols = []string{
	ProtoOpenAIResponses, ProtoOpenRouterChat, ProtoOpenRouterResponses, ProtoOpenAIChat,
	ProtoAnthropicMessages, ProtoGoogleGenerate, ProtoGeminiInteractions, ProtoXAIResponses,
}

var allowedByKind = map[string][]string{
	KindChat: chatProtocols,
	KindAudio: {
		ProtoOpenAIResponses, ProtoOpenRouterChat, ProtoOpenRouterResponses, ProtoOpenAIChat,
		ProtoAnthropicMessages, ProtoGoogleGenerate, ProtoXAIResponses,
	},
	KindImageGen:       {ProtoOpenAIImageGen, ProtoOpenRouterImages, ProtoGoogleImage, ProtoGeminiInteractions, ProtoXAIImage},
	KindImageEdit:      {ProtoOpenAIImageEdit, ProtoOpenRouterImages, ProtoGoogleImage, ProtoGeminiInteractions, ProtoXAIImageEdit},
	KindVideoGen:       {ProtoOpenAIVideoGen, ProtoGeminiInteractions, ProtoXAIVideo},
	KindVideoExtension: {ProtoXAIVideoExtension},
}

// protocolAllowedForKind reports whether protocol p may serve kind k.
func protocolAllowedForKind(p, k string) bool {
	for _, a := range allowedByKind[k] {
		if a == p {
			return true
		}
	}
	return false
}

func protocolAllowedForAnyKind(p string, kinds []string) bool {
	for _, k := range kinds {
		if protocolAllowedForKind(p, k) {
			return true
		}
	}
	return false
}

func isKind(k string) bool {
	for _, v := range kindOrder {
		if v == k {
			return true
		}
	}
	return false
}

// protocolDefaultsFallback is the system default protocol per kind for each
// upstream style, used when the upstream's own defaults have no entry.
var protocolDefaultsFallback = map[string]map[string]string{
	StyleOpenAI: {
		KindChat: ProtoOpenAIChat, KindAudio: ProtoOpenAIChat, KindImageGen: ProtoOpenAIImageGen,
		KindImageEdit: ProtoOpenAIImageEdit, KindVideoGen: ProtoOpenAIVideoGen,
	},
	StyleAnthropic: {KindChat: ProtoAnthropicMessages, KindAudio: ProtoAnthropicMessages},
	StyleGoogle: {
		KindChat: ProtoGoogleGenerate, KindAudio: ProtoGoogleGenerate, KindImageGen: ProtoGoogleImage,
		KindImageEdit: ProtoGoogleImage, KindVideoGen: ProtoGeminiInteractions,
	},
	StyleXAI: {
		KindChat: ProtoXAIResponses, KindAudio: ProtoXAIResponses, KindImageGen: ProtoXAIImage,
		KindImageEdit: ProtoXAIImageEdit, KindVideoGen: ProtoXAIVideo, KindVideoExtension: ProtoXAIVideoExtension,
	},
	StyleOpenRouter: {
		KindChat: ProtoOpenRouterResponses, KindAudio: ProtoOpenRouterResponses, KindImageGen: ProtoOpenRouterImages,
		KindImageEdit: ProtoOpenRouterImages, KindVideoGen: ProtoOpenAIVideoGen,
	},
	StyleCustom: {
		KindChat: ProtoOpenAIChat, KindAudio: ProtoOpenAIChat, KindImageGen: ProtoOpenAIImageGen, KindImageEdit: ProtoOpenAIImageEdit,
	},
}

// fallbackDefaultsJSON renders the fallback table of a style as a compact JSON
// object keyed by kind ("{}" for an unknown style).
func fallbackDefaultsJSON(style string) string {
	m := protocolDefaultsFallback[style]
	if m == nil {
		return "{}"
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// protocolDefaults is a kind -> protocol map.
type protocolDefaults map[string]string

// parseProtocolDefaultsStrict parses an administrator-supplied defaults
// document. Unknown kind keys are dropped; values are trimmed and lower-cased,
// blank values dropped; unknown or disallowed protocols fail with
// llm.invalid_adapter and malformed JSON with llm.invalid_protocol_defaults_config.
func parseProtocolDefaultsStrict(raw string) (protocolDefaults, error) {
	raw = strings.TrimSpace(raw)
	out := protocolDefaults{}
	if raw == "" {
		return out, nil
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(raw), &in); err != nil || in == nil {
		return nil, ErrInvalidDefaults
	}
	for k, v := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		if !isKind(k) {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, ErrInvalidDefaults
		}
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !isKnownProtocol(s) || !protocolAllowedForKind(s, k) {
			return nil, ErrInvalidAdapter
		}
		out[k] = s
	}
	return out, nil
}

// parseProtocolDefaultsLenient is the display/runtime variant: invalid legacy
// entries are silently omitted and unusable input yields an empty map.
func parseProtocolDefaultsLenient(raw string) protocolDefaults {
	out := protocolDefaults{}
	var in map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &in) != nil {
		return out
	}
	for k, v := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		s, ok := v.(string)
		if !ok || !isKind(k) {
			continue
		}
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && isKnownProtocol(s) && protocolAllowedForKind(s, k) {
			out[k] = s
		}
	}
	return out
}

func (d protocolDefaults) json() string {
	if len(d) == 0 {
		return "{}"
	}
	raw, _ := json.Marshal(map[string]string(d))
	return string(raw)
}

// lookup returns the upstream's own default for a kind, then the fallback of
// its style ("" when neither has one).
func (d protocolDefaults) lookup(style, kind string) string {
	if p := d[kind]; p != "" {
		return p
	}
	return protocolDefaultsFallback[style][kind]
}

func primaryKind(kinds []string) string {
	for _, k := range kindOrder {
		if containsString(kinds, k) {
			return k
		}
	}
	return ""
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func protocolSupportsAllKinds(p string, kinds []string) bool {
	for _, k := range kinds {
		if !protocolAllowedForKind(p, k) {
			return false
		}
	}
	return len(kinds) > 0
}

// unifiedVideoProtocol applies the "universal interactions model" rule: a model
// with several kinds including video_gen gets the upstream's video_gen default
// when that protocol supports every kind.
func unifiedVideoProtocol(style string, defs protocolDefaults, kinds []string) string {
	if len(kinds) > 1 && containsString(kinds, KindVideoGen) {
		if p := defs.lookup(style, KindVideoGen); p != "" && protocolSupportsAllKinds(p, kinds) {
			return p
		}
	}
	return ""
}

// resolveProtocol resolves a single protocol for a model with the given kinds.
func resolveProtocol(style string, defs protocolDefaults, kinds []string, explicit string) (string, error) {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	if explicit != "" {
		if !isKnownProtocol(explicit) {
			return "", ErrInvalidAdapter
		}
		if len(kinds) > 0 && !protocolAllowedForAnyKind(explicit, kinds) {
			return "", ErrInvalidAdapter
		}
		return explicit, nil
	}
	pk := primaryKind(kinds)
	if pk == "" {
		return "", ErrProtocolRequired
	}
	if p := unifiedVideoProtocol(style, defs, kinds); p != "" {
		return p, nil
	}
	if p := defs.lookup(style, pk); p != "" {
		return p, nil
	}
	return "", ErrProtocolRequired
}

// resolveProtocolSet resolves the protocol set of a binding. Explicit entries
// are resolved one by one; without them the set is inferred from the kinds.
func resolveProtocolSet(style string, defs protocolDefaults, kinds []string, explicit []string) ([]string, error) {
	var cleaned []string
	for _, e := range explicit {
		if strings.TrimSpace(e) != "" {
			cleaned = append(cleaned, e)
		}
	}
	if len(cleaned) > 0 {
		// The shape of the set is judged first: only well-known identifiers
		// can form a combination, and a combination that is not one of the
		// paired sets is refused as such even when a member would also be
		// refused for the model's kinds.
		var named []string
		for _, e := range cleaned {
			p := strings.ToLower(strings.TrimSpace(e))
			if !isKnownProtocol(p) {
				return nil, ErrInvalidAdapter
			}
			named = append(named, p)
		}
		if !supportedCombination(dedupStrings(named)) {
			return nil, ErrInvalidCombination
		}
		var out []string
		for _, e := range cleaned {
			p, err := resolveProtocol(style, defs, kinds, e)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		out = dedupStrings(out)
		if !supportedCombination(out) {
			return nil, ErrInvalidCombination
		}
		return out, nil
	}
	if p := unifiedVideoProtocol(style, defs, kinds); p != "" {
		return []string{p}, nil
	}
	if containsString(kinds, KindImageGen) && containsString(kinds, KindImageEdit) {
		gen := defs.lookup(style, KindImageGen)
		edit := defs.lookup(style, KindImageEdit)
		if gen != "" && edit != "" {
			set := dedupStrings([]string{gen, edit})
			if supportedCombination(set) {
				return set, nil
			}
		}
	}
	if containsString(kinds, KindVideoGen) && containsString(kinds, KindVideoExtension) &&
		style == StyleXAI && defs.lookup(style, KindVideoGen) == ProtoXAIVideo {
		return []string{ProtoXAIVideo, ProtoXAIVideoExtension}, nil
	}
	p, err := resolveProtocol(style, defs, kinds, "")
	if err != nil {
		return nil, err
	}
	return []string{p}, nil
}

// supportedCombination reports whether a protocol set may live on one binding:
// at most one distinct protocol, or one of the paired media protocol sets.
func supportedCombination(protocols []string) bool {
	set := dedupStrings(protocols)
	switch len(set) {
	case 0, 1:
		return true
	case 2:
		a, b := set[0], set[1]
		if a > b {
			a, b = b, a
		}
		switch [2]string{a, b} {
		case [2]string{ProtoOpenAIImageEdit, ProtoOpenAIImageGen},
			[2]string{ProtoXAIImage, ProtoXAIImageEdit},
			[2]string{ProtoXAIVideo, ProtoXAIVideoExtension}:
			return true
		}
	}
	return false
}

// NormalizeTaskType maps anything unknown or blank to chat.
func NormalizeTaskType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case TaskImageGeneration:
		return TaskImageGeneration
	case TaskImageEdit:
		return TaskImageEdit
	case TaskVideoGeneration:
		return TaskVideoGeneration
	case TaskVideoExtension:
		return TaskVideoExtension
	}
	return TaskChat
}

func validTaskType(t string) bool {
	switch t {
	case TaskChat, TaskImageGeneration, TaskImageEdit, TaskVideoGeneration, TaskVideoExtension:
		return true
	}
	return false
}

// RouteServesTask reports whether a route with the given protocol on a model
// with the given kinds can serve a task. With no kinds recorded only the
// protocol is checked.
func RouteServesTask(kinds []string, protocol, task string) bool {
	task = NormalizeTaskType(task)
	switch task {
	case TaskChat:
		if len(kinds) == 0 {
			return protocolAllowedForKind(protocol, KindChat) || protocolAllowedForKind(protocol, KindAudio)
		}
		return (containsString(kinds, KindChat) && protocolAllowedForKind(protocol, KindChat)) ||
			(containsString(kinds, KindAudio) && protocolAllowedForKind(protocol, KindAudio))
	case TaskImageGeneration:
		return (len(kinds) == 0 || containsString(kinds, KindImageGen)) && protocolAllowedForKind(protocol, KindImageGen)
	case TaskImageEdit:
		return (len(kinds) == 0 || containsString(kinds, KindImageEdit)) && protocolAllowedForKind(protocol, KindImageEdit)
	case TaskVideoGeneration:
		return protocol != ProtoXAIVideoExtension &&
			(len(kinds) == 0 || containsString(kinds, KindVideoGen)) && protocolAllowedForKind(protocol, KindVideoGen)
	case TaskVideoExtension:
		return (len(kinds) == 0 || containsString(kinds, KindVideoExtension)) && protocol == ProtoXAIVideoExtension
	}
	return false
}

// ModelSupportsTask is the static capability check for a model (no route
// health involved).
func ModelSupportsTask(kinds []string, task string) bool {
	if len(kinds) == 0 {
		return true
	}
	switch NormalizeTaskType(task) {
	case TaskImageGeneration:
		return containsString(kinds, KindImageGen)
	case TaskImageEdit:
		return containsString(kinds, KindImageEdit)
	case TaskVideoGeneration:
		return containsString(kinds, KindVideoGen)
	case TaskVideoExtension:
		return containsString(kinds, KindVideoExtension)
	}
	return containsString(kinds, KindChat)
}

// normalizeKinds accepts a JSON array of strings or a bare single string and
// returns trimmed, lower-cased, de-duplicated, validated kinds in the order
// given. An empty result is an error.
func normalizeKinds(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidKinds
	}
	var list []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil, ErrInvalidKinds
		}
	} else {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			s = raw
		}
		list = []string{s}
	}
	var out []string
	seen := map[string]bool{}
	for _, k := range list {
		k = strings.ToLower(strings.TrimSpace(k))
		if !isKind(k) {
			return nil, ErrInvalidKinds
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return nil, ErrInvalidKinds
	}
	return out, nil
}

func kindsJSON(kinds []string) string {
	raw, _ := json.Marshal(kinds)
	return string(raw)
}

// parseStoredKinds reads kinds_json leniently (invalid entries dropped).
func parseStoredKinds(raw string) []string {
	var list []string
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &list) != nil {
		var s string
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &s) == nil {
			list = []string{s}
		}
	}
	var out []string
	for _, k := range list {
		k = strings.ToLower(strings.TrimSpace(k))
		if isKind(k) && !containsString(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// inferKinds infers the kinds of a platform model from its name.
func inferKinds(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(n, "gemini-omni-flash"):
		return []string{KindChat, KindImageGen, KindImageEdit, KindVideoGen}
	case strings.HasPrefix(n, "gpt-image-"), n == "chatgpt-image-latest", n == "dall-e-2", isGeminiImageName(n),
		strings.HasPrefix(n, "grok-imagine-image"):
		return []string{KindImageGen, KindImageEdit}
	case n == "dall-e-3", strings.HasPrefix(n, "imagen-"):
		return []string{KindImageGen}
	case strings.HasPrefix(n, "grok-imagine-video"):
		return []string{KindVideoGen, KindVideoExtension}
	case n == "sora", n == "veo-2", strings.HasPrefix(n, "kling"), strings.HasPrefix(n, "veo-"):
		return []string{KindVideoGen}
	case strings.HasPrefix(n, "gpt-4o-audio"):
		return []string{KindAudio}
	}
	return []string{KindChat}
}

var geminiImageNames = map[string]bool{
	"nano-banana": true, "nano-banana-2": true, "nano-banana-pro": true, "gemini-2.5-flash-image": true,
	"gemini-3.1-flash-image": true, "gemini-3-pro-image": true, "gemini-3-pro-image-preview": true,
	"gemini-3.1-flash-image-preview": true,
}

func isGeminiImageName(n string) bool { return geminiImageNames[n] }

// SuggestedProtocols returns the protocols a route for a remote model would
// get, never failing with "protocol required" (it yields none instead).
func suggestedProtocols(style string, defs protocolDefaults, kinds []string) ([]string, error) {
	set, err := resolveProtocolSet(style, defs, kinds, nil)
	if err != nil {
		if err == ErrProtocolRequired || isErr(err, ErrProtocolRequired) {
			return nil, nil
		}
		return nil, err
	}
	return set, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// protocolEndpoint is the generic endpoint kind of a protocol.
func protocolEndpoint(p string) string {
	switch p {
	case ProtoOpenAIChat, ProtoOpenRouterChat:
		return "chat_completions"
	case ProtoOpenAIResponses, ProtoOpenRouterResponses, ProtoXAIResponses:
		return "responses"
	case ProtoOpenAIImageGen, ProtoXAIImage, ProtoGoogleImage:
		return "image_generations"
	case ProtoOpenAIImageEdit, ProtoXAIImageEdit:
		return "image_edits"
	case ProtoOpenRouterImages:
		return "images"
	case ProtoOpenAIVideoGen, ProtoXAIVideo:
		return "video_generations"
	case ProtoXAIVideoExtension:
		return "video_extensions"
	case ProtoGeminiInteractions:
		return "interactions"
	case ProtoAnthropicMessages, ProtoGoogleGenerate:
		// The generic endpoint vocabulary has no dedicated entry for these
		// text protocols; they are reported as the chat endpoint.
		return "chat_completions"
	}
	return ""
}

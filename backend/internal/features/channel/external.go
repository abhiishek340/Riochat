package channel

import (
	"context"
	"strings"
	"time"
)

// This file holds the helpers other areas use for user-owned ("personal")
// providers. The provider management itself lives elsewhere; here are the
// protocol and kind inference rules, and the construction of a route and a
// model view for such a provider.

// Fixed timeouts of an external route.
const (
	externalConnectTimeout = 10 * time.Second
	externalReadTimeout    = 120 * time.Second
	externalStreamIdle     = 60 * time.Second
)

// ExternalProvider describes the user's own provider behind a personal model.
type ExternalProvider struct {
	Name     string
	Icon     string
	Host     string
	Protocol string // the provider's chat protocol
	BaseURL  string
	APIKey   string
}

// protocolStyle maps a protocol to the upstream style that normally serves it.
func protocolStyle(p string) string {
	switch p {
	case ProtoAnthropicMessages:
		return StyleAnthropic
	case ProtoGoogleGenerate, ProtoGoogleImage, ProtoGeminiInteractions:
		return StyleGoogle
	case ProtoXAIResponses, ProtoXAIImage, ProtoXAIImageEdit, ProtoXAIVideo, ProtoXAIVideoExtension:
		return StyleXAI
	case ProtoOpenRouterChat, ProtoOpenRouterResponses, ProtoOpenRouterImages:
		return StyleOpenRouter
	}
	return StyleOpenAI
}

func baseModelName(model string) string {
	m := strings.TrimSpace(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}

func onlyTextKinds(kinds []string) bool {
	for _, k := range kinds {
		if k != KindChat && k != KindAudio {
			return false
		}
	}
	return true
}

// SuggestExternalProtocols proposes the protocol set for a personal model:
// the provider protocol for chat-like models, the provider style's media
// protocols for image and video models, and the provider protocol again when
// inference fails.
func SuggestExternalProtocols(providerProtocol, model string) []string {
	providerProtocol = lower(providerProtocol)
	fallback := []string{providerProtocol}
	base := baseModelName(model)
	kinds := inferKinds(base)
	if onlyTextKinds(kinds) {
		return fallback
	}
	style := protocolStyle(providerProtocol)
	if style == StyleOpenAI && strings.HasPrefix(strings.ToLower(base), "grok-imagine") {
		style = StyleXAI
	}
	set, err := resolveProtocolSet(style, protocolDefaults{}, kinds, nil)
	if err != nil || len(set) == 0 {
		return fallback
	}
	for _, p := range set {
		if !isImplementedProtocol(p) {
			return fallback
		}
	}
	return set
}

// NormalizeExternalProtocols validates an accepted protocol set: every entry
// known and implemented, de-duplicated, and a supported combination.
func NormalizeExternalProtocols(list []string) ([]string, error) {
	var out []string
	for _, p := range list {
		p = lower(p)
		if p == "" {
			return nil, ErrProtocolRequired
		}
		if !isImplementedProtocol(p) {
			return nil, ErrInvalidAdapter
		}
		if !containsString(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, ErrProtocolRequired
	}
	if !supportedCombination(out) {
		return nil, ErrInvalidCombination
	}
	return out, nil
}

// ExternalKinds derives the kinds of a personal model: those inferred from its
// base name that the protocols support; when none intersect, every kind the
// protocols support except audio.
func ExternalKinds(model string, protocols []string) []string {
	supports := func(k string) bool {
		for _, p := range protocols {
			if protocolAllowedForKind(p, k) {
				return true
			}
		}
		return false
	}
	var out []string
	for _, k := range inferKinds(baseModelName(model)) {
		if supports(k) {
			out = append(out, k)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, k := range kindOrder {
		if k != KindAudio && supports(k) {
			out = append(out, k)
		}
	}
	return out
}

// ExternalProtocolForTask picks the first listed protocol that can serve a
// task; none gives route.not_found.
func ExternalProtocolForTask(protocols, kinds []string, task string) (string, error) {
	for _, p := range protocols {
		if RouteServesTask(kinds, p, task) {
			return p, nil
		}
	}
	return "", ErrRouteNotFound
}

// ExternalRouteSpec asks for the route of a personal model.
type ExternalRouteSpec struct {
	Provider  ExternalProvider
	Model     string   // the provider-side model name
	Reference string   // the personal reference shown to the user (personal:<id>/<model>)
	Protocols []string // optional accepted protocol set; inferred when empty
	Task      string
}

// BuildExternalRoute builds the route snapshot for a personal model. External
// routes carry no upstream id, so they never take part in circuit breaking,
// back-off or fail-over, and they are flagged untrusted so the caller uses the
// SSRF-strict, no-redirect HTTP client.
func (s *Service) BuildExternalRoute(_ context.Context, spec ExternalRouteSpec) (*Route, error) {
	model := strings.TrimSpace(spec.Model)
	if model == "" {
		return nil, ErrModelNotFound
	}
	protocols := spec.Protocols
	if len(protocols) == 0 {
		protocols = SuggestExternalProtocols(spec.Provider.Protocol, model)
	}
	protocols, err := NormalizeExternalProtocols(protocols)
	if err != nil {
		return nil, err
	}
	kinds := ExternalKinds(model, protocols)
	protocol, err := ExternalProtocolForTask(protocols, kinds, spec.Task)
	if err != nil {
		return nil, err
	}
	vendor := deriveVendor("", model, spec.Provider.Name)
	icon := autoIcon("", model, vendor)
	view := s.catalog.current()
	pkeys := protocolKeys([]string{protocol})
	rEntry := view.matchReasoning(model, vendor, pkeys)
	mEntry := view.matchModalityExternal(model, vendor, pkeys)
	name := strings.TrimSpace(spec.Reference)
	if name == "" {
		name = model
	}
	route := &Route{
		PlatformModelName: name, UpstreamModelName: model, Protocol: protocol,
		BaseURL: strings.TrimSpace(spec.Provider.BaseURL), APIKey: spec.Provider.APIKey,
		ConnectTimeout: externalConnectTimeout, ReadTimeout: externalReadTimeout, StreamIdleTimeout: externalStreamIdle,
		HeadersJSON: "", Vendor: vendor, Icon: icon, CapabilitiesJSON: "{}",
		Reasoning: resolveReasoning(capsMap{}, []string{protocol}, vendor, rEntry),
		External:  true, UntrustedEndpoint: true,
	}
	route.ReasoningPassback, route.PassbackOptions = passbackFor(protocol, vendor, model, spec.Provider.Name)
	if mEntry != nil {
		route.InputModalities = append([]string{}, mEntry.Inputs...)
	}
	return route, nil
}

// ExternalModelSpec asks for the user-facing view of a personal model.
type ExternalModelSpec struct {
	Provider  ExternalProvider
	Model     string
	Reference string
	Protocols []string
}

// BuildExternalModel builds the public view of a personal model: kinds from
// the protocols, empty capabilities, public access scope and the vendor key as
// its display name. Pricing is always null.
func (s *Service) BuildExternalModel(spec ExternalModelSpec) PublicModel {
	model := strings.TrimSpace(spec.Model)
	protocols := spec.Protocols
	if len(protocols) == 0 {
		protocols = SuggestExternalProtocols(spec.Provider.Protocol, model)
	}
	if norm, err := NormalizeExternalProtocols(protocols); err == nil {
		protocols = norm
	} else {
		protocols = []string{lower(spec.Provider.Protocol)}
	}
	kinds := ExternalKinds(model, protocols)
	vendor := deriveVendor("", model, spec.Provider.Name)
	name := strings.TrimSpace(spec.Reference)
	if name == "" {
		name = model
	}
	pm := PublicModel{
		PlatformModelName: name, Vendor: vendor, VendorName: vendor, KindsJSON: kindsJSON(kinds),
		Icon: autoIcon("", model, vendor), ProtocolsJSON: jsonStringArray(protocols), CapabilitiesJSON: "{}",
		Description: model, Source: "personal", ProviderName: spec.Provider.Name, ProviderIcon: spec.Provider.Icon,
		ProviderHost: spec.Provider.Host, Controls: []Control{},
	}
	s.fillCapabilities(&pm, model, vendor, capsMap{}, protocols, s.catalog.current(), true)
	return pm
}

// ---- capability resolver ------------------------------------------------------------------

// CapabilityInput is one model handed to a CapabilityResolver.
type CapabilityInput struct {
	Name             string
	Vendor           string
	CapabilitiesJSON string
	Protocols        []string
	External         bool
}

// ModelCapabilities is the resolved capability view of one model.
type ModelCapabilities struct {
	Reasoning             *PublicReasoning
	Controls              []Control
	InputModalities       []string
	InputModalitiesSource *string
	OutputModalities      []string
	ContextWindow         *int
}

// CapabilityResolver resolves capabilities against one catalog snapshot, so a
// batch of models sees a consistent view.
type CapabilityResolver struct {
	s    *Service
	view *catalogView
}

// CapabilityResolver binds a resolver to the current catalog snapshot.
func (s *Service) CapabilityResolver() *CapabilityResolver {
	return &CapabilityResolver{s: s, view: s.catalog.current()}
}

// Resolve resolves one model.
func (r *CapabilityResolver) Resolve(in CapabilityInput) ModelCapabilities {
	var pm PublicModel
	r.s.fillCapabilities(&pm, in.Name, lower(in.Vendor), parseCapsLenient(in.CapabilitiesJSON), in.Protocols, r.view, in.External)
	return ModelCapabilities{
		Reasoning: pm.Reasoning, Controls: pm.Controls, InputModalities: pm.InputModalities,
		InputModalitiesSource: pm.InputModalitiesSource, OutputModalities: pm.OutputModalities, ContextWindow: pm.ContextWindow,
	}
}

// EffectiveContextWindow is the window used for budgeting a conversation:
// administrator override, then the built-in family table, then the configured
// fallback.
func (s *Service) EffectiveContextWindow(ctx context.Context, name, capabilitiesJSON string) int {
	return effectiveContextWindow(name, parseCapsLenient(capabilitiesJSON), s.contextFallbackTokens(ctx))
}

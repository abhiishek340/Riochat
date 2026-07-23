package channel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Probe statuses.
const (
	probeSuccess     = "success"
	probeFailed      = "failed"
	probeUnsupported = "unsupported"
)

const (
	probePrompt        = "Reply with OK."
	probeReadCap       = 30 * time.Second
	probeConcurrency   = 4
	probeBodyLimit     = 8192
	probeRedacted      = "[redacted]"
	probeTruncatedMark = "...[truncated]"
)

// textProbeProtocols are the protocols a probe may exercise: they answer text
// and cannot create billable media output.
var textProbeProtocols = map[string]bool{
	ProtoOpenAIResponses: true, ProtoOpenRouterChat: true, ProtoOpenRouterResponses: true, ProtoOpenAIChat: true,
	ProtoAnthropicMessages: true, ProtoGoogleGenerate: true, ProtoGeminiInteractions: true, ProtoXAIResponses: true,
}

// probeTarget is a route prepared for probing.
type probeTarget struct {
	rec  routeRecord
	up   *Upstream
	pm   *PlatformModel
	task string
}

func (t probeTarget) base() ProbeResult {
	return ProbeResult{
		Protocol: t.rec.Protocol, Endpoint: protocolEndpoint(t.rec.Protocol),
		PlatformModelID: t.pm.ID, PlatformModelName: t.pm.Name,
		UpstreamID: t.up.ID, UpstreamName: t.up.Name,
		UpstreamModelID: t.rec.UpstreamModelID, UpstreamModelName: t.rec.UpstreamModelName,
		RouteID: t.rec.RouteID, BindingCode: t.rec.BindingCode,
	}
}

func failedProbe(res ProbeResult, code, msg string) ProbeResult {
	res.Success, res.Status, res.ErrorCode, res.ErrorMessage = false, probeFailed, code, msg
	return res
}

// deriveRouteTask picks the task a route is meant for when the caller gave none.
func deriveRouteTask(kinds []string, protocol string) string {
	switch {
	case containsString(kinds, KindImageGen) && protocolAllowedForKind(protocol, KindImageGen):
		return TaskImageGeneration
	case containsString(kinds, KindImageEdit) && protocolAllowedForKind(protocol, KindImageEdit):
		return TaskImageEdit
	}
	switch {
	case protocol == ProtoXAIVideoExtension:
		return TaskVideoExtension
	case protocolAllowedForKind(protocol, KindChat):
		return TaskChat
	case protocolAllowedForKind(protocol, KindImageGen):
		return TaskImageGeneration
	case protocolAllowedForKind(protocol, KindImageEdit):
		return TaskImageEdit
	case protocolAllowedForKind(protocol, KindVideoGen):
		return TaskVideoGeneration
	}
	switch {
	case containsString(kinds, KindImageGen):
		return TaskImageGeneration
	case containsString(kinds, KindImageEdit):
		return TaskImageEdit
	case containsString(kinds, KindVideoGen):
		return TaskVideoGeneration
	case containsString(kinds, KindVideoExtension):
		return TaskVideoExtension
	}
	return TaskChat
}

// preflight runs the local checks of a probe. It returns a finished result when
// the probe cannot (or must not) go to the network, otherwise the key and
// merged headers to use.
func (s *Service) preflight(t probeTarget, checkTask bool) (*ProbeResult, string) {
	res := t.base()
	p := t.rec.Protocol
	if !isImplementedProtocol(p) {
		r := failedProbe(res, "unsupported_protocol", "route protocol is not supported")
		return &r, ""
	}
	if checkTask && !RouteServesTask(parseStoredKinds(t.pm.KindsJSON), p, t.task) {
		r := failedProbe(res, "protocol_mismatch", "route protocol does not match the model type")
		return &r, ""
	}
	if !textProbeProtocols[p] {
		r := failedProbe(res, "unsupported_protocol", "this protocol requires media input or may create billable media output")
		r.Status = probeUnsupported
		return &r, ""
	}
	if strings.TrimSpace(t.rec.UpstreamModelName) == "" {
		r := failedProbe(res, "model_not_found", "upstream model name is empty")
		return &r, ""
	}
	if _, err := validateUpstreamBaseURL(t.up.BaseURL); err != nil {
		r := failedProbe(res, "config_invalid", "upstream base url is invalid")
		return &r, ""
	}
	doc, err := s.decryptKeys(t.up)
	if err != nil {
		r := failedProbe(res, "config_invalid", "upstream api keys are invalid")
		return &r, ""
	}
	active := doc.activeKeys()
	if len(active) == 0 {
		r := failedProbe(res, "no_active_key", "no active api key is configured")
		return &r, ""
	}
	return nil, active[0].Key
}

// classifyProbeError maps a transport or upstream failure to the public probe
// error vocabulary. The upstream's own text is never returned.
func classifyProbeError(err error) (code, msg string, status int) {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		status = ue.StatusCode
	}
	var ne net.Error
	switch {
	case errors.Is(err, ErrUnsupportedAdapter):
		return "unsupported_protocol", "route protocol is not supported", 0
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "timeout", "upstream request timed out", status
	case status == 401 || status == 403:
		return "auth_failed", "upstream authentication failed", status
	case status == 404:
		return "model_not_found", "upstream model or endpoint was not found", status
	case status == 408 || status == 504:
		return "timeout", "upstream request timed out", status
	case status == 429:
		return "rate_limited", "upstream rate limit reached", status
	case status == 400 || status == 422:
		return "request_invalid", "upstream rejected the test request", status
	case status >= 500:
		return "upstream_unavailable", "upstream service is unavailable", status
	case status > 0:
		return "upstream_request_failed", "upstream request failed", status
	}
	return "network_error", "upstream request failed", 0
}

// scrubber redacts secrets from a captured exchange.
type scrubber struct{ needles []string }

func newScrubber(baseURL, key string) scrubber {
	var needles []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			needles = append(needles, v)
		}
	}
	trimmed := strings.TrimSpace(baseURL)
	add(trimmed)
	add(strings.TrimRight(trimmed, "/"))
	if u, err := url.Parse(trimmed); err == nil {
		add(u.Host)
		add(u.Hostname())
	}
	add(key)
	// Longest first so a URL is replaced before the host inside it.
	for i := range needles {
		for j := i + 1; j < len(needles); j++ {
			if len(needles[j]) > len(needles[i]) {
				needles[i], needles[j] = needles[j], needles[i]
			}
		}
	}
	return scrubber{needles: needles}
}

func (sc scrubber) clean(v string) string {
	for _, n := range sc.needles {
		v = strings.ReplaceAll(v, n, probeRedacted)
	}
	return v
}

func (sc scrubber) body(v string) string {
	v = sc.clean(v)
	if r := []rune(v); len(r) > probeBodyLimit {
		v = string(r[:probeBodyLimit]) + probeTruncatedMark
	}
	return v
}

func (sc scrubber) headers(in map[string]string, allowed ...string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		for _, a := range allowed {
			if strings.EqualFold(k, a) {
				out[k] = sc.clean(v)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (sc scrubber) debug(d *ProbeDebug) *ProbeDebug {
	if d == nil {
		return nil
	}
	return &ProbeDebug{
		Request: ProbeDebugRequest{
			Method: sc.clean(d.Request.Method), Path: sc.clean(d.Request.Path),
			Headers: sc.headers(d.Request.Headers, "Content-Type", "Accept"), Body: sc.body(d.Request.Body),
		},
		Response: ProbeDebugResponse{
			StatusCode: d.Response.StatusCode, Headers: sc.headers(d.Response.Headers, "Content-Type"), Body: sc.body(d.Response.Body),
		},
	}
}

// execute sends the probe request. The target must have passed preflight.
func (s *Service) execute(ctx context.Context, t probeTarget, key string) ProbeResult {
	res := t.base()
	read := time.Duration(timeoutOrDefault(t.up.ReadTimeoutMS, defaultReadTimeoutMS)) * time.Millisecond
	if read > probeReadCap {
		read = probeReadCap
	}
	name, web := s.appAttribution()
	call := UpstreamCall{
		Protocol: t.rec.Protocol, BaseURL: strings.TrimSpace(t.up.BaseURL), APIKey: key,
		HeadersJSON:    mergeHeaders(t.up.HeadersJSON, t.rec.RouteHeaders),
		ConnectTimeout: time.Duration(timeoutOrDefault(t.up.ConnectTimeoutMS, defaultConnectTimeoutMS)) * time.Millisecond,
		ReadTimeout:    read, AppName: name, WebBaseURL: web,
	}
	req := ProbeRequest{UpstreamModel: t.rec.UpstreamModelName, Prompt: probePrompt, MaxTokens: 1, Temperature: 0, Task: t.task}
	sc := newScrubber(call.BaseURL, key)

	var (
		ex  *ProbeExchange
		err error
	)
	started := s.now()
	if s.opts.Client == nil {
		err = ErrUnsupportedAdapter
	} else {
		cctx, cancel := context.WithTimeout(ctx, read+call.ConnectTimeout)
		ex, err = s.opts.Client.Probe(cctx, call, req)
		cancel()
	}
	res.LatencyMS = s.now().Sub(started).Milliseconds()
	if err != nil {
		code, msg, status := classifyProbeError(err)
		res = failedProbe(res, code, msg)
		res.UpstreamStatusCode = status
		var ue *UpstreamError
		if errors.As(err, &ue) {
			res.Debug = sc.debug(ue.Debug)
		}
		return res
	}
	if ex != nil {
		res.Debug = sc.debug(ex.Debug)
		if ex.Incompatible {
			res = failedProbe(res, "response_incompatible", "upstream response format is incompatible")
			return res
		}
	}
	res.Success, res.Status = true, probeSuccess
	res.UpstreamStatusCode = http.StatusOK
	return res
}

// probeTargetsOf loads a model's active-chain routes in routing order.
func (s *Service) probeRoutesOf(ctx context.Context, pm *PlatformModel) ([]probeTarget, error) {
	recs, err := s.loadRoutes(ctx, []uint{pm.ID})
	if err != nil {
		return nil, err
	}
	ups := map[uint]*Upstream{}
	var out []probeTarget
	for _, r := range recs {
		if !r.activeChain() {
			continue
		}
		up := ups[r.UpstreamID]
		if up == nil {
			up, err = s.loadUpstream(ctx, s.db, r.UpstreamID)
			if err != nil {
				if errors.Is(err, ErrUpstreamNotFound) {
					continue
				}
				return nil, err
			}
			ups[r.UpstreamID] = up
		}
		out = append(out, probeTarget{rec: r, up: up, pm: pm})
	}
	return out, nil
}

func notFoundProbe(pm *PlatformModel, msg string) ProbeResult {
	return ProbeResult{
		Success: false, Status: probeFailed, ErrorCode: "route_not_found", ErrorMessage: msg,
		PlatformModelID: pm.ID, PlatformModelName: pm.Name,
	}
}

// ProbeRoute tests one route of an upstream.
func (s *Service) ProbeRoute(ctx context.Context, upstreamID, routeID uint, taskType string) (*ProbeResult, error) {
	up, err := s.loadUpstream(ctx, s.db, upstreamID)
	if err != nil {
		return nil, err
	}
	route, um, err := s.routeOfUpstream(ctx, s.db, upstreamID, routeID)
	if err != nil {
		return nil, err
	}
	pm, err := s.loadModel(ctx, s.db, route.PlatformModelID)
	if err != nil {
		if errors.Is(err, ErrModelNotFound) {
			return nil, ErrUpstreamModelNotFound
		}
		return nil, err
	}
	rec := routeRecord{
		RouteID: route.ID, PlatformModelID: pm.ID, UpstreamModelID: um.ID, Protocol: route.Protocol,
		RouteStatus: route.Status, RouteHeaders: route.HeadersJSON, UpstreamID: up.ID, BindingCode: um.BindingCode,
		UpstreamModelName: um.UpstreamModelName, UMStatus: um.Status, UpName: up.Name, UpBaseURL: up.BaseURL, UpStatus: up.Status,
	}
	t := probeTarget{rec: rec, up: up, pm: pm}
	if strings.TrimSpace(taskType) != "" {
		t.task = NormalizeTaskType(taskType)
	} else {
		t.task = deriveRouteTask(parseStoredKinds(pm.KindsJSON), route.Protocol)
	}
	res := s.runProbe(ctx, t, true)
	return &res, nil
}

func (s *Service) runProbe(ctx context.Context, t probeTarget, checkTask bool) ProbeResult {
	if early, key := s.preflight(t, checkTask); early != nil {
		return *early
	} else {
		return s.execute(ctx, t, key)
	}
}

// ProbeModel tests the first suitable route of a platform model.
func (s *Service) ProbeModel(ctx context.Context, modelID uint, taskType string) (*ProbeResult, error) {
	pm, err := s.loadModel(ctx, s.db, modelID)
	if err != nil {
		return nil, err
	}
	targets, err := s.probeRoutesOf(ctx, pm)
	if err != nil {
		return nil, err
	}
	kinds := parseStoredKinds(pm.KindsJSON)
	task := TaskChat
	if strings.TrimSpace(taskType) != "" {
		task = NormalizeTaskType(taskType)
	} else if containsString(kinds, KindImageGen) {
		task = TaskImageGeneration
	}
	if len(targets) == 0 {
		r := notFoundProbe(pm, "no active route is available for this model")
		return &r, nil
	}
	var firstFailure *ProbeResult
	matched := false
	for _, t := range targets {
		if !RouteServesTask(kinds, t.rec.Protocol, task) {
			continue
		}
		matched = true
		t.task = task
		early, key := s.preflight(t, false)
		if early != nil {
			if firstFailure == nil {
				firstFailure = early
			}
			continue
		}
		res := s.execute(ctx, t, key)
		return &res, nil
	}
	if firstFailure != nil {
		return firstFailure, nil
	}
	if !matched {
		r := notFoundProbe(pm, "no active route matches the selected test type")
		return &r, nil
	}
	r := notFoundProbe(pm, "no active route is available for this model")
	return &r, nil
}

// ProbeModelAll tests every suitable route of a platform model, at most four
// at a time, reporting in route order.
func (s *Service) ProbeModelAll(ctx context.Context, modelID uint, taskType string) (*ProbeAllResult, error) {
	pm, err := s.loadModel(ctx, s.db, modelID)
	if err != nil {
		return nil, err
	}
	targets, err := s.probeRoutesOf(ctx, pm)
	if err != nil {
		return nil, err
	}
	kinds := parseStoredKinds(pm.KindsJSON)
	explicit := strings.TrimSpace(taskType) != ""
	var picked []probeTarget
	for _, t := range targets {
		if explicit {
			t.task = NormalizeTaskType(taskType)
			if !RouteServesTask(kinds, t.rec.Protocol, t.task) {
				continue
			}
		} else {
			t.task = deriveRouteTask(kinds, t.rec.Protocol)
		}
		picked = append(picked, t)
	}
	if len(picked) == 0 {
		msg := "no active route is available for this model"
		if len(targets) > 0 {
			msg = "no active route matches the selected test type"
		}
		return summarizeProbes([]ProbeResult{notFoundProbe(pm, msg)}), nil
	}
	results := make([]ProbeResult, len(picked))
	sem := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for i := range picked {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.runProbe(ctx, picked[i], true)
		}(i)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return summarizeProbes(results), nil
}

func summarizeProbes(results []ProbeResult) *ProbeAllResult {
	out := &ProbeAllResult{TotalCount: len(results), Results: results}
	for _, r := range results {
		switch {
		case r.Success:
			out.SuccessCount++
		default:
			out.FailedCount++
			if r.Status == probeUnsupported {
				out.UnsupportedCount++
			}
		}
	}
	return out
}

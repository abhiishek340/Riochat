package trace

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Settings is the validated tracing configuration.
type Settings struct {
	Enabled      bool
	Endpoint     string // full URL of the traces endpoint
	Headers      map[string]string
	Insecure     bool
	Protocol     string // "grpc" or "http" (grpc is exported over OTLP/HTTP)
	SamplingRate float64
	ServiceName  string
	Resource     map[string]string
}

// ParseSettings validates raw tracing configuration. enabled is the optional
// tri-state switch: nil means "enabled iff an endpoint is configured".
func ParseSettings(enabled *bool, endpoint, headers string, insecure bool, protocol string, rate float64, appName string, getenv func(string) string) (Settings, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	endpoint = strings.TrimSpace(endpoint)
	s := Settings{Insecure: insecure, SamplingRate: clamp01(rate), Headers: ParseHeaders(headers)}
	if enabled != nil {
		s.Enabled = *enabled
	} else {
		s.Enabled = endpoint != ""
	}
	badProtocol := false
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "http", "http/protobuf", "http/json":
		s.Protocol = "http"
	case "", "grpc":
		s.Protocol = "grpc"
	default:
		s.Protocol = "grpc"
		badProtocol = true
	}
	s.ServiceName = strings.TrimSpace(appName)
	if s.ServiceName == "" {
		s.ServiceName = "riochat"
	}
	s.Resource = map[string]string{"service.name": s.ServiceName}
	if h, err := os.Hostname(); err == nil && h != "" {
		s.Resource["host.name"] = h
	}
	for env, key := range map[string]string{
		"KUBERNETES_NAMESPACE": "k8s.namespace.name",
		"KUBERNETES_POD_NAME":  "k8s.pod.name",
		"KUBERNETES_POD_UID":   "k8s.pod.uid",
	} {
		if v := strings.TrimSpace(getenv(env)); v != "" {
			s.Resource[key] = v
		}
	}
	if !s.Enabled {
		return s, nil
	}
	if endpoint == "" {
		return s, errors.New("otel exporter endpoint is required when tracing is enabled")
	}
	if badProtocol {
		return s, errors.New("unsupported otel exporter protocol")
	}
	u, err := normalizeEndpoint(endpoint, insecure)
	if err != nil {
		return s, err
	}
	s.Endpoint = u
	return s, nil
}

// ParseHeaders parses "k=v,k2=v2"; blank keys and malformed pairs are skipped.
func ParseHeaders(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

func normalizeEndpoint(ep string, insecure bool) (string, error) {
	if !strings.Contains(ep, "://") {
		scheme := "https://"
		if insecure {
			scheme = "http://"
		}
		ep = scheme + ep
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid otel exporter endpoint %q", ep)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String(), nil
}

// NewFromSettings builds a Tracer that exports to the configured endpoint, or
// a propagation-only Tracer when tracing is disabled.
func NewFromSettings(s Settings, client *http.Client) *Tracer {
	if !s.Enabled {
		return New(Options{SamplingRate: s.SamplingRate})
	}
	return New(Options{Exporter: NewOTLPExporter(s, client), SamplingRate: s.SamplingRate})
}

// OTLPExporter posts spans as OTLP/HTTP JSON.
type OTLPExporter struct {
	url     string
	headers map[string]string
	client  *http.Client
	res     map[string]string
}

// NewOTLPExporter returns an exporter for settings s.
func NewOTLPExporter(s Settings, client *http.Client) *OTLPExporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OTLPExporter{url: s.Endpoint, headers: s.Headers, client: client, res: s.Resource}
}

type kv struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func attrValue(v any) map[string]any {
	switch x := v.(type) {
	case string:
		return map[string]any{"stringValue": x}
	case bool:
		return map[string]any{"boolValue": x}
	case int:
		return map[string]any{"intValue": strconv.Itoa(x)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(x, 10)}
	case float64:
		return map[string]any{"doubleValue": x}
	default:
		return map[string]any{"stringValue": fmt.Sprint(v)}
	}
}

func encodeAttrs(in []Attr) []kv {
	out := make([]kv, 0, len(in))
	for _, a := range in {
		out = append(out, kv{a.Key, attrValue(a.Value)})
	}
	return out
}

// Encode renders spans as an OTLP/HTTP JSON export request.
func (e *OTLPExporter) Encode(spans []*SpanData) ([]byte, error) {
	var resAttrs []Attr
	for k, v := range e.res {
		resAttrs = append(resAttrs, Attr{k, v})
	}
	list := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		m := map[string]any{
			"traceId":           hex.EncodeToString(s.Context.TraceID[:]),
			"spanId":            hex.EncodeToString(s.Context.SpanID[:]),
			"name":              s.Name,
			"kind":              int(s.Kind),
			"startTimeUnixNano": strconv.FormatInt(s.Start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(s.End.UnixNano(), 10),
			"attributes":        encodeAttrs(s.Attrs),
		}
		if s.ParentID != [8]byte{} {
			m["parentSpanId"] = hex.EncodeToString(s.ParentID[:])
		}
		if s.Failed {
			m["status"] = map[string]any{"code": 2, "message": s.Message}
		}
		list = append(list, m)
	}
	doc := map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": encodeAttrs(resAttrs)},
		"scopeSpans": []any{map[string]any{
			"scope": map[string]any{"name": "riochat"},
			"spans": list,
		}},
	}}}
	return json.Marshal(doc)
}

// Export implements Exporter.
func (e *OTLPExporter) Export(ctx context.Context, spans []*SpanData) error {
	if len(spans) == 0 {
		return nil
	}
	body, err := e.Encode(spans)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("otlp export: status %d", resp.StatusCode)
	}
	return nil
}

// Shutdown implements Exporter.
func (e *OTLPExporter) Shutdown(context.Context) error {
	e.client.CloseIdleConnections()
	return nil
}

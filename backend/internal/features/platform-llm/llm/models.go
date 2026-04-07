package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ListModels lists the provider's models as {id, owned_by}. Anthropic and
// Google listings fall back to the OpenAI-compatible form of the same base URL
// (except on OpenRouter); if both fail the combined error is returned.
func (c *Client) ListModels(ctx context.Context, route Route) ([]Model, error) {
	x, _, err := c.newExchange(route, &Request{}, false)
	if err != nil {
		return nil, err
	}
	switch x.proto {
	case AnthropicMessages:
		models, err := c.listAnthropic(ctx, x)
		return c.withFallback(ctx, route, models, err)
	case GoogleGenerateContent, GoogleImageGeneration, GeminiInteractions:
		models, err := c.listGoogle(ctx, x)
		return c.withFallback(ctx, route, models, err)
	case OpenRouterImages:
		return c.listOpenAIStyle(ctx, x, "/images/models")
	}
	return c.listOpenAIStyle(ctx, x, "/models")
}

func (c *Client) withFallback(ctx context.Context, route Route, models []Model, err error) ([]Model, error) {
	if err == nil {
		return models, nil
	}
	if u, perr := url.Parse(route.BaseURL); perr == nil && isOpenRouterHost(u.Hostname()) {
		return nil, err
	}
	fr := route
	fr.Protocol = string(OpenAIChatCompletions)
	if proto, _ := NormalizeProtocol(route.Protocol); proto.isGoogle() || proto == GeminiInteractions {
		fr.BaseURL = googleBase(route.BaseURL)
	}
	fx, _, nerr := c.newExchange(fr, &Request{}, false)
	if nerr != nil {
		return nil, err
	}
	// Keep the original key header convention working on proxies.
	out, ferr := c.listOpenAIStyle(ctx, fx, "/models")
	if ferr == nil {
		return out, nil
	}
	return nil, &Error{Kind: KindUpstream, Status: statusOf(ferr, statusOf(err, 0)),
		Message: fmt.Sprintf("model listing failed: %v; fallback failed: %v", err, ferr), Cause: errors.Join(err, ferr)}
}

func statusOf(err error, def int) int {
	if e, ok := AsError(err); ok && e.Status != 0 {
		return e.Status
	}
	return def
}

func (c *Client) getJSON(ctx context.Context, x *exchange, u string) (map[string]any, error) {
	_, body, _, _, err := c.doBuffered(ctx, x, &call{method: http.MethodGet, url: u}, false)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Kind: KindParse, Message: "upstream returned an invalid JSON body", Cause: err}
	}
	return m, nil
}

func (c *Client) listOpenAIStyle(ctx context.Context, x *exchange, endpoint string) ([]Model, error) {
	u, err := x.apiURL("v1", endpoint)
	if err != nil {
		return nil, err
	}
	m, err := c.getJSON(ctx, x, u)
	if err != nil {
		return nil, err
	}
	var out []Model
	for _, it := range asList(m["data"]) {
		im, _ := it.(map[string]any)
		id := strings.TrimSpace(strOf(im["id"]))
		if id == "" {
			continue
		}
		out = append(out, Model{ID: id, OwnedBy: strOf(im["owned_by"])})
	}
	return out, nil
}

func (c *Client) listAnthropic(ctx context.Context, x *exchange) ([]Model, error) {
	var out []Model
	after := ""
	seen := map[string]bool{}
	for {
		base, err := x.apiURL("v1", "/models")
		if err != nil {
			return nil, err
		}
		q := url.Values{"limit": {"1000"}}
		if after != "" {
			q.Set("after_id", after)
		}
		m, err := c.getJSON(ctx, x, base+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		for _, it := range asList(m["data"]) {
			im, _ := it.(map[string]any)
			if id := strings.TrimSpace(strOf(im["id"])); id != "" {
				out = append(out, Model{ID: id, OwnedBy: "anthropic"})
			}
		}
		more, _ := m["has_more"].(bool)
		if !more {
			return out, nil
		}
		last := strOf(m["last_id"])
		if last == "" {
			return nil, &Error{Kind: KindParse, Message: "model listing claims more pages but gave no last_id"}
		}
		if seen[last] {
			return nil, &Error{Kind: KindParse, Message: "model listing repeated a pagination cursor"}
		}
		seen[last] = true
		after = last
	}
}

func (c *Client) listGoogle(ctx context.Context, x *exchange) ([]Model, error) {
	var out []Model
	token := ""
	seen := map[string]bool{}
	for {
		base, err := buildURL(googleBase(x.route.BaseURL), "v1beta", "/models")
		if err != nil {
			return nil, err
		}
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		m, err := c.getJSON(ctx, x, base+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		for _, it := range asList(m["models"]) {
			im, _ := it.(map[string]any)
			id := strings.TrimPrefix(strings.TrimSpace(strOf(im["name"])), "models/")
			if id != "" {
				out = append(out, Model{ID: id, OwnedBy: "google"})
			}
		}
		next := strOf(m["nextPageToken"])
		if next == "" {
			return out, nil
		}
		if seen[next] {
			return nil, &Error{Kind: KindParse, Message: "model listing repeated a pagination cursor"}
		}
		seen[next] = true
		token = next
	}
}

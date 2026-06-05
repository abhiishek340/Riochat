package personalprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Timeouts of a catalog probe.
const (
	probeConnectTimeout = 10 * time.Second
	probeTotalTimeout   = 20 * time.Second
	maxCatalogBytes     = 8 << 20
	maxCatalogPages     = 5
	anthropicVersion    = "2023-06-01"
)

// httpLister is the default ModelLister: a plain GET of the provider's model
// listing with the credentials of the provider's protocol family.
type httpLister struct {
	client *http.Client
}

// NewHTTPLister returns a ModelLister whose connections are checked against
// policy's address rules at connect time (after DNS resolution), that never
// follows redirects and that never puts upstream text into errors.
func NewHTTPLister(policy OutboundPolicy) ModelLister {
	return &httpLister{client: newGuardedClient(policy, forbiddenIP)}
}

// newGuardedClient builds the HTTP client; deny decides which resolved
// addresses are refused (tests pass a laxer function to reach a local server).
func newGuardedClient(policy OutboundPolicy, deny func(net.IP) bool) *http.Client {
	dialer := &net.Dialer{Timeout: probeConnectTimeout}
	tr := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   probeConnectTimeout,
		ResponseHeaderTimeout: probeTotalTimeout,
		DisableKeepAlives:     true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if policy != nil {
				if err := policy.CheckHost(strings.ToLower(host)); err != nil && net.ParseIP(host) == nil {
					return nil, ErrBlockedHost
				}
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("no addresses")
			}
			for _, ip := range ips {
				if deny(ip.IP) {
					return nil, ErrBlockedHost
				}
			}
			var last error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return &http.Client{
		Transport: tr,
		Timeout:   probeTotalTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ListModels implements ModelLister.
func (l *httpLister) ListModels(ctx context.Context, req ListRequest) ([]string, error) {
	switch req.Protocol {
	case ProtoGoogleGenerate, ProtoGeminiInteract:
		return l.listGoogle(ctx, req)
	default:
		return l.listData(ctx, req)
	}
}

func (l *httpLister) get(ctx context.Context, rawURL string, headers map[string]string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, probeTotalTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := l.client.Do(r)
	if err != nil {
		// The transport error may quote the URL; keep only its category.
		return errors.New("upstream request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &UpstreamStatusError{Status: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCatalogBytes)).Decode(dst); err != nil {
		return errors.New("upstream answer is not a model list")
	}
	return nil
}

// listData handles the OpenAI-style listing {"data":[{"id":...}]} that
// OpenAI, OpenRouter, xAI and Anthropic share.
func (l *httpLister) listData(ctx context.Context, req ListRequest) ([]string, error) {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}
	target := strings.TrimRight(req.BaseURL, "/") + "/models"
	if req.Protocol == ProtoAnthropic {
		headers = map[string]string{"x-api-key": req.APIKey, "anthropic-version": anthropicVersion}
		target += "?limit=1000"
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := l.get(ctx, target, headers, &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// listGoogle handles {"models":[{"name":"models/x"}],"nextPageToken":...}.
func (l *httpLister) listGoogle(ctx context.Context, req ListRequest) ([]string, error) {
	headers := map[string]string{"x-goog-api-key": req.APIKey}
	var out []string
	token := ""
	for page := 0; page < maxCatalogPages; page++ {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var body struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := l.get(ctx, strings.TrimRight(req.BaseURL, "/")+"/models?"+q.Encode(), headers, &body); err != nil {
			return nil, err
		}
		for _, m := range body.Models {
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
		if body.NextPageToken == "" {
			break
		}
		token = body.NextPageToken
	}
	return out, nil
}

package llm

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultGoogleBase  = "https://generativelanguage.googleapis.com"
	defaultGoogleModel = "gemini-2.0-flash"
)

var versionSegment = regexp.MustCompile(`^v\d+((alpha|beta|preview)\d*)?$`)

// buildURL joins a configured base URL and a protocol endpoint. endpoint is
// the path after the version prefix (for example "/chat/completions"). If the
// base's last path segment already looks like an API version the endpoint is
// appended directly, otherwise defaultVersion ("v1" or "v1beta") is inserted.
func buildURL(base, defaultVersion, endpoint string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", invalid(ErrInvalidBaseURL, "invalid base url")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", invalid(ErrInvalidBaseURL, "invalid base url")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/")
	}
	last := u.Path
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	if !versionSegment.MatchString(last) {
		u.Path += "/" + defaultVersion
		if u.RawPath != "" {
			u.RawPath += "/" + defaultVersion
		}
	}
	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	u.Path += endpoint
	if u.RawPath != "" {
		u.RawPath += endpoint
	}
	return u.String(), nil
}

// googleBase returns the Google base URL: the default when empty, and a
// trailing "/v1" removed so OpenAI-style proxy URLs work.
func googleBase(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return defaultGoogleBase
	}
	if strings.HasSuffix(b, "/v1") {
		b = strings.TrimSuffix(b, "/v1")
	}
	return b
}

// googleModelPath returns "models/<name>" (or a qualified collection path).
func googleModelPath(model string) string {
	m := strings.Trim(strings.TrimSpace(model), "/")
	if m == "" {
		m = defaultGoogleModel
	}
	if !strings.Contains(m, "/") {
		m = "models/" + m
	}
	return m
}

// ---- custom headers -------------------------------------------------------

// headerVars returns the template variables available for a request.
func headerVars(req *Request) map[string]string {
	return map[string]string{
		"RIOCHAT_CONVERSATION_ID":     req.ConversationID,
		"RIOCHAT_SESSION_ID":          req.SessionKey,
		"RIOCHAT_REQUEST_ID":          req.RequestID,
		"RIOCHAT_UPSTREAM_REQUEST_ID": uuid.NewString(),
	}
}

var templateRe = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// expandTemplate substitutes ${NAME} placeholders. It reports false when a
// known placeholder has no value, in which case the header must be omitted.
// Unknown placeholders are left as written.
func expandTemplate(v string, vars map[string]string) (string, bool) {
	ok := true
	out := templateRe.ReplaceAllStringFunc(v, func(m string) string {
		name := templateRe.FindStringSubmatch(m)[1]
		val, known := vars[name]
		if !known {
			return m
		}
		if strings.TrimSpace(val) == "" {
			ok = false
			return ""
		}
		return val
	})
	return out, ok
}

type headerPair struct{ Name, Value string }

// customHeaders parses the route's header JSON. Invalid JSON, blank names or
// values, and headers whose template cannot be filled are skipped.
func customHeaders(raw string, vars map[string]string) []headerPair {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	var out []headerPair
	for k, v := range m {
		name := strings.TrimSpace(k)
		s, isStr := v.(string)
		if !isStr {
			if v == nil {
				continue
			}
			b, _ := json.Marshal(v)
			s = string(b)
		}
		s = strings.TrimSpace(s)
		if name == "" || s == "" {
			continue
		}
		val, ok := expandTemplate(s, vars)
		if !ok || strings.TrimSpace(val) == "" {
			continue
		}
		out = append(out, headerPair{name, val})
	}
	return out
}

func isOpenRouterHost(host string) bool {
	h := strings.ToLower(host)
	return h == "openrouter.ai" || strings.HasSuffix(h, ".openrouter.ai")
}

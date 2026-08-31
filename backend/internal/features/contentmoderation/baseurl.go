package contentmoderation

import (
	"errors"
	"net/url"
	"strings"
)

// DefaultBaseURL is the moderation service used when none is configured.
const DefaultBaseURL = "https://api.openai.com/v1"

// ErrInvalidBaseURL is returned by ModerationEndpoint for an unusable address.
var ErrInvalidBaseURL = errors.New("invalid moderation base url")

// ModerationEndpoint turns a configured base address into the full moderation
// endpoint URL.
//
//   - A missing scheme gets "https://".
//   - Only http and https hosts without embedded credentials are accepted.
//   - A path already ending in /moderations is kept, one ending in /v1 gets
//     /moderations appended, an empty path becomes /v1/moderations and
//     anything else gets /v1/moderations appended.
//   - Query string and fragment are dropped.
func ModerationEndpoint(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrInvalidBaseURL
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", ErrInvalidBaseURL
	}
	if u.Host == "" || u.Hostname() == "" || u.User != nil {
		return "", ErrInvalidBaseURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ErrInvalidBaseURL
	}
	path := strings.TrimRight(u.Path, "/")
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, "/moderations"):
	case strings.HasSuffix(lower, "/v1"):
		path += "/moderations"
	case path == "":
		path = "/v1/moderations"
	default:
		path += "/v1/moderations"
	}
	out := url.URL{Scheme: scheme, Host: u.Host, Path: path}
	return out.String(), nil
}

// ValidateBaseURL reports whether raw is an acceptable base address.
func ValidateBaseURL(raw string) error {
	_, err := ModerationEndpoint(raw)
	return err
}

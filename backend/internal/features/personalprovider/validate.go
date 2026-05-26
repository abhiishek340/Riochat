package personalprovider

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits shared by validation and the data model.
const (
	maxNameRunes     = 64
	maxIconLen       = 64
	maxBaseURLLen    = 512
	maxAPIKeyBytes   = 512
	maxModelNameLen  = 100
	maxModelsPerReq  = 200
	maxHostLen       = 255
	maxLastErrorLen  = 200
	maxBlockedHosts  = 4096
	minPublicIDLen   = 8
	maxPublicIDLen   = 32
	generatedIDLen   = 16
	referencePrefix  = "personal:"
	keyHintMask      = "••••"
	minKeyForPartial = 12
)

// ---- public ids and model references -----------------------------------------

func validPublicID(id string) bool {
	if len(id) < minPublicIDLen || len(id) > maxPublicIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func newPublicID() (string, error) {
	buf := make([]byte, generatedIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, generatedIDLen)
	for i, b := range buf {
		out[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(out), nil
}

// IsReference reports whether a model name lies in the reserved personal
// namespace (it need not be well formed).
func IsReference(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), referencePrefix)
}

// FormatReference builds "personal:<providerPublicID>/<upstreamModel>".
func FormatReference(publicID, model string) string {
	return referencePrefix + publicID + "/" + model
}

// ParseReference splits a model reference. Only the first slash separates the
// provider id from the model name, which may itself contain slashes.
func ParseReference(ref string) (publicID, model string, ok bool) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, referencePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, referencePrefix)
	i := strings.Index(rest, "/")
	if i < 0 {
		return "", "", false
	}
	publicID, model = rest[:i], rest[i+1:]
	if !validPublicID(publicID) || !validModelName(model) {
		return "", "", false
	}
	return publicID, model, true
}

// ---- names, icons, keys -------------------------------------------------------

func validModelName(name string) bool {
	if name == "" || len(name) > maxModelNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x21 || name[i] == 0x7f {
			return false
		}
	}
	return true
}

// normalizeName trims and checks a provider name. An empty result is returned
// as is so the caller decides whether a default applies.
func normalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

// normalizeIcon lower-cases and checks an icon slug. Empty is valid.
func normalizeIcon(raw string) (string, error) {
	icon := strings.ToLower(strings.TrimSpace(raw))
	if icon == "" {
		return "", nil
	}
	if len(icon) > maxIconLen || icon[0] == '-' {
		return "", ErrInvalidIcon
	}
	for i := 0; i < len(icon); i++ {
		c := icon[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "", ErrInvalidIcon
		}
	}
	return icon, nil
}

// normalizeAPIKey trims the key and requires 1..512 bytes of visible ASCII.
func normalizeAPIKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > maxAPIKeyBytes {
		return "", ErrInvalidAPIKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return "", ErrInvalidAPIKey
		}
	}
	return key, nil
}

// keyHint is the masked rendition of a key that is safe to show.
func keyHint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) < minKeyForPartial {
		return keyHintMask
	}
	return string(r[:3]) + keyHintMask + string(r[len(r)-4:])
}

func keyBinding(userID uint, publicID string) string {
	return "personal_provider.api_key:" + strconv.FormatUint(uint64(userID), 10) + ":" + publicID
}

// ---- host names and the deny-list ---------------------------------------------

// validHostname checks a bare DNS name (or IPv4 literal): dot separated labels
// of letters, digits and inner hyphens.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func splitHostList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

// ValidateBlockedHosts is the validator of the blocked_hosts setting: at most
// 4096 characters, every entry (after an optional "*." prefix) a hostname.
func ValidateBlockedHosts(raw string) error {
	if utf8.RuneCountInString(raw) > maxBlockedHosts {
		return fmt.Errorf("blocked_hosts is longer than %d characters", maxBlockedHosts)
	}
	for _, e := range splitHostList(raw) {
		h := strings.TrimPrefix(e, "*.")
		h = strings.TrimSuffix(h, ".")
		if !validHostname(h) {
			return fmt.Errorf("blocked_hosts entry %q is not a valid host name", e)
		}
	}
	return nil
}

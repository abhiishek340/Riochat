package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zone names must resolve on minimal images too
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ---- randomness and digests ------------------------------------------------

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: crypto/rand failed: %v", err))
	}
	return b
}

func randomURLToken(n int) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(n))
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

func newPublicID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pkceChallenge is the S256 transform of a PKCE verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func constEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// randomDigits returns n uniformly random decimal digits.
func randomDigits(n int) string {
	var sb strings.Builder
	ten := big.NewInt(10)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, ten)
		if err != nil {
			panic(fmt.Sprintf("auth: crypto/rand failed: %v", err))
		}
		sb.WriteByte(byte('0' + v.Int64()))
	}
	return sb.String()
}

func isSet(t *time.Time) bool { return t != nil && !t.IsZero() }

// ---- email ----------------------------------------------------------------

// normalizeEmail trims and lowercases an address and requires it to be a single
// plain address of at most 128 characters.
func normalizeEmail(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || utf8.RuneCountInString(s) > 128 || strings.ContainsAny(s, "\x00\r\n") {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return "", ErrInvalidEmail
	}
	return s, nil
}

func emailParts(email string) (local, domain string) {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email, ""
	}
	return email[:at], email[at+1:]
}

// allowedDomains parses the registration allow-list setting.
func allowedDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "@"))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// checkEmailPolicy applies the alias and domain restrictions to a normalized
// address.
func checkEmailPolicy(email string, s Settings) error {
	local, domain := emailParts(email)
	if s.EmailBlockPlusAlias && strings.Contains(local, "+") {
		return ErrEmailAliasNotAllowed
	}
	if list := allowedDomains(s.EmailAllowedDomains); len(list) > 0 {
		for _, d := range list {
			if d == domain {
				return nil
			}
		}
		return ErrEmailDomainNotAllowed
	}
	return nil
}

// ---- passwords --------------------------------------------------------------

// checkPasswordPolicy returns the trimmed password when it is acceptable.
func checkPasswordPolicy(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(p)
	if n < 8 || n > 128 {
		return "", ErrInvalidPassword
	}
	digitsOnly := true
	for _, r := range p {
		if r < '0' || r > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		return "", ErrInvalidPassword
	}
	return p, nil
}

// ---- names ------------------------------------------------------------------

var reservedUsernames = map[string]bool{
	"admin": true, "api": true, "auth": true, "billing": true, "chat": true,
	"riochat": true, "rio-chat": true, "files": true, "help": true, "me": true,
	"root": true, "settings": true, "support": true, "system": true, "user": true,
	"users": true,
}

// normalizeUsername validates a self-service username.
func normalizeUsername(raw string) (string, error) {
	u := strings.ToLower(strings.TrimSpace(raw))
	n := utf8.RuneCountInString(u)
	if n < 3 || n > 16 {
		return "", ErrInvalidUsername
	}
	for _, r := range u {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return "", ErrInvalidUsername
		}
	}
	first, last := u[0], u[len(u)-1]
	if first == '_' || first == '-' || last == '_' || last == '-' {
		return "", ErrInvalidUsername
	}
	if reservedUsernames[u] {
		return "", ErrInvalidUsername
	}
	return u, nil
}

var nonSlugRun = regexp.MustCompile(`[^a-z0-9_-]+`)

// slugify lowercases s, turns spaces into dashes, collapses runs of other
// characters into one dash and trims dashes and underscores from the ends.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = nonSlugRun.ReplaceAllString(s, "-")
	return strings.Trim(s, "-_")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// generatedUsername builds "<slug7>-<hash8>" from a slug source and a hash input.
func generatedUsername(slugSource, fallback, hashInput string) string {
	base := slugify(slugSource)
	if base == "" {
		base = fallback
	}
	return truncateRunes(base, 7) + "-" + sha256Hex(hashInput)[:8]
}

// suffixedUsername appends -n to base, shortening base so the result fits 16
// characters.
func suffixedUsername(base string, n int) string {
	suffix := fmt.Sprintf("-%d", n)
	return truncateRunes(base, 16-len(suffix)) + suffix
}

// normalizeDisplayName cuts the text to 16 characters and pads it to 3 with
// underscores. Empty input becomes "user".
func normalizeDisplayName(src string) string {
	s := strings.TrimSpace(src)
	if s == "" {
		s = "user"
	}
	s = truncateRunes(s, 16)
	for utf8.RuneCountInString(s) < 3 {
		s += "_"
	}
	return s
}

// ---- profile field validation -----------------------------------------------

func validTimeZone(name string) bool {
	if name == "" || strings.EqualFold(name, "local") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func normalizeLocale(raw string) (string, error) {
	l := strings.TrimSpace(raw)
	if l == "" {
		return "en-US", nil
	}
	l = strings.ReplaceAll(l, "_", "-")
	switch l {
	case "en", "en-US":
		return l, nil
	}
	return "", ErrInvalidLocale
}

var (
	appearanceAllowed = map[string]map[string]bool{
		"preset":         set("default", "azure", "cobalt", "graphite", "lagoon", "ink", "ochre", "sepia"),
		"chatFont":       set("default", "songti", "heiti", "mono"),
		"chatFontWeight": set("regular", "medium", "semibold", "bold"),
		"fontSize":       set("small", "standard", "medium", "large"),
	}
	fileIDPattern = regexp.MustCompile(`^file_[A-Za-z0-9_-]*$`)
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

// normalizeAppearance validates and re-serializes appearance preferences.
func normalizeAppearance(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(s), &generic); err != nil || generic == nil {
		return "", ErrInvalidAppearance
	}
	out := make(map[string]string, len(generic))
	for k, v := range generic {
		allowed, ok := appearanceAllowed[k]
		if !ok {
			return "", ErrInvalidAppearance
		}
		str, ok := v.(string)
		if !ok {
			return "", ErrInvalidAppearance
		}
		if !allowed[str] {
			if k == "fontSize" {
				str = "standard"
			} else {
				return "", ErrInvalidAppearance
			}
		}
		out[k] = str
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", ErrInvalidAppearance
	}
	return string(b), nil
}

// avatarShape classifies an avatar value. fileID is non-empty for file: refs.
func avatarShape(raw string) (ok bool, fileID string) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return true, ""
	case strings.HasPrefix(v, "/"), strings.HasPrefix(v, "generated:github:"):
		return true, ""
	case strings.HasPrefix(v, "file:"):
		id := strings.TrimPrefix(v, "file:")
		if len(id) > 128 || !fileIDPattern.MatchString(id) {
			return false, ""
		}
		return true, id
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false, ""
	}
	return true, ""
}

// ---- misc -------------------------------------------------------------------

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func trimmedPtr(p *string) (string, bool) {
	if p == nil {
		return "", false
	}
	return strings.TrimSpace(*p), true
}

// asTyped reports whether err carries one of this area's typed errors.
func asTyped(err error) (*apperr.Error, bool) { return apperr.Find(err) }

func itoa(n int) string { return strconv.Itoa(n) }

func asLocked(err error, target **lockedError) bool { return errors.As(err, target) }

func isNotFoundErr(err error) bool { return database.IsNotFound(err) }

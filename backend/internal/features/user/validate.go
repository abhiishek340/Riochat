package user

import (
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	// Embed the IANA database so zone validation does not depend on the host.
	_ "time/tzdata"
)

// Field limits.
const (
	usernameMin    = 3
	usernameMax    = 16
	displayNameMin = 3
	displayNameMax = 16
	passwordMin    = 8
	passwordMax    = 128
	phoneDigitsMin = 6
	phoneDigitsMax = 20

	// DefaultLocale and DefaultTimezone are the values used for blank input.
	DefaultLocale   = "en-US"
	DefaultTimezone = "Etc/UTC"

	// AvatarFilePrefix marks an internal upload reference.
	AvatarFilePrefix = "file:"
	// AvatarGeneratedPrefix marks a generated avatar.
	AvatarGeneratedPrefix = "generated:github:"

	fileIDPrefix = "file_"
	fileIDMax    = 128

	// Stored column widths that the rules above do not bound by themselves.
	emailMax  = 128
	avatarMax = 2048
)

var reservedUsernames = map[string]struct{}{
	"admin": {}, "api": {}, "auth": {}, "billing": {}, "chat": {}, "riochat": {},
	"rio-chat": {}, "files": {}, "help": {}, "me": {}, "root": {}, "settings": {},
	"support": {}, "system": {}, "user": {}, "users": {},
}

// NormalizeUsername trims and lowercases a login name and enforces the rules
// for length, characters, edge characters and reserved words.
func NormalizeUsername(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	n := utf8.RuneCountInString(name)
	if n < usernameMin || n > usernameMax {
		return "", ErrInvalidUsername
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return "", ErrInvalidUsername
		}
	}
	if edge := name[0]; edge == '-' || edge == '_' {
		return "", ErrInvalidUsername
	}
	if edge := name[len(name)-1]; edge == '-' || edge == '_' {
		return "", ErrInvalidUsername
	}
	if _, reserved := reservedUsernames[name]; reserved {
		return "", ErrInvalidUsername
	}
	return name, nil
}

// NormalizeDisplayName trims a display name and checks its length in Unicode
// characters.
func NormalizeDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n < displayNameMin || n > displayNameMax {
		return "", ErrInvalidDisplayName
	}
	return name, nil
}

// GeneratedDisplayName derives a valid display name from arbitrary text, for
// flows that take a name from an external provider. It never fails.
func GeneratedDisplayName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		name = "user"
	}
	if r := []rune(name); len(r) > displayNameMax {
		name = string(r[:displayNameMax])
	}
	for utf8.RuneCountInString(name) < displayNameMin {
		name += "_"
	}
	return name
}

// NormalizePassword trims a password and checks its length and composition. The
// trimmed text is what must be hashed and stored.
func NormalizePassword(raw string) (string, error) {
	pw := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(pw)
	if n < passwordMin || n > passwordMax {
		return "", ErrInvalidPassword
	}
	digitsOnly := true
	for _, r := range pw {
		if !unicode.IsDigit(r) {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		return "", ErrInvalidPassword
	}
	return pw, nil
}

// NormalizeEmail accepts a blank value (meaning unset) or a bare address. The
// case is preserved.
func NormalizeEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" {
		return "", nil
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email || utf8.RuneCountInString(email) > emailMax {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// NormalizePhone accepts a blank value or a phone number made of digits, an
// optional leading plus, and separators (spaces, hyphens, parentheses) which
// are stripped.
func NormalizePhone(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	if phone == "" {
		return "", nil
	}
	var b strings.Builder
	digits := 0
	for i, r := range phone {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			digits++
		case r == '+' && i == 0:
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return "", ErrInvalidPhone
		}
	}
	if digits < phoneDigitsMin || digits > phoneDigitsMax {
		return "", ErrInvalidPhone
	}
	return b.String(), nil
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return s != ""
}

// NormalizeLocale canonicalizes a locale such as "fr_fr" to "fr-FR". A blank
// value becomes the default locale.
func NormalizeLocale(raw string) (string, error) {
	loc := strings.TrimSpace(raw)
	if loc == "" {
		return DefaultLocale, nil
	}
	parts := strings.Split(strings.ReplaceAll(loc, "_", "-"), "-")
	if len(parts) > 2 {
		return "", ErrInvalidLocale
	}
	lang := parts[0]
	if len(lang) < 2 || len(lang) > 3 || !isASCIILetters(lang) {
		return "", ErrInvalidLocale
	}
	out := strings.ToLower(lang)
	if len(parts) == 2 {
		region := parts[1]
		if len(region) != 2 || !isASCIILetters(region) {
			return "", ErrInvalidLocale
		}
		out += "-" + strings.ToUpper(region)
	}
	return out, nil
}

// NormalizeTimezone checks that the zone resolves in the IANA database. A blank
// value becomes the default zone. The trimmed name is returned as given.
func NormalizeTimezone(raw string) (string, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return DefaultTimezone, nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", ErrInvalidTimeZone
	}
	return tz, nil
}

// ---- avatar references ----------------------------------------------------

var fileIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidUploadID reports whether id is a well-formed upload id.
func ValidUploadID(id string) bool {
	return strings.HasPrefix(id, fileIDPrefix) && len(id) <= fileIDMax && fileIDPattern.MatchString(id)
}

// AvatarRefForUpload builds the stored avatar reference of an upload.
func AvatarRefForUpload(uploadID string) string { return AvatarFilePrefix + uploadID }

// UploadIDFromAvatarRef extracts the upload id from a stored avatar value. ok is
// false unless the value is a well-formed "file:" reference.
func UploadIDFromAvatarRef(ref string) (id string, ok bool) {
	rest, found := strings.CutPrefix(ref, AvatarFilePrefix)
	if !found || !ValidUploadID(rest) {
		return "", false
	}
	return rest, true
}

// ValidAvatarRef reports whether s is an acceptable stored avatar value: empty,
// a root-relative path, a generated marker, an internal file reference or an
// absolute http(s) URL with a host. The value is checked exactly as given.
func ValidAvatarRef(s string) bool {
	switch {
	case s == "":
		return true
	case utf8.RuneCountInString(s) > avatarMax || strings.ContainsRune(s, 0):
		// The column holds 2048 characters and PostgreSQL text cannot hold NUL.
		return false
	case strings.HasPrefix(s, AvatarFilePrefix):
		_, ok := UploadIDFromAvatarRef(s)
		return ok
	case strings.HasPrefix(s, AvatarGeneratedPrefix):
		return true
	case strings.HasPrefix(s, "/"):
		// A scheme-relative "//host/x" would point off-site, and a backslash is
		// treated like a slash by browsers.
		return !strings.HasPrefix(s, "//") && !strings.Contains(s, `\`)
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != ""
}

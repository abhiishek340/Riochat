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

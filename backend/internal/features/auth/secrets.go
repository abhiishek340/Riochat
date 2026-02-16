package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---- passwords --------------------------------------------------------------

// bcrypt only looks at the first 72 bytes. Longer passwords are folded through
// SHA-256 first so every character still counts.
func prepPassword(plain string) []byte {
	if len(plain) <= 72 {
		return []byte(plain)
	}
	sum := sha256.Sum256([]byte(plain))
	return []byte(base64.StdEncoding.EncodeToString(sum[:]))
}

func hashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(prepPassword(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func checkPassword(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), prepPassword(plain)) == nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// burnPasswordCheck spends the same time as a real comparison so that unknown
// accounts cannot be told apart by latency.
func burnPasswordCheck(plain string) {
	dummyOnce.Do(func() { dummyHash, _ = hashPassword("riochat-timing-equalizer") })
	_ = checkPassword(dummyHash, plain)
}

// ---- keyed hashes -----------------------------------------------------------

func (s *Service) hmacHex(parts ...string) string {
	m := hmac.New(sha256.New, []byte(s.cfg().Security.JWTSecret))
	m.Write([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(m.Sum(nil))
}

// ---- TOTP (RFC 6238, SHA-1, 30 s, 6 digits) -----------------------------------

const (
	totpPeriod = 30
	totpDigits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string { return b32.EncodeToString(randomBytes(20)) }

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(buf[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%0*d", totpDigits, bin%1000000)
}

// totpAt returns the code for the instant t.
func totpAt(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(t.Unix()/totpPeriod)), nil
}

// verifyTOTP accepts the current step and one step either side.
func verifyTOTP(secret, code string, now time.Time) bool {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return false
	}
	step := now.Unix() / totpPeriod
	ok := false
	for d := int64(-1); d <= 1; d++ {
		if step+d < 0 {
			continue
		}
		if constEq(hotp(key, uint64(step+d)), code) {
			ok = true
		}
	}
	return ok
}

func otpauthURL(account, secret string) string {
	label := url.PathEscape("Riochat") + ":" + url.PathEscape(account)
	q := url.Values{"secret": {secret}, "issuer": {"Riochat"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	// Encode sorts the parameters by name, which is the order clients have always seen.
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ---- recovery codes -----------------------------------------------------------

// normalizeCode drops every character that is not a letter or digit and
// upper-cases the rest.
func normalizeCode(code string) string {
	var sb strings.Builder
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r - 32)
		}
	}
	return sb.String()
}

func isTOTPShape(norm string) bool {
	if len(norm) != totpDigits {
		return false
	}
	for _, r := range norm {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// recoveryAlphabet is the upper-cased base64 alphabet; "+" and "/" can appear in
// a code and are ignored when a code is entered (normalizeCode).
const recoveryAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/"

func newRecoveryCode() string {
	for {
		// A code whose letters and digits alone look like a TOTP code would be
		// verified as one, so draw again (vanishingly rare).
		if c := drawRecoveryCode(); !isTOTPShape(normalizeCode(c)) {
			return c
		}
	}
}

func drawRecoveryCode() string {
	n := big.NewInt(int64(len(recoveryAlphabet)))
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		v, err := rand.Int(rand.Reader, n)
		if err != nil {
			panic(fmt.Sprintf("auth: crypto/rand failed: %v", err))
		}
		sb.WriteByte(recoveryAlphabet[v.Int64()])
	}
	return sb.String()
}

type recoveryEntry struct {
	Hash string
}

func decodeRecoveryHashes(raw string) []recoveryEntry {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []recoveryEntry
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func encodeRecoveryHashes(list []recoveryEntry) string {
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// recoveryHash is HMAC-SHA-256 (hex) keyed with the JWT secret over the
// normalized code.
func (s *Service) recoveryHash(code string) string {
	m := hmac.New(sha256.New, []byte(s.cfg().Security.JWTSecret))
	m.Write([]byte(normalizeCode(code)))
	return hex.EncodeToString(m.Sum(nil))
}

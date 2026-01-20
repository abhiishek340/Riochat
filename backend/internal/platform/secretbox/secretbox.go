// Package secretbox encrypts secrets that must be recoverable (provider API
// keys, tokens, setting secrets) with a rotatable key ring.
//
// Payload format: the literal prefix "v1:" followed by standard base64 of
// nonce || ciphertext+tag, using AES-256-GCM. The AES key is the SHA-256 of the
// trimmed master key string.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Prefix marks a version-1 payload.
const Prefix = "v1:"

// Errors returned by the key ring.
var (
	ErrNotConfigured  = errors.New("secretbox: encryption key is not configured")
	ErrInvalidPayload = errors.New("secretbox: invalid encrypted payload")
	ErrDecrypt        = errors.New("secretbox: decryption failed")
)

type key struct {
	master string
	aead   cipher.AEAD
}

// KeyRing holds one active key (used for all encryption) and any number of
// previous keys (decrypt only). It is immutable and safe for concurrent use.
type KeyRing struct {
	active   key
	previous []key
}

func newKey(master string) (key, error) {
	sum := sha256.Sum256([]byte(master))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return key{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return key{}, err
	}
	return key{master: master, aead: aead}, nil
}

// NewKeyRing builds a ring. Blank and duplicate keys are ignored; an empty
// active key is ErrNotConfigured.
func NewKeyRing(active string, previous []string) (*KeyRing, error) {
	active = strings.TrimSpace(active)
	if active == "" {
		return nil, ErrNotConfigured
	}
	ak, err := newKey(active)
	if err != nil {
		return nil, err
	}
	ring := &KeyRing{active: ak}
	seen := map[string]bool{active: true}
	for _, p := range previous {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		pk, err := newKey(p)
		if err != nil {
			return nil, err
		}
		ring.previous = append(ring.previous, pk)
	}
	return ring, nil
}

// HasPrevious reports whether retired keys are configured (a re-encryption
// pass is needed).
func (r *KeyRing) HasPrevious() bool { return r != nil && len(r.previous) > 0 }

// Encrypt encrypts plaintext with the active key. The plaintext is trimmed; an
// empty plaintext yields an empty payload.
func (r *KeyRing) Encrypt(plaintext string) (string, error) { return r.encrypt(plaintext, nil) }

// EncryptBound is Encrypt with a binding string used as authenticated
// additional data (not stored). A ciphertext copied to another record fails to
// decrypt because the binding differs.
func (r *KeyRing) EncryptBound(plaintext, binding string) (string, error) {
	return r.encrypt(plaintext, []byte(binding))
}

func (r *KeyRing) encrypt(plaintext string, aad []byte) (string, error) {
	if r == nil || r.active.aead == nil {
		return "", ErrNotConfigured
	}
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, r.active.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := r.active.aead.Seal(nonce, nonce, []byte(plaintext), aad)
	return Prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a payload, trying the active key first and then the previous
// keys in order. usedActive reports whether the active key succeeded. An empty
// payload decrypts to "".
func (r *KeyRing) Decrypt(payload string) (plaintext string, usedActive bool, err error) {
	return r.decrypt(payload, nil)
}

// DecryptBound is Decrypt for payloads made with EncryptBound.
func (r *KeyRing) DecryptBound(payload, binding string) (string, bool, error) {
	return r.decrypt(payload, []byte(binding))
}

func (r *KeyRing) decrypt(payload string, aad []byte) (string, bool, error) {
	if r == nil || r.active.aead == nil {
		return "", false, ErrNotConfigured
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", true, nil
	}
	if !strings.HasPrefix(payload, Prefix) {
		return "", false, ErrInvalidPayload
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(payload, Prefix))
	if err != nil {
		return "", false, ErrInvalidPayload
	}
	ns := r.active.aead.NonceSize()
	if len(raw) < ns+r.active.aead.Overhead() {
		return "", false, ErrInvalidPayload
	}
	nonce, body := raw[:ns], raw[ns:]
	if pt, err := r.active.aead.Open(nil, nonce, body, aad); err == nil {
		return string(pt), true, nil
	}
	for _, k := range r.previous {
		if pt, err := k.aead.Open(nil, nonce, body, aad); err == nil {
			return string(pt), false, nil
		}
	}
	return "", false, ErrDecrypt
}

// Reencrypt moves a payload to the active key. It returns the new payload and
// whether anything changed. Already-current and empty payloads are returned
// unchanged. The new payload is verified by decrypting it again.
func (r *KeyRing) Reencrypt(payload string) (string, bool, error) {
	return r.reencrypt(payload, nil)
}

// ReencryptBound is Reencrypt for bound payloads; the binding is preserved.
func (r *KeyRing) ReencryptBound(payload, binding string) (string, bool, error) {
	return r.reencrypt(payload, []byte(binding))
}

func (r *KeyRing) reencrypt(payload string, aad []byte) (string, bool, error) {
	if strings.TrimSpace(payload) == "" {
		return payload, false, nil
	}
	plain, usedActive, err := r.decrypt(payload, aad)
	if err != nil {
		return "", false, err
	}
	if usedActive {
		return payload, false, nil
	}
	if plain == "" {
		return "", false, errors.New("secretbox: refusing to re-encrypt an empty plaintext from a non-empty payload")
	}
	next, err := r.encrypt(plain, aad)
	if err != nil {
		return "", false, err
	}
	check, active, err := r.decrypt(next, aad)
	if err != nil || !active || check != strings.TrimSpace(plain) {
		return "", false, errors.New("secretbox: re-encryption verification failed")
	}
	return next, true, nil
}

// LookupHashes returns HMAC-SHA256 (hex) digests of value keyed by each master
// key, active key first. New data is indexed with the first digest; lookups
// try all of them so an index survives key rotation.
func (r *KeyRing) LookupHashes(value string) []string {
	if r == nil || r.active.aead == nil {
		return nil
	}
	out := []string{hmacHex(r.active.master, value)}
	for _, k := range r.previous {
		out = append(out, hmacHex(k.master, value))
	}
	return out
}

// LookupHash returns the digest to store for new data.
func (r *KeyRing) LookupHash(value string) string {
	if h := r.LookupHashes(value); len(h) > 0 {
		return h[0]
	}
	return ""
}

func hmacHex(masterKey, value string) string {
	m := hmac.New(sha256.New, []byte(masterKey))
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

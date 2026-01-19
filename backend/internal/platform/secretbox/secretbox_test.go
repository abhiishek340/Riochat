package secretbox

import (
	"errors"
	"strings"
	"testing"
)

func mustRing(t *testing.T, active string, prev ...string) *KeyRing {
	t.Helper()
	r, err := NewKeyRing(active, prev)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoundTripAndFormat(t *testing.T) {
	r := mustRing(t, "key-A")
	p, err := r.Encrypt("  sk-secret  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "v1:") {
		t.Fatalf("missing prefix: %q", p)
	}
	got, active, err := r.Decrypt(p)
	if err != nil || got != "sk-secret" || !active {
		t.Fatalf("decrypt: %q %v %v", got, active, err)
	}
	p2, _ := r.Encrypt("sk-secret")
	if p == p2 {
		t.Fatal("nonce must make payloads differ")
	}
	if e, _ := r.Encrypt("   "); e != "" {
		t.Fatal("empty plaintext yields empty payload")
	}
	if d, _, err := r.Decrypt(""); d != "" || err != nil {
		t.Fatalf("empty payload: %q %v", d, err)
	}
}

func TestInvalidPayloads(t *testing.T) {
	r := mustRing(t, "key-A")
	for _, bad := range []string{"plain", "v2:AAAA", "v1:%%%not-base64", "v1:AAAA"} {
		if _, _, err := r.Decrypt(bad); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("%q: want ErrInvalidPayload, got %v", bad, err)
		}
	}
	other := mustRing(t, "key-B")
	p, _ := other.Encrypt("x")
	if _, _, err := r.Decrypt(p); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
	if _, err := NewKeyRing("  ", nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty key: %v", err)
	}
}

func TestRotationAndReencrypt(t *testing.T) {
	oldRing := mustRing(t, "key-A")
	payload, _ := oldRing.Encrypt("tenant-token")

	rotated := mustRing(t, "key-B", "key-A", "", "key-A", "key-B")
	if !rotated.HasPrevious() || len(rotated.previous) != 1 {
		t.Fatalf("blank/duplicate keys must be ignored: %d previous", len(rotated.previous))
	}
	plain, active, err := rotated.Decrypt(payload)
	if err != nil || plain != "tenant-token" || active {
		t.Fatalf("rotated decrypt: %q %v %v", plain, active, err)
	}
	next, changed, err := rotated.Reencrypt(payload)
	if err != nil || !changed {
		t.Fatalf("reencrypt: %v %v", changed, err)
	}
	onlyB := mustRing(t, "key-B")
	if got, _, err := onlyB.Decrypt(next); err != nil || got != "tenant-token" {
		t.Fatalf("new payload must open with B alone: %q %v", got, err)
	}
	same, changed, err := rotated.Reencrypt(next)
	if err != nil || changed || same != next {
		t.Fatalf("current payload must be left alone: %v %v", changed, err)
	}
	if out, changed, err := rotated.Reencrypt(""); out != "" || changed || err != nil {
		t.Fatal("empty payload must be a no-op")
	}
	if _, _, err := rotated.Reencrypt("v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("undecryptable payload: %v", err)
	}
}

func TestBoundEncryption(t *testing.T) {
	r := mustRing(t, "key-A")
	p, err := r.EncryptBound("provider-key", "user:7|pub:abc")
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := r.DecryptBound(p, "user:7|pub:abc"); err != nil || got != "provider-key" {
		t.Fatalf("matching binding: %q %v", got, err)
	}
	if _, _, err := r.DecryptBound(p, "user:8|pub:abc"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other binding must fail: %v", err)
	}
	if _, _, err := r.Decrypt(p); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unbound open of bound payload must fail: %v", err)
	}

	rotated := mustRing(t, "key-B", "key-A")
	moved, changed, err := rotated.ReencryptBound(p, "user:7|pub:abc")
	if err != nil || !changed {
		t.Fatalf("bound reencrypt: %v %v", changed, err)
	}
	if got, active, err := rotated.DecryptBound(moved, "user:7|pub:abc"); err != nil || !active || got != "provider-key" {
		t.Fatalf("bound after rotation: %q %v %v", got, active, err)
	}
}

func TestLookupHashesSurviveRotation(t *testing.T) {
	a := mustRing(t, "key-A")
	stored := a.LookupHash("REDEEM-CODE")
	rotated := mustRing(t, "key-B", "key-A")
	hashes := rotated.LookupHashes("REDEEM-CODE")
	if len(hashes) != 2 || hashes[0] == stored || hashes[1] != stored {
		t.Fatalf("hashes: %v (stored %s)", hashes, stored)
	}
	if rotated.LookupHash("REDEEM-CODE") != hashes[0] {
		t.Fatal("new data uses the first digest")
	}
	var nilRing *KeyRing
	if nilRing.LookupHash("x") != "" || nilRing.LookupHashes("x") != nil {
		t.Fatal("nil ring yields nothing")
	}
	if _, err := nilRing.Encrypt("x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil ring encrypt: %v", err)
	}
}

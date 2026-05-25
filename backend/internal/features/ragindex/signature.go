package ragindex

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

func normalizeEndpoint(h string) string { return strings.TrimRight(strings.TrimSpace(h), "/") }

// ModelSignature is the legacy vector-space signature: the first four bytes of
// SHA-256("<model>@<dimensions>") in hex, then "@<dimensions>". The model name
// is trimmed first.
func ModelSignature(model string, dims int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d", strings.TrimSpace(model), dims)))
	return hex.EncodeToString(sum[:4]) + "@" + strconv.Itoa(dims)
}

// SpaceSignature is the current signature: the first eight bytes of
// SHA-256("<model>@<dimensions>@<endpoint>") in hex, then "@<dimensions>". The
// model and the endpoint are trimmed and the endpoint loses trailing slashes,
// so cosmetic differences never change the space.
func SpaceSignature(model string, dims int, endpoint string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d@%s", strings.TrimSpace(model), dims, normalizeEndpoint(endpoint))))
	return hex.EncodeToString(sum[:8]) + "@" + strconv.Itoa(dims)
}

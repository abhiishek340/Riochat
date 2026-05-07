package extract

import (
	"context"
	"net"
	"time"
)

// Resolver looks up host names.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Managed sidecar addresses.
const (
	ManagedTikaHost     = "riochat-tika"
	ManagedTikaURL      = "http://riochat-tika:9998"
	LocalTikaURL        = "http://127.0.0.1:9998"
	ManagedRapidOCRHost = "riochat-rapidocr"
	ManagedRapidOCRURL  = "http://riochat-rapidocr:8002/ocr"
	LocalRapidOCRURL    = "http://127.0.0.1:8002/ocr"
)

// ResolveManaged returns the managed container URL when its host name
// resolves within 300 ms, otherwise the local address.
func ResolveManaged(ctx context.Context, r Resolver, host, managedURL, localURL string) string {
	if r == nil {
		r = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if addrs, err := r.LookupHost(ctx, host); err == nil && len(addrs) > 0 {
		return managedURL
	}
	return localURL
}

// ManagedCandidates lists the addresses to probe, managed first.
func ManagedCandidates(ctx context.Context, r Resolver, host, managedURL, localURL string) []string {
	first := ResolveManaged(ctx, r, host, managedURL, localURL)
	if first == localURL {
		return []string{localURL}
	}
	return []string{first, localURL}
}

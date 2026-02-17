package auth

import (
	"context"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// clientInfo describes where a request came from: address, user agent and the
// trusted-proxy geo headers, plus the request id for event correlation.
type clientInfo struct {
	middleware.AuditContext
	RequestID string
}

type clientKey struct{}

func withClient(ctx context.Context, ci clientInfo) context.Context {
	return context.WithValue(ctx, clientKey{}, ci)
}

// clientFrom returns the client info attached to ctx; without one it falls back
// to the request id in the request state.
func clientFrom(ctx context.Context) clientInfo {
	if ci, ok := ctx.Value(clientKey{}).(clientInfo); ok {
		return ci
	}
	return clientInfo{RequestID: reqctx.From(ctx).RequestID()}
}

// deviceMeta is what we derive from a user agent.
type deviceMeta struct {
	Browser string
	OS      string
	Type    string
	Label   string
}

func parseBrowser(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera/"):
		return "Opera"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		return "Chrome"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS/"):
		return "Firefox"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	case strings.Contains(ua, "PostmanRuntime/"):
		return "Postman"
	case strings.Contains(ua, "curl/"):
		return "curl"
	}
	return ""
}

func parseOS(ua string) string {
	// Order matters (first match wins): the documented sequence is followed
	// literally, so an iPhone agent that also says "like Mac OS X" reports macOS.
	switch {
	case strings.Contains(ua, "Windows NT"):
		return "Windows"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		return "macOS"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		return "iOS"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	return ""
}

func parseDeviceType(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.TrimSpace(ua) == "":
		return "unknown"
	case strings.Contains(lower, "bot"), strings.Contains(lower, "spider"), strings.Contains(lower, "crawler"):
		return "bot"
	case strings.Contains(ua, "iPad"), strings.Contains(ua, "Tablet"):
		return "tablet"
	case strings.Contains(ua, "Mobile"), strings.Contains(ua, "Android"), strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPod"):
		return "mobile"
	}
	return "desktop"
}

func parseDevice(ua string) deviceMeta {
	m := deviceMeta{Browser: parseBrowser(ua), OS: parseOS(ua), Type: parseDeviceType(ua)}
	switch {
	case m.Browser != "" && m.OS != "":
		m.Label = m.Browser + " on " + m.OS
	case m.Browser != "":
		m.Label = m.Browser
	case m.OS != "":
		m.Label = m.OS
	default:
		switch m.Type {
		case "mobile":
			m.Label = "Mobile device"
		case "tablet":
			m.Label = "Tablet"
		case "bot":
			m.Label = "Automation client"
		case "desktop":
			m.Label = "Desktop browser"
		default:
			m.Label = "Unknown device"
		}
	}
	return m
}

// locationLabel joins city, region and country code, skipping blanks.
func locationLabel(city, region, country string) string {
	var parts []string
	for _, p := range []string{city, region, country} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return truncateRunes(s, n)
}

package restapi

import (
	"log/slog"
	"net/http"
	"strings"
)

var knownSDKLangs = map[string]bool{
	"js":     true,
	"node":   true,
	"python": true,
	"java":   true,
	"kotlin": true,
	"go":     true,
	"ruby":   true,
}

// normalizeSDKLang lower-cases the header value and bucket-maps anything
// outside the known SDK language set to "other".
func normalizeSDKLang(raw string) string {
	lang := strings.ToLower(strings.TrimSpace(raw))
	if knownSDKLangs[lang] {
		return lang
	}
	return "other"
}

// classifyClient returns slog attributes describing the client platform.
// SDK requests (identified by the X-Stainless-Lang header set by all
// OneBusAway SDKs) include both client_platform="sdk" and a normalized
// sdk_lang. Other requests fall back to coarse User-Agent classification.
func classifyClient(r *http.Request) []slog.Attr {
	if raw := strings.TrimSpace(r.Header.Get("X-Stainless-Lang")); raw != "" {
		return []slog.Attr{
			slog.String("client_platform", "sdk"),
			slog.String("sdk_lang", normalizeSDKLang(raw)),
		}
	}
	return []slog.Attr{
		slog.String("client_platform", classifyUserAgent(r.Header.Get("User-Agent"))),
	}
}

// classifyUserAgent maps a User-Agent header to a coarse platform bucket.
// Returning a category rather than the raw header avoids writing
// device-identifying data to logs while preserving aggregate platform stats.
func classifyUserAgent(ua string) string {
	if ua == "" {
		return "unknown"
	}
	ua = strings.ToLower(ua)
	// iOS markers must be checked before the generic "web" markers because
	// iOS Safari User-Agents also contain "mozilla" and "safari".
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "cfnetwork") {
		return "ios"
	}
	if strings.Contains(ua, "android") || strings.Contains(ua, "okhttp") {
		return "android"
	}
	if strings.Contains(ua, "mozilla") || strings.Contains(ua, "chrome") || strings.Contains(ua, "safari") || strings.Contains(ua, "firefox") {
		return "web"
	}
	return "other"
}

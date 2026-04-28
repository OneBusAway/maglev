package restapi

import (
	"log/slog"
	"net/http"
	"strings"
)

// classifyClient returns slog attributes describing the client platform.
// SDK requests (identified by the X-Stainless-Lang header set by all
// OneBusAway SDKs) include both client_platform="sdk" and sdk_lang.
// Other requests fall back to coarse User-Agent classification.
func classifyClient(r *http.Request) []slog.Attr {
	if sdkLang := r.Header.Get("X-Stainless-Lang"); sdkLang != "" {
		return []slog.Attr{
			slog.String("client_platform", "sdk"),
			slog.String("sdk_lang", sdkLang),
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
	// iOS markers must be checked before the generic "web" markers because
	// iOS Safari User-Agents also contain "Mozilla" and "Safari".
	if strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "CFNetwork") {
		return "ios"
	}
	if strings.Contains(ua, "Android") || strings.Contains(ua, "okhttp") {
		return "android"
	}
	if strings.Contains(ua, "Mozilla") || strings.Contains(ua, "Chrome") || strings.Contains(ua, "Safari") || strings.Contains(ua, "Firefox") {
		return "web"
	}
	return "other"
}

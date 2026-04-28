package restapi

import (
	"net/http/httptest"
	"testing"
)

func TestClassifyUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want string
	}{
		{"empty string", "", "unknown"},
		{"iPhone Safari", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15", "ios"},
		{"iPad Safari", "Mozilla/5.0 (iPad; CPU OS 17_4 like Mac OS X)", "ios"},
		{"iOS native (CFNetwork)", "OneBusAway/3.14 CFNetwork/1494 Darwin/23.4.0", "ios"},
		{"Android Chrome", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36", "android"},
		{"Android okhttp", "okhttp/4.12.0", "android"},
		{"Desktop Chrome", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "web"},
		{"Desktop Firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0", "web"},
		{"curl", "curl/8.4.0", "other"},
		{"Go http client", "Go-http-client/1.1", "other"},
		{"lowercased iPhone UA", "mozilla/5.0 (iphone; cpu iphone os 17_4 like mac os x)", "ios"},
		{"uppercased Android UA", "MOZILLA/5.0 (LINUX; ANDROID 14; PIXEL 8)", "android"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyUserAgent(tt.ua); got != tt.want {
				t.Errorf("classifyUserAgent(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

func TestNormalizeSDKLang(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"known js", "js", "js"},
		{"known python", "python", "python"},
		{"uppercase normalized", "JAVA", "java"},
		{"surrounding whitespace trimmed", "  go  ", "go"},
		{"unknown bucketed", "rust", "other"},
		{"injection attempt bucketed", "javascript\n{\"pii\":\"leak\"}", "other"},
		{"empty bucketed", "", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSDKLang(tt.raw); got != tt.want {
				t.Errorf("normalizeSDKLang(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestClassifyClient(t *testing.T) {
	t.Run("normalizes known SDK lang", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Stainless-Lang", "Python")

		attrs := classifyClient(req)
		got := map[string]string{}
		for _, a := range attrs {
			got[a.Key] = a.Value.String()
		}
		if got["client_platform"] != "sdk" {
			t.Errorf("client_platform = %q, want sdk", got["client_platform"])
		}
		if got["sdk_lang"] != "python" {
			t.Errorf("sdk_lang = %q, want python", got["sdk_lang"])
		}
	})

	t.Run("whitespace-only SDK header falls back to UA classification", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Stainless-Lang", "   ")
		req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_4)")

		attrs := classifyClient(req)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr (no sdk_lang), got %d", len(attrs))
		}
		if attrs[0].Key != "client_platform" || attrs[0].Value.String() != "ios" {
			t.Errorf("got %v, want client_platform=ios", attrs[0])
		}
	})

	t.Run("buckets unknown SDK lang as other", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Stainless-Lang", "<script>alert(1)</script>")

		attrs := classifyClient(req)
		var sdkLang string
		for _, a := range attrs {
			if a.Key == "sdk_lang" {
				sdkLang = a.Value.String()
			}
		}
		if sdkLang != "other" {
			t.Errorf("sdk_lang = %q, want other", sdkLang)
		}
	})

	t.Run("falls back to UA classification when header absent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14)")

		attrs := classifyClient(req)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attr, got %d", len(attrs))
		}
		if attrs[0].Key != "client_platform" || attrs[0].Value.String() != "android" {
			t.Errorf("got %v, want client_platform=android", attrs[0])
		}
	})
}

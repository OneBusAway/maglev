package restapi

import "testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyUserAgent(tt.ua); got != tt.want {
				t.Errorf("classifyUserAgent(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

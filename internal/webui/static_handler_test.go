package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMarketingHandler_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()

	marketingDir := filepath.Join(tempDir, "marketing")
	if err := os.MkdirAll(marketingDir, 0755); err != nil {
		t.Fatalf("failed to create marketing directory: %v", err)
	}

	validFile := filepath.Join(marketingDir, "index.html")
	if err := os.WriteFile(validFile, []byte("<html>Valid Marketing Page</html>"), 0644); err != nil {
		t.Fatalf("failed to create valid file: %v", err)
	}

	secretDir := filepath.Join(tempDir, "marketing-secret")
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		t.Fatalf("failed to create marketing-secret directory: %v", err)
	}

	secretFile := filepath.Join(secretDir, "secret.html")
	if err := os.WriteFile(secretFile, []byte("<html>SECRET DATA</html>"), 0644); err != nil {
		t.Fatalf("failed to create secret file: %v", err)
	}

	parentSecretFile := filepath.Join(tempDir, "parent-secret.html")
	if err := os.WriteFile(parentSecretFile, []byte("<html>PARENT SECRET</html>"), 0644); err != nil {
		t.Fatalf("failed to create parent secret file: %v", err)
	}

	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWd)
	})

	// Create WebUI instance for testing
	webUI := &WebUI{}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		allowRedirect  bool
		description    string
	}{
		{
			name:           "valid file access",
			path:           "/marketing/index.html",
			expectedStatus: http.StatusOK,
			allowRedirect:  true,
			description:    "should allow access to valid file in marketing directory",
		},
		{
			name:           "parent directory traversal with ..",
			path:           "/marketing/../parent-secret.html",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should block parent directory traversal attempts",
		},
		{
			name:           "sibling directory attack via encoded path",
			path:           "/marketing/../marketing-secret/secret.html",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should block sibling directory access attempts",
		},
		{
			name:           "double encoded traversal",
			path:           "/marketing/%2e%2e/marketing-secret/secret.html",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should block encoded traversal attempts",
		},
		{
			name:           "backslash traversal",
			path:           "/marketing/..\\marketing-secret\\secret.html",
			expectedStatus: http.StatusBadRequest,
			allowRedirect:  false,
			description:    "should block backslash traversal attempts",
		},
		{
			name:           "disallowed extension",
			path:           "/marketing/config.json",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should block disallowed file extensions",
		},
		{
			name:           "directory access attempt",
			path:           "/marketing/",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should block directory listing",
		},
		{
			name:           "null byte injection",
			path:           "/marketing/index.html%00.png",
			expectedStatus: http.StatusNotFound,
			allowRedirect:  false,
			description:    "should handle null byte injection attempts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()

			webUI.marketingHandler(rr, req)

			statusOK := rr.Code == tt.expectedStatus
			if tt.allowRedirect && !statusOK {
				statusOK = rr.Code == http.StatusMovedPermanently || rr.Code == http.StatusOK
			}
			if !statusOK {
				t.Errorf("%s: expected status %d, got %d (body: %s)",
					tt.description, tt.expectedStatus, rr.Code, rr.Body.String())
			}

		})
	}
}

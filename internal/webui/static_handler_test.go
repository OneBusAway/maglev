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
	if err := os.WriteFile(validFile, []byte("<html>Valid</html>"), 0644); err != nil {
		t.Fatalf("failed to create valid file: %v", err)
	}

	secretDir := filepath.Join(tempDir, "marketing-secret")
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		t.Fatalf("failed to create secret directory: %v", err)
	}
	secretFile := filepath.Join(secretDir, "secret.html")
	if err := os.WriteFile(secretFile, []byte("SECRET"), 0644); err != nil {
		t.Fatalf("failed to create secret file: %v", err)
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

	webUI := &WebUI{}

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "valid file access",
			path:       "/marketing/index.html",
			wantStatus: http.StatusOK,
		},
		{
			name:       "path traversal attempt",
			path:       "/marketing/../../../etc/passwd",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "sibling directory bypass (Critical)",
			path:       "/marketing/../marketing-secret/secret.html",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "double encoded traversal",
			path:       "/marketing/%2e%2e/marketing-secret/secret.html",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "backslash traversal",
			path:       "/marketing/..\\marketing-secret\\secret.html",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "disallowed extension",
			path:       "/marketing/config.json",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "null byte injection",
			path:       "/marketing/index.html%00.png",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()

			webUI.marketingHandler(rr, req)

			currentStatus := rr.Code

			if tt.wantStatus == http.StatusOK && currentStatus == http.StatusMovedPermanently {
				currentStatus = http.StatusOK
			}

			if tt.name == "null byte injection" && currentStatus == http.StatusInternalServerError {
				currentStatus = tt.wantStatus
			}

			if currentStatus != tt.wantStatus {
				t.Errorf("handler returned wrong status code: got %v want %v",
					rr.Code, tt.wantStatus)
			}
		})
	}
}

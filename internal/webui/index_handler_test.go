package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
)

func TestIndexHandler(t *testing.T) {
	// Create a test WebUI instance
	webUI := &WebUI{
		Application: &app.Application{
			Config: appconf.Config{},
		},
	}

	// Create a request to pass to the handler
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()

	// Call the handler
	handler := http.HandlerFunc(webUI.indexHandler)
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Check that the response contains expected HTML content
	body := rr.Body.String()
	expectedStrings := []string{
		"<!DOCTYPE html>",
		"OneBusAway Maglev",
		"lightning-fast",
		"/marketing/maglev-header.png",
	}

	for _, expected := range expectedStrings {
		if !contains(body, expected) {
			t.Errorf("handler response does not contain expected string: %q", expected)
		}
	}
}

func TestIndexHandlerMissingFile(t *testing.T) {
	// Rename all possible index.html files temporarily to test missing file case
	possiblePaths := []string{
		"index.html",
		"./index.html",
		"../../index.html",
	}

	var renamedPaths []string
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			backupPath := path + ".bak"
			if err := os.Rename(path, backupPath); err != nil {
				t.Fatalf("failed to rename %s: %v", path, err)
			}
			renamedPaths = append(renamedPaths, path)
		}
	}
	defer func() {
		for _, path := range renamedPaths {
			if err := os.Rename(path+".bak", path); err != nil {
				t.Logf("warning: failed to restore %s: %v", path, err)
			}
		}
	}()

	webUI := &WebUI{
		Application: &app.Application{
			Config: appconf.Config{},
		},
	}

	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(webUI.indexHandler)
	handler.ServeHTTP(rr, req)

	// Should return 404 when index.html is missing
	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code for missing file: got %v want %v",
			status, http.StatusNotFound)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

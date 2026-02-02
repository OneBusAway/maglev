package webui

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (webUI *WebUI) marketingHandler(w http.ResponseWriter, r *http.Request) {
	// Get the file path from the URL
	fileName := filepath.Base(r.URL.Path)

	// Whitelist allowed extensions
	ext := strings.ToLower(filepath.Ext(fileName))
	allowedExtensions := map[string]bool{
		".html": true, ".css": true, ".js": true,
		".png": true, ".jpg": true, ".jpeg": true, ".svg": true,
		".ico": true,
	}
	if !allowedExtensions[ext] {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Ensure no path traversal attempts
	if strings.Contains(fileName, "..") || strings.ContainsAny(fileName, `/\`) {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(".", "marketing", fileName)

	// Verify the resolved path is still within marketing directory
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	marketingDir, err := filepath.Abs("./marketing")
	if err != nil {
		http.Error(w, "Internal configuration error", http.StatusInternalServerError)
		return
	}
	rel, err := filepath.Rel(marketingDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		slog.Warn("potential path traversal attempt blocked", "path", absPath)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	stat, err := os.Stat(absPath)
	if err != nil || stat.IsDir() {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, absPath)
}

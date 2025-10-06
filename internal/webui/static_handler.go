package webui

import (
	"net/http"
	"os"
	"path/filepath"
)

func (webUI *WebUI) marketingHandler(w http.ResponseWriter, r *http.Request) {
	// Get the file path from the URL
	fileName := filepath.Base(r.URL.Path)
	filePath := filepath.Join(".", "marketing", fileName)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Serve the file
	http.ServeFile(w, r, filePath)
}

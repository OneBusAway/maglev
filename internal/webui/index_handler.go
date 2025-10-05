package webui

import (
	"net/http"
	"os"
)

func (webUI *WebUI) indexHandler(w http.ResponseWriter, r *http.Request) {
	// Serve the index.html file from the project root
	// Try multiple possible locations to handle both runtime and test scenarios
	possiblePaths := []string{
		"index.html",
		"./index.html",
		"../../index.html", // For tests running from internal/webui
	}

	var indexPath string
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			indexPath = path
			break
		}
	}

	if indexPath == "" {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}

	// Serve the file
	http.ServeFile(w, r, indexPath)
}

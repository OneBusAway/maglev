package webui

import "net/http"

func (webUI *WebUI) SetWebUIRoutes(mux *http.ServeMux) {
	// Serve static assets from marketing directory (must be before root handler)
	mux.HandleFunc("GET /marketing/{file}", webUI.marketingHandler)

	mux.HandleFunc("GET /debug/", webUI.debugIndexHandler)
	mux.HandleFunc("GET /", webUI.indexHandler)
}

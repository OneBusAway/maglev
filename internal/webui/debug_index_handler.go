package webui

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/davecgh/go-spew/spew"
	"maglev.onebusaway.org/internal/appconf"
)

//go:embed debug_index.html
var templateFS embed.FS

type debugData struct {
	Title string
	Pre   string
}

func writeDebugData(w http.ResponseWriter, title string, data interface{}) {
	content := spew.Sdump(data)
	w.Header().Set("Content-Type", "text/html")
	tmpl, err := template.ParseFS(templateFS, "debug_index.html")
	if err != nil {
		// Log the actual error server-side
		slog.Error("failed to parse debug template", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	dataStruct := debugData{
		Title: title,
		Pre:   content,
	}

	err = tmpl.Execute(w, dataStruct)
	if err != nil {
		slog.Error("failed to execute debug template", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (webUI *WebUI) debugIndexHandler(w http.ResponseWriter, r *http.Request) {
	if webUI.Config.Env == appconf.Production {
		http.NotFound(w, r)
		return
	}
	dataType := r.URL.Query().Get("dataType")

	var data interface{}
	var title string

	webUI.GtfsManager.RLock()
	defer webUI.GtfsManager.RUnlock()

	staticData := webUI.GtfsManager.GetStaticData()

	switch dataType {
	case "warnings":
		data = staticData.Warnings
		title = "GTFS Static - Parse Warnings"
	case "agencies":
		data = staticData.Agencies
		title = "GTFS Static - Agencies"
	case "routes":
		data = staticData.Routes
		title = "GTFS Static - Routes"
	case "stops":
		data = staticData.Stops
		title = "GTFS Static - Stops"
	case "transfers":
		data = staticData.Transfers
		title = "GTFS Static - Transfers"
	case "services":
		data = staticData.Services
		title = "GTFS Static - Services"
	case "trips":
		data = staticData.Trips
		title = "GTFS Static - Trips"
	case "shapes":
		data = staticData.Shapes
		title = "GTFS Static - Shapes"
	case "realtime_trips":
		data = webUI.GtfsManager.GetRealTimeTrips()
		title = "GTFS Realtime - Trips"
	case "realtime_vehicles":
		data = webUI.GtfsManager.GetRealTimeVehicles()
		title = "GTFS Realtime - Vehicles"
	default:
		data = map[string]string{
			"error": "Please use one of the following: warnings, agencies, routes, stops, transfers, services, trips, shapes, realtime_trips, realtime_vehicles.",
		}
		title = "Choose a data type"
	}

	writeDebugData(w, title, data)
}

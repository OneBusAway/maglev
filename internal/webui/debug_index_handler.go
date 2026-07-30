package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/internal/appconf"
)

//go:embed debug_index.html
var templateFS embed.FS

type debugData struct {
	Title string
	Pre   string
}

func writeDebugData(w http.ResponseWriter, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>%s</title></head><body>", html.EscapeString(title))
	fmt.Fprintf(w, "<h1>%s</h1><pre>", html.EscapeString(title))

	if data == nil {
		io.WriteString(w, "nil")
	} else {
		switch v := data.(type) {
		case string:
			fmt.Fprint(w, html.EscapeString(v))
		default:
			if b, err := json.MarshalIndent(v, "", "  "); err == nil {
				fmt.Fprint(w, html.EscapeString(string(b)))
			} else {
				fmt.Fprint(w, html.EscapeString(fmt.Sprint(v)))
			}
		}
	}

	io.WriteString(w, "</pre></body></html>")
}

func (webUI *WebUI) debugIndexHandler(w http.ResponseWriter, r *http.Request) {
	if webUI.Config.Env == appconf.Production {
		http.NotFound(w, r)
		return
	}
	dataType := r.URL.Query().Get("dataType")
	ctx := context.Background()
	queries := webUI.GtfsManager.GtfsDB.Queries

	var data any
	var title string

	var err error
	switch dataType {
	case "agencies":
		data, err = queries.ListAgencies(ctx)
		if err != nil {
			slog.Error("debug: failed to list agencies", "error", err)
		}
		title = "GTFS Static - Agencies"
	case "routes":
		data, err = queries.ListRoutes(ctx)
		if err != nil {
			slog.Error("debug: failed to list routes", "error", err)
		}
		title = "GTFS Static - Routes"
	case "stops":
		data, err = queries.ListStops(ctx)
		if err != nil {
			slog.Error("debug: failed to list stops", "error", err)
		}
		title = "GTFS Static - Stops"
	case "trips":
		data, err = queries.ListTrips(ctx)
		if err != nil {
			slog.Error("debug: failed to list trips", "error", err)
		}
		title = "GTFS Static - Trips"
	case "realtime_trips":
		data = webUI.GtfsManager.GetRealTimeTrips()
		title = "GTFS Realtime - Trips"
	case "realtime_vehicles":
		data = webUI.GtfsManager.GetRealTimeVehicles()
		title = "GTFS Realtime - Vehicles"
	default:
		data = map[string]string{
			"error": "Please use one of the following: agencies, routes, stops, trips, realtime_trips, realtime_vehicles.",
		}
		title = "Choose a data type"
	}

	writeDebugData(w, title, data)
}

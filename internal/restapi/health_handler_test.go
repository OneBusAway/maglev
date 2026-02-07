package restapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestHealthHandlerWithNilApplication(t *testing.T) {
	// Create a minimal RestAPI with nil Application to simulate DB unavailable
	api := &RestAPI{
		Application: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	api.healthHandler(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp HealthResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", resp.Status)
	assert.Equal(t, "database not initialized", resp.Detail)
}

func TestHealthHandlerReturnsOK(t *testing.T) {
	// Use in-memory DB to test the health check successfully
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Create a minimal Application with the DB
	app := &app.Application{
		GtfsManager: &gtfs.Manager{
			GtfsDB: &gtfsdb.Client{
				DB: db,
			},
		},
		Config: appconf.Config{
			RateLimit: 100,
		},
	}

	api := NewRestAPI(app)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp HealthResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "ok", healthResp.Status)
}


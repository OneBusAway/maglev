package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func TestConsolidatedStopIDs_Translate(t *testing.T) {
	absorbed, dropped := "5027", "3022"
	tripVehicle := &gtfs.Vehicle{StopID: &absorbed}
	realtime := &gtfs.Realtime{
		Trips: []gtfs.Trip{{
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &absorbed}, {StopID: &dropped}, {}},
			Vehicle:         tripVehicle,
		}},
		Vehicles: []gtfs.Vehicle{{StopID: &absorbed}, {}},
		Alerts:   []gtfs.Alert{{InformedEntities: []gtfs.AlertInformedEntity{{StopID: &absorbed}, {}}}},
	}

	consolidatedStopIDs{"5027": "5008"}.translate(realtime)

	updates := realtime.Trips[0].StopTimeUpdates
	assert.Equal(t, "5008", *updates[0].StopID)
	assert.Equal(t, "3022", *updates[1].StopID, "a stop whose stop times were dropped has no canonical stop to point at")
	assert.Nil(t, updates[2].StopID)
	assert.Equal(t, "5008", *tripVehicle.StopID)
	assert.Equal(t, "5008", *realtime.Vehicles[0].StopID)
	assert.Nil(t, realtime.Vehicles[1].StopID)
	assert.Equal(t, "5008", *realtime.Alerts[0].InformedEntities[0].StopID)
	assert.Equal(t, "5027", absorbed, "IDs are replaced, not written through shared pointers")
}

func TestConsolidatedStopIDs_TranslateNothing(t *testing.T) {
	assert.NotPanics(t, func() {
		consolidatedStopIDs{"5027": "5008"}.translate(nil)
		var none consolidatedStopIDs
		none.translate(&gtfs.Realtime{})
	})
}

func TestStopConsolidation_RealtimeUsesCanonicalStop(t *testing.T) {
	manager, err := newConsolidationManager(t, writeConsolidationFile(t, rabaConsolidation))
	require.NoError(t, err)

	vehicles := encodeVehicleFeed(time.Now(), []*gtfsrt.VehiclePosition{{
		Vehicle: &gtfsrt.VehicleDescriptor{Id: proto.String("bus")},
		StopId:  proto.String("5027"),
	}})
	alerts := encodeStopAlertFeed(t, "5027")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		if r.URL.Path == "/alerts" {
			_, _ = w.Write(alerts)
			return
		}
		_, _ = w.Write(vehicles)
	}))
	t.Cleanup(server.Close)

	manager.updateFeedRealtime(context.Background(), RTFeedConfig{
		ID:                  "raba",
		VehiclePositionsURL: server.URL + "/vehicles",
		ServiceAlertsURL:    server.URL + "/alerts",
	})

	realtimeVehicles := manager.GetRealTimeVehicles()
	require.Len(t, realtimeVehicles, 1)
	require.NotNil(t, realtimeVehicles[0].StopID)
	assert.Equal(t, "5008", *realtimeVehicles[0].StopID)
	assert.Len(t, manager.GetAlertsForStop("5008"), 1)
	assert.Empty(t, manager.GetAlertsForStop("5027"))
}

func TestStopConsolidation_TranslationSurvivesSkippedImport(t *testing.T) {
	config := Config{
		GtfsURL:              models.GetFixturePath(t, "raba.zip"),
		StopConsolidationURL: writeConsolidationFile(t, rabaConsolidation),
		GTFSDataPath:         filepath.Join(t.TempDir(), "gtfs.db"),
		Env:                  appconf.Development,
	}
	first, err := InitGTFSManager(context.Background(), config)
	require.NoError(t, err)
	require.NoError(t, first.Shutdown(context.Background()))

	restarted, err := InitGTFSManager(context.Background(), config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })

	assert.Equal(t, consolidatedStopIDs{"5027": "5008"}, *restarted.consolidatedStops.Load())
}

func encodeStopAlertFeed(t *testing.T, stopID string) []byte {
	t.Helper()
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:           proto.Uint64(uint64(time.Now().Unix())),
		},
		Entity: []*gtfsrt.FeedEntity{{
			Id: proto.String("alert"),
			Alert: &gtfsrt.Alert{
				InformedEntity: []*gtfsrt.EntitySelector{{StopId: proto.String(stopID)}},
				HeaderText: &gtfsrt.TranslatedString{Translation: []*gtfsrt.TranslatedString_Translation{
					{Text: proto.String("Stop moved")},
				}},
			},
		}},
	}
	b, err := proto.Marshal(feed)
	require.NoError(t, err)
	return b
}

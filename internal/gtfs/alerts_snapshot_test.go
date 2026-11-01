package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertsKeepsCrossFeedIDs(t *testing.T) {
	manager := &Manager{}
	agencyA := "25"
	agencyB := "40"

	manager.MockAddAlert("feed-b", gtfs.Alert{
		ID:               "dup",
		Header:           []gtfs.AlertText{{Text: "from b"}},
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyB}},
	})
	manager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "dup",
		Header:           []gtfs.AlertText{{Text: "from a"}},
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyA}},
	})
	manager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "",
		Header:           []gtfs.AlertText{{Text: "nameless"}},
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyA}},
	})
	manager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "no-entities",
		Header: []gtfs.AlertText{{Text: "unindexed"}},
	})

	alerts := manager.Alerts()
	require.Len(t, alerts, 2)
	assert.Equal(t, "from a", alerts[0].Header[0].Text)
	assert.Equal(t, "dup", alerts[0].ID)
	assert.Equal(t, "from b", alerts[1].Header[0].Text)

	// A later snapshot publish has to keep the alert list. MockAddTripUpdate
	// clones the merged view instead of rebuilding it from the feed maps.
	manager.MockAddTripUpdate("trip", nil, nil)
	alerts = manager.Alerts()
	require.Len(t, alerts, 2)
	assert.Equal(t, "from a", alerts[0].Header[0].Text)
	assert.Equal(t, "from b", alerts[1].Header[0].Text)
}

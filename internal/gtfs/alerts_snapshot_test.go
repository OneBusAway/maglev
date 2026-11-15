package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirstAgencyForFeed checks that firstAgencyForFeed returns a feed's
// first configured agency-ids entry, and an empty string for feeds without
// agency-ids and for unknown feed IDs.
func TestFirstAgencyForFeed(t *testing.T) {
	config := Config{RTFeeds: []RTFeedConfig{
		{ID: "feed-a", AgencyIDs: []string{"40", "25"}},
		{ID: "feed-b", AgencyIDs: []string{}},
	}}

	tests := []struct {
		name   string
		feedID string
		want   string
	}{
		{"first configured agency", "feed-a", "40"},
		{"feed without agency-ids", "feed-b", ""},
		{"unknown feed", "feed-c", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.firstAgencyForFeed(tt.feedID))
		})
	}
}

// TestFeedAlertsCarryFirstAgency verifies that the feed's first configured
// agency travels with each alert into the merged snapshot, including through
// the indexed lookups the REST API uses.
func TestFeedAlertsCarryFirstAgency(t *testing.T) {
	agencyID := "40"
	manager := &Manager{
		config: Config{RTFeeds: []RTFeedConfig{
			{ID: "feed-a", AgencyIDs: []string{"40", "25"}},
			{ID: "feed-b", AgencyIDs: []string{"1"}},
		}},
		feedAlerts: map[string][]gtfs.Alert{
			"feed-a": {{ID: "from-a", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyID}}}},
			"feed-b": {{ID: "from-b", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyID}}}},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	alerts := manager.Alerts()
	require.Len(t, alerts, 2)
	assert.Equal(t, "from-a", alerts[0].ID)
	assert.Equal(t, "40", alerts[0].FeedAgencyID)
	assert.Equal(t, "from-b", alerts[1].ID)
	assert.Equal(t, "1", alerts[1].FeedAgencyID)

	byAgency := manager.GetAlertsByIDs("", "", agencyID)
	require.Len(t, byAgency, 2)
	assert.Equal(t, "40", byAgency[0].FeedAgencyID)
	assert.Equal(t, "1", byAgency[1].FeedAgencyID)
}
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

package merge

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMerge_SingleFeed(t *testing.T) {
	// Single feed should return as-is
	feed := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Stop 1"},
			},
		},
		Index:  0,
		Source: "test.zip",
	}

	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, NONE, result.Strategy)
	assert.Equal(t, 1, len(result.Merged.Agencies))
	assert.Equal(t, 1, len(result.Merged.Stops))
}

func TestMerge_TwoFeeds_NoDuplicates(t *testing.T) {
	lat1, lon1 := 40.0, -74.0
	lat2, lon2 := 40.1, -74.1

	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Agency 1"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Stop 1", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency2", Name: "Agency 2"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop2", Name: "Stop 2", Latitude: &lat2, Longitude: &lon2},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Should have both agencies
	assert.Equal(t, 2, len(result.Merged.Agencies))

	// Should have both stops
	assert.Equal(t, 2, len(result.Merged.Stops))

	// No duplicates detected
	assert.Equal(t, 0, result.DuplicatesA)
}

func TestMerge_WithDuplicates(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"}, // Duplicate
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St"}, // Duplicate
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Merged.Agencies))
	assert.Equal(t, 1, len(result.Merged.Stops))
	assert.Equal(t, 2, result.DuplicatesA) // 2 duplicates found
}

func TestMerge_WithRoutesAndTrips(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{
				{Id: "route1", ShortName: "R1"},
			},
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip1", Headsign: "Downtown"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{
				{Id: "route1", ShortName: "R1"}, // Same ID, will be renamed
			},
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip1", Headsign: "Uptown"}, // Same ID, will be renamed
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Merged.Routes))
	assert.Equal(t, 2, len(result.Merged.Trips))
	assert.Equal(t, 2, result.RenamingsA) // 2 renamings (route + trip)
}

func TestMerge_NoFeeds(t *testing.T) {
	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no feeds provided")
}

func TestMerge_WithServices(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{Id: "weekday"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{Id: "weekend"},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Merged.Services))
}

func TestRenameID(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		feedIndex  int
		renameMode RenameMode
		expected   string
	}{
		{
			name:       "Context mode, first feed",
			id:         "stop1",
			feedIndex:  0,
			renameMode: CONTEXT,
			expected:   "a-stop1",
		},
		{
			name:       "Context mode, second feed",
			id:         "stop1",
			feedIndex:  1,
			renameMode: CONTEXT,
			expected:   "b-stop1",
		},
		{
			name:       "Agency mode fallback",
			id:         "route1",
			feedIndex:  0,
			renameMode: AGENCY,
			expected:   "a-route1", // Falls back to CONTEXT
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.RenameMode = tt.renameMode

			merger := NewMerger(opts)
			result := merger.renameID(tt.id, tt.feedIndex)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRegisterScorer(t *testing.T) {
	merger := NewMerger(DefaultOptions())

	// Register a custom scorer
	scorer := &StopNameScorer{}
	merger.RegisterScorer("stop", scorer)

	// Verify it was registered
	assert.NotNil(t, merger.scorers["stop"])
}

type StopNameScorer struct{}

func (s *StopNameScorer) Score(a, b interface{}) float64 {
	return 1.0
}

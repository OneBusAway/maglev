package restapi

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/servicedate"
)

// tripWithWindow builds a trip whose scheduled span runs from minHours to
// maxHours after its service date's midnight. GTFS allows values past 24h.
func tripWithWindow(serviceID string, minHours, maxHours float64) gtfsdb.Trip {
	return gtfsdb.Trip{
		ID:               "trip-1",
		ServiceID:        serviceID,
		MinArrivalTime:   sql.NullInt64{Int64: int64(float64(time.Hour) * minHours), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: int64(float64(time.Hour) * maxHours), Valid: true},
	}
}

func TestServiceDateResolver_Resolve(t *testing.T) {
	location := time.FixedZone("TEST", 0)
	queryDay := time.Date(2024, 3, 15, 0, 0, 0, 0, location)
	previousDay := queryDay.AddDate(0, 0, -1)

	// The query moment is 00:30 on the query day — the window where a trip could
	// belong to either service date.
	resolver := &serviceDateResolver{
		queryDayMidnight:     queryDay,
		sinceStartNs:         int64(30 * time.Minute),
		previousSinceStartNs: int64(30*time.Minute + 24*time.Hour),
		queryDayServices:     map[string]struct{}{"weekday": {}},
		previousDayServices:  map[string]struct{}{"weekday": {}},
	}

	tests := []struct {
		name string
		trip gtfsdb.Trip
		want time.Time
	}{
		{
			name: "Trip running now on today's service",
			trip: tripWithWindow("weekday", 0, 1),
			want: queryDay,
		},
		{
			name: "Past-midnight trip belongs to yesterday's service",
			trip: tripWithWindow("weekday", 24, 25.5),
			want: previousDay,
		},
		{
			// The handlers select on overlap with [now-runningLate, now+runningEarly],
			// so a trip that ended 20 minutes ago is still returned and must be
			// dated to the service day it ran on.
			name: "Previous-day trip that just ended is still yesterday's",
			trip: tripWithWindow("weekday", 23, 24+10.0/60),
			want: previousDay,
		},
		{
			name: "Previous-day trip past the late window is not resolved to yesterday",
			trip: tripWithWindow("weekday", 22, 23+50.0/60),
			want: queryDay,
		},
		{
			// max_departure lands exactly on sinceMidnight-runningLate, which
			// the inclusive bound still counts as running.
			name: "Previous-day trip ending exactly at the late window edge is yesterday's",
			trip: tripWithWindow("weekday", 23, 24),
			want: previousDay,
		},
		{
			// min_arrival lands exactly on sinceMidnight+runningEarly.
			name: "Previous-day trip starting exactly at the early window edge is yesterday's",
			trip: tripWithWindow("weekday", 24+40.0/60, 26),
			want: previousDay,
		},
		{
			name: "Trip not running at this moment falls back to the query day",
			trip: tripWithWindow("weekday", 6, 7),
			want: queryDay,
		},
		{
			name: "Service inactive on both days falls back to the query day",
			trip: tripWithWindow("weekend", 0, 1),
			want: queryDay,
		},
		{
			name: "Trip with no cached time window falls back to the query day",
			trip: gtfsdb.Trip{ID: "trip-1", ServiceID: "weekday"},
			want: queryDay,
		},
		{
			name: "Zero trip falls back to the query day",
			trip: gtfsdb.Trip{},
			want: queryDay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolver.Resolve(tt.trip))
		})
	}
}

// TestServiceDateResolver_PreviousDayServiceOnly covers a trip whose service
// runs only on the previous day, so today's set cannot match it at all.
func TestServiceDateResolver_PreviousDayServiceOnly(t *testing.T) {
	location := time.FixedZone("TEST", 0)
	queryDay := time.Date(2024, 3, 15, 0, 0, 0, 0, location)

	resolver := &serviceDateResolver{
		queryDayMidnight:     queryDay,
		sinceStartNs:         int64(30 * time.Minute),
		previousSinceStartNs: int64(30*time.Minute + 24*time.Hour),
		queryDayServices:     map[string]struct{}{"weekday": {}},
		previousDayServices:  map[string]struct{}{"friday-night": {}},
	}

	assert.Equal(t, queryDay.AddDate(0, 0, -1),
		resolver.Resolve(tripWithWindow("friday-night", 23, 26)))
}

// TestNewServiceDateResolverFor covers the constructor handlers use when they
// already hold the day's service IDs, so it must resolve the same as one that
// loaded them itself.
func TestNewServiceDateResolverFor(t *testing.T) {
	location := time.FixedZone("TEST", 0)
	queryDay := time.Date(2024, 3, 15, 0, 0, 0, 0, location)
	currentTime := queryDay.Add(30 * time.Minute)

	resolver := newServiceDateResolverFor(queryDay, currentTime, serviceIDsByDay{
		QueryDay:    []string{"weekday"},
		PreviousDay: []string{"friday-night"},
	})

	assert.Equal(t, queryDay, resolver.Resolve(tripWithWindow("weekday", 0, 1)))
	assert.Equal(t, queryDay.AddDate(0, 0, -1), resolver.Resolve(tripWithWindow("friday-night", 23, 26)))
	assert.Equal(t, queryDay, resolver.Resolve(tripWithWindow("saturday", 0, 1)))
}

func TestNewServiceDateResolverFor_SpringForwardMeasuresFromServiceStart(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	day := servicedate.New(2026, time.March, 8)
	now := time.Date(2026, time.March, 8, 1, 40, 0, 0, losAngeles)
	resolver := newServiceDateResolverFor(day.Midnight(losAngeles), now, serviceIDsByDay{
		QueryDay: []string{"sunday"},
	})

	assert.Equal(t, now.Sub(day.Start(losAngeles)).Nanoseconds(), resolver.sinceStartNs)
	assert.Equal(t, now.Sub(day.AddDays(-1).Start(losAngeles)).Nanoseconds(), resolver.previousSinceStartNs)
	assert.Equal(t, day.Midnight(losAngeles), resolver.Resolve(tripWithWindow("sunday", 2.5, 2+50.0/60)))

	days := resolver.ServiceDays()
	require.Len(t, days, 2)
	assert.Equal(t, resolver.sinceStartNs, days[0].sinceMidnightNs)
	assert.Equal(t, resolver.previousSinceStartNs, days[1].sinceMidnightNs)
}

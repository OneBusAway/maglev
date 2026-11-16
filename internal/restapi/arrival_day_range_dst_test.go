package restapi

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/servicedate"
)

func TestArrivalWindowSpringServiceDayStartsPreviousEvening(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	query := time.Date(2025, 3, 8, 23, 30, 0, 0, loc)
	serviceDay := servicedate.New(2025, 3, 9)
	require.Equal(t, query, serviceDay.Start(loc).Add(30*time.Minute))
	files := frequencyFixtureFiles()
	files["agency.txt"] = strings.ReplaceAll(files["agency.txt"], ",UTC", ",America/Los_Angeles")
	files["calendar.txt"] = "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" + freqServiceID + ",1,1,1,1,1,1,1,20250309,20250309\n"
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "08:00:00", "00:30:00")
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "08:15:00", "00:45:00")
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(query), "spring-service-base.zip", files)
	params := url.Values{"time": {fmt.Sprint(query.UnixMilli())}, "minutesBefore": {"5"}, "minutesAfter": {"5"}, "frequencyMinutesBefore": {"5"}, "frequencyMinutesAfter": {"5"}}
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, params)
			require.True(t, arrivalsIncludeTrip(t, arrivals, freqNormalTripD), "00:30 on March 9 service date uses a March 8 23:00 GTFS offset base")
		})
	}
}

func TestArrivalDayRangeUsesGTFSOffsetBase(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	tests := []struct {
		name       string
		query, end time.Time
		wantMax    int
	}{
		{"before spring base", time.Date(2025, 3, 8, 12, 0, 0, 0, loc), time.Date(2025, 3, 8, 22, 59, 59, 0, loc), 0},
		{"at spring base", time.Date(2025, 3, 8, 12, 0, 0, 0, loc), time.Date(2025, 3, 8, 23, 0, 0, 0, loc), 1},
		{"after spring base", time.Date(2025, 3, 8, 12, 0, 0, 0, loc), time.Date(2025, 3, 8, 23, 30, 0, 0, loc), 1},
		{"ordinary evening", time.Date(2025, 3, 7, 12, 0, 0, 0, loc), time.Date(2025, 3, 7, 23, 30, 0, 0, loc), 0},
		{"fall evening", time.Date(2025, 11, 1, 12, 0, 0, 0, loc), time.Date(2025, 11, 1, 23, 30, 0, 0, loc), 0},
		{"fall midnight is conservative", time.Date(2025, 11, 1, 12, 0, 0, 0, loc), time.Date(2025, 11, 2, 0, 30, 0, 0, loc), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, maxDay := arrivalDayRange(tt.query, tt.query, tt.end, loc)
			require.Equal(t, tt.wantMax, maxDay)
		})
	}
}

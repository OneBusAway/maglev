package restapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// scheduleGetWithETag performs a GET, sending ifNoneMatch when it is non-empty,
// and returns the status and the ETag the server answers with.
func scheduleGetWithETag(t *testing.T, serverURL, ifNoneMatch string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, serverURL, nil)
	require.NoError(t, err)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header.Get("ETag")
}

// TestScheduleETag_OmittedDateRevalidatesAfterMidnight pins #1514: a conditional
// request for the implicit (today's) schedule must not validate yesterday's
// response once the agency-local service day has rolled over.
func TestScheduleETag_OmittedDateRevalidatesAfterMidnight(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	friday := time.Date(2025, time.June, 13, 23, 30, 0, 0, la)
	saturday := time.Date(2025, time.June, 14, 0, 30, 0, 0, la)

	endpoints := []string{
		"/api/where/schedule-for-stop/25_1033.json?key=TEST",
		"/api/where/schedule-for-route/25_151.json?key=TEST",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			mockClock := clock.NewMockClock(friday)
			api := createTestApiWithClock(t, mockClock)
			server := httptest.NewServer(api.SetupAPIRoutes())
			defer server.Close()

			status, fridayETag := scheduleGetWithETag(t, server.URL+endpoint, "")
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, fridayETag, "-20250613", "the validator carries Friday's service day")

			mockClock.Set(saturday)
			status, saturdayETag := scheduleGetWithETag(t, server.URL+endpoint, fridayETag)
			assert.Equal(t, http.StatusOK, status, "yesterday's validator must not return 304")
			assert.Contains(t, saturdayETag, "-20250614", "the validator carries Saturday's service day")
		})
	}
}

// TestScheduleETag_ExplicitDateKeepsValidator checks that an explicit date still
// validates on the static feed's ETag, since its response does not depend on the clock.
func TestScheduleETag_ExplicitDateKeepsValidator(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	friday := time.Date(2025, time.June, 13, 23, 30, 0, 0, la)
	saturday := time.Date(2025, time.June, 14, 0, 30, 0, 0, la)

	mockClock := clock.NewMockClock(friday)
	api := createTestApiWithClock(t, mockClock)
	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	url := fmt.Sprintf("%s/api/where/schedule-for-stop/25_1033.json?key=TEST&date=2025-06-13", server.URL)
	status, etag := scheduleGetWithETag(t, url, "")
	require.Equal(t, http.StatusOK, status)

	mockClock.Set(saturday)
	status, _ = scheduleGetWithETag(t, url, etag)
	assert.Equal(t, http.StatusNotModified, status, "an explicit date validates on the static ETag")
}

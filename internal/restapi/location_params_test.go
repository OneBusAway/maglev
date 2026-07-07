package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

func TestParseLocationParams_SuccessAndClamping(t *testing.T) {
	api := &RestAPI{}

	tests := []struct {
		name            string
		url             string
		expectedRadius  float64
		expectedLatSpan float64
		expectedLonSpan float64
	}{
		{
			name:            "Radius exceeding max is auto-corrected to 20000",
			url:             "/test?lat=47.6&lon=-122.3&radius=50000",
			expectedRadius:  models.MaxSearchRadiusInMeters,
			expectedLatSpan: 0,
			expectedLonSpan: 0,
		},
		{
			name:            "Spans exceeding max are auto-corrected to 5.0",
			url:             "/test?lat=47.6&lon=-122.3&latSpan=15.0&lonSpan=25.0",
			expectedRadius:  0,
			expectedLatSpan: models.MaxSearchSpanInDegrees,
			expectedLonSpan: models.MaxSearchSpanInDegrees,
		},
		{
			name:            "Normal bounds within limits remain unchanged",
			url:             "/test?lat=47.6&lon=-122.3&radius=5000&latSpan=2.0&lonSpan=3.0",
			expectedRadius:  5000,
			expectedLatSpan: 2.0,
			expectedLonSpan: 3.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			loc, errs := api.parseLocationParams(req, nil)
			require.Empty(t, errs, "expected no validation errors")
			require.NotNil(t, loc, "expected valid LocationParams")

			assert.Equal(t, tt.expectedRadius, loc.Radius)
			assert.Equal(t, tt.expectedLatSpan, loc.LatSpan)
			assert.Equal(t, tt.expectedLonSpan, loc.LonSpan)
		})
	}
}

func TestParseLocationParams_ValidationErrors(t *testing.T) {
	api := &RestAPI{}

	t.Run("Missing required parameters returns field errors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test?radius=1000", nil)
		loc, errs := api.parseLocationParams(req, nil)
		assert.Nil(t, loc)
		assert.NotEmpty(t, errs["lat"])
		assert.NotEmpty(t, errs["lon"])
	})

	t.Run("Invalid syntax for float parameters returns field errors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test?lat=invalid&lon=invalid&radius=invalid&latSpan=invalid&lonSpan=invalid", nil)
		loc, errs := api.parseLocationParams(req, nil)
		assert.Nil(t, loc)
		assert.NotEmpty(t, errs["lat"])
		assert.NotEmpty(t, errs["lon"])
		assert.NotEmpty(t, errs["radius"])
		assert.NotEmpty(t, errs["latSpan"])
		assert.NotEmpty(t, errs["lonSpan"])
	})

	t.Run("Out of bounds coordinates return location errors with nil initial fieldErrors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test?lat=100.0&lon=200.0&radius=-10.0&latSpan=-5.0&lonSpan=-5.0", nil)
		loc, errs := api.parseLocationParams(req, nil)
		assert.Nil(t, loc)
		assert.NotEmpty(t, errs["lat"])
		assert.NotEmpty(t, errs["lon"])
		assert.NotEmpty(t, errs["radius"])
		assert.NotEmpty(t, errs["latSpan"])
		assert.NotEmpty(t, errs["lonSpan"])
	})

	t.Run("Out of bounds coordinates merge into existing empty non-nil fieldErrors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test?lat=100.0&lon=200.0", nil)
		initialErrs := make(map[string][]string)
		loc, errs := api.parseLocationParams(req, initialErrs)
		assert.Nil(t, loc)
		assert.NotEmpty(t, errs["lat"])
		assert.NotEmpty(t, errs["lon"])
	})
}

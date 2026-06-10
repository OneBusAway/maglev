package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestDeduplicateAlerts(t *testing.T) {
	alert1 := gtfs.Alert{ID: "alert-1"}
	alert2 := gtfs.Alert{ID: "alert-2"}
	alert3 := gtfs.Alert{ID: "alert-3"}

	slice1 := []gtfs.Alert{alert1, alert2}
	slice2 := []gtfs.Alert{alert2, alert3}
	slice3 := []gtfs.Alert{alert1, alert3}

	result := deduplicateAlerts(slice1, slice2, slice3)

	assert.Len(t, result, 3, "Should deduplicate and return exactly 3 unique alerts")

	idMap := make(map[string]bool)
	for _, a := range result {
		idMap[a.ID] = true
	}

	assert.True(t, idMap["alert-1"], "Missing alert-1")
	assert.True(t, idMap["alert-2"], "Missing alert-2")
	assert.True(t, idMap["alert-3"], "Missing alert-3")
}

func TestShouldIncludeReferences(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "empty string defaults to true",
			url:      "/api/where/route/1.json?key=TEST",
			expected: true,
		},
		{
			name:     "explicit true returns true",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=true",
			expected: true,
		},
		{
			name:     "explicit false returns false",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=false",
			expected: false,
		},
		{
			name:     "garbage string defaults to true",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=banana",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			actual := ShouldIncludeReferences(req)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

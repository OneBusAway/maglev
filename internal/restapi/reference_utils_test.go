package restapi

import (
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

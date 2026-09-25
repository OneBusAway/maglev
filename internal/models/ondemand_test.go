package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

func TestFormatGTFSTimeOfDay(t *testing.T) {
	tests := []struct {
		nanos int64
		want  string
	}{
		{0, "00:00:00"},
		{int64(5 * time.Hour), "05:00:00"},
		{int64(24*time.Hour + 50*time.Minute), "24:50:00"},
		{int64(25*time.Hour + 5*time.Second), "25:00:05"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, FormatGTFSTimeOfDay(tt.nanos))
	}
}

func TestNullableString(t *testing.T) {
	assert.Nil(t, NullableString(""))
	require.NotNil(t, NullableString("x"))
	assert.Equal(t, "x", *NullableString("x"))
}

func TestOnDemandReferences_SerializesTenKeysAndNullPolicy(t *testing.T) {
	refs := NewEmptyOnDemandReferences()
	refs.ServiceAreas = []ServiceArea{{ID: "a_1", BBox: [4]float64{-1, -2, 1, 2}}}

	encoded, err := json.Marshal(refs)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	for _, key := range []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips", "serviceAreas", "locationGroups", "bookingRules", "calendars"} {
		_, ok := decoded[key]
		assert.True(t, ok, "key %s must always be present", key)
	}
	assert.Len(t, decoded, 10)

	area := decoded["serviceAreas"].([]any)[0].(map[string]any)
	_, hasGeometry := area["geometry"]
	assert.False(t, hasGeometry, "geometry is omitted, not null, when absent")
	assert.Nil(t, area["name"], "absent optional values are null")
	assert.Nil(t, area["distanceToArea"])
	assert.Nil(t, area["nearestPointOnBoundary"])
	assert.Equal(t, []any{-1.0, -2.0, 1.0, 2.0}, area["bbox"])
}

func TestOnDemandService_MatchReasonOmittedWhenEmpty(t *testing.T) {
	encoded, err := json.Marshal(OnDemandService{ID: "a_1", Rules: []AvailabilityRule{}})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "matchReason")
	assert.Contains(t, string(encoded), `"routeId":null`)
	assert.Contains(t, string(encoded), `"rules":[]`)

	encoded, err = json.Marshal(OnDemandService{ID: "a_1", Rules: []AvailabilityRule{}, MatchReason: MatchReasonAreaNearby})
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"matchReason":"areaNearby"`)
}

func TestNewOnDemandResponses(t *testing.T) {
	c := clock.NewMockClock(time.Unix(1785096000, 0))
	refs := NewEmptyOnDemandReferences()

	entry := NewOnDemandEntryResponse(OnDemandService{ID: "x"}, *refs, c)
	data := entry.Data.(map[string]any)
	assert.Equal(t, 200, entry.Code)
	assert.Contains(t, data, "entry")
	assert.Contains(t, data, "references")
	assert.NotContains(t, data, "outOfRange")

	list := NewOnDemandListResponse([]OnDemandService{}, *refs, c)
	data = list.Data.(map[string]any)
	assert.Equal(t, false, data["limitExceeded"])
	assert.NotContains(t, data, "outOfRange")

	ranged := NewOnDemandListResponseWithRange([]OnDemandService{}, *refs, true, c)
	data = ranged.Data.(map[string]any)
	assert.Equal(t, true, data["outOfRange"])
	assert.Equal(t, false, data["limitExceeded"])
}

package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func newFlexTestManager(t *testing.T, fixture string) *Manager {
	t.Helper()
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      models.GetFixturePath(t, fixture),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager
}

func TestFlexIndex_Alexandria(t *testing.T) {
	idx := newFlexTestManager(t, "alexandria-flex.zip").FlexIndex()

	assert.False(t, idx.IsFlexEmpty())
	assert.Equal(t, []string{"5088_77652"}, idx.OnDemandServiceIDsForRoute("77652"))
	assert.Nil(t, idx.OnDemandServiceIDsForStop("4258639"), "the stop is referenced by no rule")

	area := idx.FlexArea("area_1449")
	require.NotNil(t, area)
	assert.Equal(t, utils.CoordinateBounds{MinLat: 38.617508, MaxLat: 39.057831, MinLon: -77.5372039, MaxLon: -76.9092198}, area.Bounds)
	assert.Len(t, area.Polygons[0][0], 4239, "containment runs on the full geometry")
	assert.Nil(t, idx.FlexArea("nope"))

	assert.Equal(t, area.Bounds, idx.ServiceBounds["5088_77652"], "no rule-referenced stops, so the bounds are the zone bbox")
	assert.Equal(t, []string{"area_1449"}, idx.ServiceAreaIDs["5088_77652"])
	assert.Empty(t, idx.ServiceStops["5088_77652"])

	assert.Equal(t, []string{"5088_77652"}, idx.ServiceBoundsOverlapping(utils.CalculateBounds(38.836368, -77.049221, 600)))
	assert.Empty(t, idx.ServiceBoundsOverlapping(utils.CalculateBounds(47.6, -122.3, 600)))
}

func TestFlexIndex_CharlevoixStopPointers(t *testing.T) {
	idx := newFlexTestManager(t, "charlevoix-flex.zip").FlexIndex()

	assert.Equal(t, []string{"CC_CC3"}, idx.OnDemandServiceIDsForStop("CC_Ironton_Ferry_West"))
	assert.Equal(t, []string{"CC_CC3"}, idx.OnDemandServiceIDsForStop("CC_Ironton_Ferry_East"))
	assert.Equal(t, []string{"CC_CC1"}, idx.OnDemandServiceIDsForRoute("CC1"))
	assert.Equal(t, []string{"CC_CC2_med"}, idx.OnDemandServiceIDsForRoute("CC2_med"))

	assert.Empty(t, idx.ServiceAreaIDs["CC_CC3"], "a stop-group service has no zones")
	require.Len(t, idx.ServiceStops["CC_CC3"], 2)
	assert.Equal(t, "CC_Ironton_Ferry_East", idx.ServiceStops["CC_CC3"][0].StopID)
	assert.InDelta(t, 45.2558717411615, idx.ServiceStops["CC_CC3"][0].Lat, 1e-9)

	bounds := idx.ServiceBounds["CC_CC3"]
	assert.InDelta(t, 45.2558717411615, bounds.MinLat, 1e-9)
	assert.InDelta(t, 45.2562297144487, bounds.MaxLat, 1e-9)
	assert.InDelta(t, -85.1852474613293, bounds.MinLon, 1e-9)
	assert.InDelta(t, -85.181870904532, bounds.MaxLon, 1e-9)

	assert.ElementsMatch(t, []string{"charlevoix_county", "gaylord", "petoskey"}, idx.ServiceAreaIDs["CC_CC2_med"])
	assert.Len(t, idx.Areas, 4)
}

func TestFlexIndex_NonFlexFeedIsEmpty(t *testing.T) {
	idx := newFlexTestManager(t, "raba.zip").FlexIndex()
	assert.True(t, idx.IsFlexEmpty())
	assert.Nil(t, idx.OnDemandServiceIDsForRoute("1"))
	assert.Empty(t, idx.ServiceBoundsOverlapping(utils.CalculateBounds(40.58, -122.39, 600)))
}

func TestFlexIndex_ReloadToNonFlexFeedEmptiesTheIndex(t *testing.T) {
	manager := newFlexTestManager(t, "charlevoix-flex.zip")
	require.False(t, manager.FlexIndex().IsFlexEmpty())

	manager.SetGtfsURL(models.GetFixturePath(t, "raba.zip"))
	changed, err := manager.ReloadStatic(context.Background())
	require.NoError(t, err)
	require.True(t, changed)

	assert.True(t, manager.FlexIndex().IsFlexEmpty())
}

func TestFlexIndex_ImmutableAccessors(t *testing.T) {
	idx := newFlexTestManager(t, "charlevoix-flex.zip").FlexIndex()
	ids := idx.OnDemandServiceIDsForStop("CC_Ironton_Ferry_West")
	ids[0] = "mutated"
	assert.Equal(t, []string{"CC_CC3"}, idx.OnDemandServiceIDsForStop("CC_Ironton_Ferry_West"), "accessors return copies")
}

func TestFlexIndex_SkippedLocationIsAbsent(t *testing.T) {
	manager := newFlexTestManager(t, "charlevoix-flex.zip")
	ctx := context.Background()
	// Simulate a zone whose locations row was skipped at import for bad
	// geometry while flex_stop_times still references it.
	_, err := manager.GtfsDB.DB.ExecContext(ctx, "DELETE FROM locations WHERE id = 'gaylord'")
	require.NoError(t, err)

	idx, err := buildFlexIndex(ctx, manager.GtfsDB)
	require.NoError(t, err)

	assert.Nil(t, idx.FlexArea("gaylord"))
	assert.ElementsMatch(t, []string{"charlevoix_county", "petoskey"}, idx.ServiceAreaIDs["CC_CC2_med"])
	assert.Equal(t, utils.UnionBounds(idx.FlexArea("charlevoix_county").Bounds, idx.FlexArea("petoskey").Bounds), idx.ServiceBounds["CC_CC2_med"])
}

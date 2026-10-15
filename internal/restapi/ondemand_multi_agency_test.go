package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/models"
)

// stopsListResponse decodes the /where list envelope down to stop ids.
type stopsListResponse struct {
	Data struct {
		List []models.Stop `json:"list"`
	} `json:"data"`
}

func listedStopIDs(t *testing.T, api *RestAPI, endpoint string) []string {
	t.Helper()
	resp, model := callAPIHandler[stopsListResponse](t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode, endpoint)
	ids := make([]string, 0, len(model.Data.List))
	for _, stop := range model.Data.List {
		ids = append(ids, stop.ID)
	}
	return ids
}

// A stop keeps the /where identity its fixed routes give it when another
// agency's flex service references it; only flex-only stops fall back to the
// service's agency.
func TestOnDemand_StopReferencedAcrossAgenciesKeepsItsWhereID(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "flex-two-agency.zip", flexfixtures.TwoAgencyFiles())

	t.Run("search stop", func(t *testing.T) {
		assert.Equal(t, []string{"aa_X"}, listedStopIDs(t, api, "/api/where/search/stop.json?key=TEST&input=Shared"))
	})

	t.Run("stops for agency", func(t *testing.T) {
		assert.ElementsMatch(t, []string{"aa_X", "aa_Z"}, listedStopIDs(t, api, "/api/where/stops-for-agency/aa.json?key=TEST"))
		assert.Equal(t, []string{"bb_Y"}, listedStopIDs(t, api, "/api/where/stops-for-agency/bb.json?key=TEST"))
	})

	t.Run("ondemand service references", func(t *testing.T) {
		resp, model := callAPIHandler[onDemandEntryResponse](t, api, "/api/ondemand/service/bb_flexbb.json?key=TEST&geometryDetail=none")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		references := model.Data.References
		stopIDs := make([]string, 0, len(references.Stops))
		for _, stop := range references.Stops {
			stopIDs = append(stopIDs, stop.ID)
		}
		assert.Equal(t, []string{"aa_X", "bb_Y"}, stopIDs)

		require.Len(t, references.LocationGroups, 1)
		assert.Equal(t, "bb_grp_bb", references.LocationGroups[0].ID)
		assert.Equal(t, []string{"aa_X", "bb_Y"}, references.LocationGroups[0].StopIds)

		require.Len(t, model.Data.Entry.Rules, 1)
		assert.Equal(t, []string{"aa_X"}, model.Data.Entry.Rules[0].FromIds)
		assert.Equal(t, []string{"bb_grp_bb"}, model.Data.Entry.Rules[0].ToIds)
	})
}

// A flex-only stop that two agencies' services reference carries the lower
// agency id on the wire, so that agency must be in references.agencies even
// when the response holds only the other agency's service.
func TestOnDemand_StopAgencyResolvesInReferences(t *testing.T) {
	files := flexfixtures.TwoAgencyFiles()
	files["agency.txt"] += "a0,Zero Flex,http://example.com/a0,UTC\n"
	files["routes.txt"] += "flexa0,a0,0,Zero Flex,3\n"
	files["trips.txt"] += "flexa0,svc,a0-trip\n"
	files["stop_times.txt"] += "a0-trip,,,Y,,1,2,1,08:00:00,10:00:00\n" +
		"a0-trip,,,Y,,2,1,2,08:00:00,10:00:00\n"
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "flex-shared-flex-stop.zip", files)

	resp, model := callAPIHandler[onDemandEntryResponse](t, api, "/api/ondemand/service/bb_flexbb.json?key=TEST&geometryDetail=none")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	references := model.Data.References
	require.Len(t, references.LocationGroups, 1)
	assert.Contains(t, references.LocationGroups[0].StopIds, "a0_Y")

	agencyIDs := make([]string, 0, len(references.Agencies))
	for _, agency := range references.Agencies {
		agencyIDs = append(agencyIDs, agency.ID)
	}
	assert.Contains(t, agencyIDs, "a0")
}

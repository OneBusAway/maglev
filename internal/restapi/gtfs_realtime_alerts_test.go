package restapi

import (
	"strconv"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

var alertExportNow = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

func alertExportRequest(agencyID string) gtfsRealtimeExportRequest {
	return gtfsRealtimeExportRequest{AgencyID: agencyID, Time: alertExportNow, RemoveAgencyIDs: true}
}

func routeSelector(routeID, owner string) internalgtfs.ExportAlertSelector {
	return internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String(routeID)}, RouteOwner: owner}
}

// exportAlert builds an alert from selectors. Like go-gtfs parsing, a
// selector without a route type gets RouteType_Unknown, not zero (tram).
func exportAlert(recordAgencyID, rawID string, selectors ...internalgtfs.ExportAlertSelector) internalgtfs.ExportAlert {
	entities := make([]gtfs.AlertInformedEntity, 0, len(selectors))
	for i := range selectors {
		selectors[i].Entity.RouteType = gtfs.RouteType_Unknown
		entities = append(entities, selectors[i].Entity)
	}
	return internalgtfs.ExportAlert{
		RecordAgencyID: recordAgencyID,
		Alert:          gtfs.Alert{ID: rawID, InformedEntities: entities},
		Selectors:      selectors,
	}
}

func selectedAlertIDs(alerts []internalgtfs.ExportAlert) []string {
	ids := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		ids = append(ids, alert.Alert.ID)
	}
	return ids
}

func TestSelectExportAlertsByAffectedAgency(t *testing.T) {
	alerts := []internalgtfs.ExportAlert{
		exportAlert("40", "cross-agency", routeSelector("B1", "20")),
		exportAlert("40", "both-agencies", routeSelector("A1", "40"), routeSelector("B1", "20")),
		exportAlert("20", "agency-40-only", routeSelector("A1", "40")),
		exportAlert("20", "unresolved", routeSelector("unknown", "")),
	}

	assert.Equal(t, []string{"cross-agency", "both-agencies"}, selectedAlertIDs(selectExportAlerts(alerts, alertExportRequest("20"))))
	assert.Equal(t, []string{"both-agencies", "agency-40-only"}, selectedAlertIDs(selectExportAlerts(alerts, alertExportRequest("40"))))
}

func TestSelectExportAlertsIgnoresActiveWindows(t *testing.T) {
	expired := alertExportNow.Add(-48 * time.Hour)
	future := alertExportNow.Add(48 * time.Hour)
	withWindow := func(rawID string, start, end time.Time) internalgtfs.ExportAlert {
		alert := exportAlert("40", rawID, routeSelector("A1", "40"))
		alert.Alert.ActivePeriods = []gtfs.AlertActivePeriod{{StartsAt: &start, EndsAt: &end}}
		return alert
	}
	alerts := []internalgtfs.ExportAlert{
		withWindow("current", alertExportNow.Add(-time.Hour), alertExportNow.Add(time.Hour)),
		withWindow("future", future, future.Add(time.Hour)),
		withWindow("expired", expired, expired.Add(time.Hour)),
	}

	feed := buildAlertsFeed(selectExportAlerts(alerts, alertExportRequest("40")), alertExportRequest("40"))

	require.Len(t, feed.Entity, 3)
	for i, alert := range alerts {
		period := feed.Entity[i].GetAlert().GetActivePeriod()[0]
		assert.Equal(t, uint64(alert.Alert.ActivePeriods[0].StartsAt.Unix()), period.GetStart(), alert.Alert.ID)
		assert.Equal(t, uint64(alert.Alert.ActivePeriods[0].EndsAt.Unix()), period.GetEnd(), alert.Alert.ID)
	}
}

func TestBuildAlertsFeedPayload(t *testing.T) {
	tripSelector := internalgtfs.ExportAlertSelector{
		Entity: gtfs.AlertInformedEntity{TripID: &gtfs.TripID{
			ID: "T1", RouteID: "2LINE", DirectionID: gtfs.DirectionID_True,
			HasStartDate: true, StartDate: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
		}},
		TripOwner: "40",
	}
	alert := exportAlert("40", "notice",
		routeSelector("2LINE", "40"),
		internalgtfs.ExportAlertSelector{
			Entity:     gtfs.AlertInformedEntity{AgencyID: proto.String("20"), RouteID: proto.String("2LINE")},
			RouteOwner: "40",
		},
		tripSelector,
		internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{StopID: proto.String("S")}, StopOwners: []string{"20", "40"}},
	)
	alert.Alert.Cause = gtfsrt.Alert_CONSTRUCTION
	alert.Alert.Effect = gtfsrt.Alert_DETOUR
	alert.Alert.Header = []gtfs.AlertText{{Text: "Closed", Language: "en"}, {Text: "Cerrado", Language: "es"}}
	alert.Alert.Description = []gtfs.AlertText{{Text: "Use 3rd Ave", Language: "en"}, {Text: "Use la 3a", Language: "es"}}
	alert.Alert.URL = []gtfs.AlertText{{Text: "https://example.com/notice", Language: "en"}}

	feed := buildAlertsFeed([]internalgtfs.ExportAlert{alert}, alertExportRequest("40"))

	require.Len(t, feed.Entity, 1)
	got := feed.Entity[0].GetAlert()
	assert.Equal(t, gtfsrt.Alert_CONSTRUCTION, got.GetCause())
	assert.Equal(t, gtfsrt.Alert_DETOUR, got.GetEffect())
	assert.Nil(t, got.SeverityLevel, "severity is not retained, so it is not invented")
	assert.ElementsMatch(t, []string{"en:Closed", "es:Cerrado"}, translations(got.GetHeaderText()))
	assert.ElementsMatch(t, []string{"en:Use 3rd Ave", "es:Use la 3a"}, translations(got.GetDescriptionText()))
	assert.ElementsMatch(t, []string{"en:https://example.com/notice"}, translations(got.GetUrl()))

	selectors := got.GetInformedEntity()
	require.Len(t, selectors, 4)
	assert.True(t, proto.Equal(&gtfsrt.EntitySelector{AgencyId: proto.String("40"), RouteId: proto.String("2LINE")}, selectors[0]),
		"route-only selector gains its owner's agency ID: %v", selectors[0])
	assert.Equal(t, "20", selectors[1].GetAgencyId(), "explicit agency ID is kept")
	assert.True(t, proto.Equal(&gtfsrt.EntitySelector{
		AgencyId: proto.String("40"),
		Trip: &gtfsrt.TripDescriptor{
			TripId: proto.String("T1"), RouteId: proto.String("2LINE"),
			DirectionId: proto.Uint32(1), StartDate: proto.String("20261009"),
		},
	}, selectors[2]), "trip selector: %v", selectors[2])
	assert.True(t, proto.Equal(&gtfsrt.EntitySelector{StopId: proto.String("S")}, selectors[3]),
		"stop served by two agencies gets no agency ID: %v", selectors[3])
}

func translations(text *gtfsrt.TranslatedString) []string {
	var out []string
	for _, translation := range text.GetTranslation() {
		out = append(out, translation.GetLanguage()+":"+translation.GetText())
	}
	return out
}

func TestBuildAlertsFeedQualifiesPayloadIDs(t *testing.T) {
	alert := exportAlert("40", "notice",
		routeSelector("2LINE", "40"),
		routeSelector("unknown", ""),
		internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{StopID: proto.String("S")}, StopOwners: []string{"40"}},
		internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{StopID: proto.String("shared")}, StopOwners: []string{"20", "40"}},
	)
	req := alertExportRequest("40")
	req.RemoveAgencyIDs = false

	selectors := buildAlertsFeed([]internalgtfs.ExportAlert{alert}, req).Entity[0].GetAlert().GetInformedEntity()

	assert.Equal(t, "40_2LINE", selectors[0].GetRouteId())
	assert.Equal(t, "unknown", selectors[1].GetRouteId(), "unresolved owner stays raw")
	assert.Equal(t, "40_S", selectors[2].GetStopId())
	assert.Equal(t, "shared", selectors[3].GetStopId(), "ambiguous owner stays raw")
}

func TestBuildAlertsFeedEntityIDs(t *testing.T) {
	alerts := []internalgtfs.ExportAlert{
		exportAlert("40", ""),
		exportAlert("40", "16623"),
		exportAlert("40", ""),
	}
	for _, removeAgencyIDs := range []bool{true, false} {
		req := alertExportRequest("40")
		req.RemoveAgencyIDs = removeAgencyIDs

		feed := buildAlertsFeed(alerts, req)

		require.Len(t, feed.Entity, 3)
		assert.Equal(t, "40_16623", feed.Entity[1].GetId(), "removeAgencyIds=%v", removeAgencyIDs)
		seen := make(map[string]bool)
		for i, entity := range feed.Entity {
			assert.False(t, seen[entity.GetId()], "duplicate entity ID %s", entity.GetId())
			seen[entity.GetId()] = true
			if i != 1 {
				_, err := strconv.Atoi(entity.GetId())
				assert.NoError(t, err, "anonymous alerts get count-based IDs")
			}
		}
	}
}

func TestNextFallbackAlertIDSkipsUsedIDs(t *testing.T) {
	used := map[string]bool{"2": true, "3": true}

	assert.Equal(t, "4", nextFallbackAlertID(2, used))
}

func TestBuildAlertsFeedKeepsRouteType(t *testing.T) {
	alert := exportAlert("40", "notice", routeSelector("2LINE", "40"))
	alert.Selectors[0].Entity.RouteType = gtfs.RouteType_Bus

	selector := buildAlertsFeed([]internalgtfs.ExportAlert{alert}, alertExportRequest("40")).Entity[0].GetAlert().GetInformedEntity()[0]

	assert.Equal(t, int32(3), selector.GetRouteType())
}

func TestSelectExportAlertsRouteFilter(t *testing.T) {
	tripSelector := func(tripID, tripRouteID string, topLevelRouteID *string) internalgtfs.ExportAlertSelector {
		selector := internalgtfs.ExportAlertSelector{
			Entity:    gtfs.AlertInformedEntity{TripID: &gtfs.TripID{ID: tripID, RouteID: tripRouteID}, RouteID: topLevelRouteID},
			TripOwner: "40",
		}
		if topLevelRouteID != nil {
			selector.RouteOwner = "40"
		}
		return selector
	}
	stopOnly := internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{StopID: proto.String("S_ON_A")}, StopOwners: []string{"40"}}
	agencyOnly := internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{AgencyID: proto.String("40")}}
	alerts := []internalgtfs.ExportAlert{
		exportAlert("40", "two-routes",
			internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("A"), StopID: proto.String("S1")}, RouteOwner: "40"},
			internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("A"), StopID: proto.String("S2")}, RouteOwner: "40"},
			internalgtfs.ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("B"), StopID: proto.String("S3")}, RouteOwner: "40"},
		),
		exportAlert("40", "trip-only", tripSelector("T_ON_A", "", nil)),
		exportAlert("40", "stop-only", stopOnly),
		exportAlert("40", "agency-only", agencyOnly),
		exportAlert("40", "trip-route-over-top-level", tripSelector("", "A", proto.String("B"))),
		exportAlert("40", "named-trip-hint", tripSelector("T1", "A", nil)),
		exportAlert("40", "named-trip-top-level", tripSelector("T1", "A", proto.String("B"))),
	}
	tests := []struct {
		routeFilterID string
		want          []string
	}{
		{"A", []string{"two-routes", "trip-route-over-top-level"}},
		{"B", []string{"two-routes", "named-trip-top-level"}},
		{"40_A", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.routeFilterID, func(t *testing.T) {
			req := alertExportRequest("40")
			req.RouteFilterID = tt.routeFilterID

			selected := selectExportAlerts(alerts, req)

			assert.Equal(t, tt.want, selectedAlertIDs(selected))
		})
	}
}

func TestSelectExportAlertsRouteFilterKeepsAllSelectors(t *testing.T) {
	alert := exportAlert("40", "notice", routeSelector("A", "40"), routeSelector("A", "40"), routeSelector("B", "20"))
	req := alertExportRequest("40")
	req.RouteFilterID = "A"

	feed := buildAlertsFeed(selectExportAlerts([]internalgtfs.ExportAlert{alert}, req), req)

	require.Len(t, feed.Entity, 1)
	assert.Len(t, feed.Entity[0].GetAlert().GetInformedEntity(), 3)
}

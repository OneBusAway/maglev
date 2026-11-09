package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestSituationID(t *testing.T) {
	tests := []struct {
		name     string
		alertID  string
		agencyID string
		want     string
	}{
		{
			name:     "Bare alert ID gets the agency prefix",
			alertID:  "92239",
			agencyID: "1",
			want:     "1_92239",
		},
		{
			// Puget Sound's feed is exported by an OBA instance, so its alert
			// IDs already carry the prefix. Upstream reports "1_92239".
			name:     "Already-prefixed alert ID is left alone",
			alertID:  "1_92239",
			agencyID: "1",
			want:     "1_92239",
		},
		{
			name:     "A different agency's prefix is not mistaken for ours",
			alertID:  "40_92239",
			agencyID: "1",
			want:     "1_40_92239",
		},
		{
			name:     "Unknown agency leaves the ID untouched",
			alertID:  "92239",
			agencyID: "",
			want:     "92239",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SituationID(tt.alertID, tt.agencyID))
		})
	}
}

func TestAlertAgencyID(t *testing.T) {
	agencyOnly := func(agencyID string) gtfs.AlertInformedEntity {
		return gtfs.AlertInformedEntity{AgencyID: &agencyID}
	}
	routeOnly := func(routeID string) gtfs.AlertInformedEntity {
		return gtfs.AlertInformedEntity{RouteID: &routeID}
	}
	tests := []struct {
		name     string
		entities []gtfs.AlertInformedEntity
		want     string
	}{
		{
			name:     "first non-empty agency_id wins",
			entities: []gtfs.AlertInformedEntity{agencyOnly(""), agencyOnly("40"), agencyOnly("1")},
			want:     "40",
		},
		{
			name:     "route-only selectors name no agency",
			entities: []gtfs.AlertInformedEntity{routeOnly("100"), routeOnly("200")},
			want:     "",
		},
		{
			name: "no selectors",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, AlertAgencyID(gtfs.Alert{InformedEntities: tt.entities}))
		})
	}
}

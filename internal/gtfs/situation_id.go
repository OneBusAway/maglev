package gtfs

import (
	"strings"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// SituationID returns the combined-form ID for an alert.
//
// Prefixing is idempotent, not unconditional: a GTFS-RT feed exported by an OBA
// instance already carries the agency prefix on its alert IDs (Puget Sound
// ships "1_92239"), and prefixing again would publish "1_1_92239", which
// resolves against nothing a client has seen. Upstream reports "1_92239".
func SituationID(alertID, agencyID string) string {
	if agencyID == "" || strings.HasPrefix(alertID, agencyID+"_") {
		return alertID
	}
	return utils.FormCombinedID(agencyID, alertID)
}

// AlertAgencyID returns the first non-empty agency_id among the alert's
// informed entities, or "" when none names an agency.
func AlertAgencyID(alert gtfs.Alert) string {
	for _, entity := range alert.InformedEntities {
		if entity.AgencyID != nil && *entity.AgencyID != "" {
			return *entity.AgencyID
		}
	}
	return ""
}

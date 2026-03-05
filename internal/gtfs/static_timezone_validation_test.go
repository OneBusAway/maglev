package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/require"
)

func TestValidateStaticAgencyTimezones(t *testing.T) {
	t.Run("valid timezone", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "America/Los_Angeles"},
			},
		}
		require.NoError(t, validateStaticAgencyTimezones(staticData))
	})

	t.Run("empty timezone string", func(t *testing.T) {
		// Go treats LoadLocation("") as UTC, so we consider this an error for GTFS validation purposes
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: ""},
			},
		}
		require.Error(t, validateStaticAgencyTimezones(staticData))
	})

	t.Run("invalid timezone", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "Invalid/Zone"},
			},
		}
		require.Error(t, validateStaticAgencyTimezones(staticData))
	})
}

package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
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

	t.Run("explicit UTC timezone is valid", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "UTC"},
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
		err := validateStaticAgencyTimezones(staticData)
		require.Contains(t, err.Error(), "empty timezone")
	})

	t.Run("invalid timezone", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "Invalid/Zone"},
			},
		}
		err := validateStaticAgencyTimezones(staticData)
		require.Error(t, err)
		require.Contains(t, err.Error(), "a1")
	})

	t.Run("whitespace-padded timezone is normalized and valid", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "  America/Los_Angeles  "},
			},
		}
		require.NoError(t, validateStaticAgencyTimezones(staticData))
		assert.Equal(t, "America/Los_Angeles", staticData.Agencies[0].Timezone)
	})

	t.Run("whitespace-only timezone is rejected", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "   "},
			},
		}
		err := validateStaticAgencyTimezones(staticData)
		require.Contains(t, err.Error(), "empty timezone")
	})

	t.Run("multiple agencies second has invalid timezone", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "a1", Timezone: "America/Los_Angeles"},
				{Id: "a2", Timezone: "Invalid/Zone"},
			},
		}
		err := validateStaticAgencyTimezones(staticData)
		require.Contains(t, err.Error(), "a2")
	})

	t.Run("empty agency list passes validation", func(t *testing.T) {
		staticData := &gtfs.Static{
			Agencies: []gtfs.Agency{},
		}
		require.NoError(t, validateStaticAgencyTimezones(staticData))
	})
}

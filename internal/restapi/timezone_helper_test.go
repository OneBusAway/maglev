package restapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAgencyLocation(t *testing.T) {
	t.Run("valid timezone", func(t *testing.T) {
		loc, err := loadAgencyLocation("agency-1", "America/Los_Angeles")
		require.NoError(t, err)
		require.NotNil(t, loc)
		assert.Equal(t, "America/Los_Angeles", loc.String())
	})

	t.Run("invalid timezone returns error", func(t *testing.T) {
		loc, err := loadAgencyLocation("agency-1", "Invalid/Timezone")
		require.Error(t, err)
		assert.Nil(t, loc)
		assert.Contains(t, err.Error(), `invalid timezone for agency "agency-1"`)
		assert.Contains(t, err.Error(), "unknown time zone")
	})
}

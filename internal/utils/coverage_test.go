package utils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimeAndContextCoverage(t *testing.T) {
	now := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	svcDate := CalculateServiceDate(now)
	assert.Equal(t, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), svcDate)

	explicit := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	d, m := ServiceDateMillis(&explicit, now)
	assert.Equal(t, explicit, d)
	assert.Equal(t, explicit.Unix()*1000, m)

	sec := CalculateSecondsSinceServiceDate(now, svcDate)
	assert.Equal(t, int64(12*3600), sec)

	assert.Equal(t, int64(5), NanosToSeconds(5000000000))
	assert.Equal(t, int64(5), EffectiveStopTimeSeconds(5000000000, 0))

	ctx := WithValidatedID(context.Background(), "id1")
	id, _ := GetIDFromContext(ctx)
	assert.Equal(t, "id1", id)

	parsed := ParsedID{CombinedID: "c", AgencyID: "a", CodeID: "b"}
	ctx = WithParsedID(ctx, parsed)
	p, _ := GetParsedIDFromContext(ctx)
	assert.Equal(t, parsed, p)
}

func TestValidationCoverage(t *testing.T) {
	errors := ValidateLocationParams(100.0, 200.0, -10.0, -1.0, -1.0)
	assert.NotEmpty(t, errors)

	valid := ValidateLocationParams(45.0, -120.0, 1000.0, 1.0, 1.0)
	assert.Empty(t, valid)

	q, err := ValidateAndSanitizeQuery("test")
	assert.NoError(t, err)
	assert.Equal(t, "test", q)

	_, err = ValidateAndSanitizeQuery("invalid <script> query")
	assert.Error(t, err)
}

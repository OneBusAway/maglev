package utils

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

func TestExtractCodeID(t *testing.T) {
	tests := []struct {
		name        string
		combinedID  string
		expected    string
		expectError bool
	}{
		{
			name:        "Valid combined ID with underscores",
			combinedID:  "agency_123_code_456",
			expected:    "123_code_456",
			expectError: false,
		},
		{
			name:        "Simple valid ID",
			combinedID:  "agency_code",
			expected:    "code",
			expectError: false,
		},
		{
			name:        "Missing underscore",
			combinedID:  "agencycode",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Empty string",
			combinedID:  "",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Only agency ID",
			combinedID:  "agency_",
			expected:    "",
			expectError: false,
		},
		{
			name:        "Code ID with underscores",
			combinedID:  "agency_code_with_underscores",
			expected:    "code_with_underscores",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractCodeID(tt.combinedID)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestExtractAgencyID(t *testing.T) {
	tests := []struct {
		name        string
		combinedID  string
		expected    string
		expectError bool
	}{
		{
			name:        "Valid combined ID with underscores",
			combinedID:  "agency_123_code_456",
			expected:    "agency",
			expectError: false,
		},
		{
			name:        "Simple valid ID",
			combinedID:  "agency_code",
			expected:    "agency",
			expectError: false,
		},
		{
			name:        "Missing underscore",
			combinedID:  "agencycode",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Empty string",
			combinedID:  "",
			expected:    "",
			expectError: true,
		},
		{
			name:        "Empty agency ID",
			combinedID:  "_code",
			expected:    "",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractAgencyID(tt.combinedID)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestExtractAgencyIDAndCodeID(t *testing.T) {
	tests := []struct {
		name           string
		combinedID     string
		expectedAgency string
		expectedCode   string
		expectError    bool
	}{
		{
			name:           "Valid combined ID with underscores",
			combinedID:     "agency_123_code_456",
			expectedAgency: "agency",
			expectedCode:   "123_code_456",
			expectError:    false,
		},
		{
			name:           "Simple valid ID",
			combinedID:     "agency_code",
			expectedAgency: "agency",
			expectedCode:   "code",
			expectError:    false,
		},
		{
			name:           "Missing underscore",
			combinedID:     "agencycode",
			expectedAgency: "",
			expectedCode:   "",
			expectError:    true,
		},
		{
			name:           "Empty string",
			combinedID:     "",
			expectedAgency: "",
			expectedCode:   "",
			expectError:    true,
		},
		{
			name:           "Empty parts",
			combinedID:     "_",
			expectedAgency: "",
			expectedCode:   "",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agencyID, codeID, err := ExtractAgencyIDAndCodeID(tt.combinedID)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedAgency, agencyID)
				assert.Equal(t, tt.expectedCode, codeID)
			}
		})
	}
}

func TestFormCombinedID(t *testing.T) {
	tests := []struct {
		name     string
		agencyID string
		codeID   string
		expected string
	}{
		{
			name:     "Valid IDs",
			agencyID: "agency",
			codeID:   "code",
			expected: "agency_code",
		},
		{
			name:     "IDs with underscores",
			agencyID: "agency_123",
			codeID:   "code_456",
			expected: "agency_123_code_456",
		},
		{
			name:     "Empty agency ID",
			agencyID: "",
			codeID:   "code",
			expected: "",
		},
		{
			name:     "Empty code ID",
			agencyID: "agency",
			codeID:   "",
			expected: "",
		},
		{
			name:     "Both empty",
			agencyID: "",
			codeID:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormCombinedID(tt.agencyID, tt.codeID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapWheelchairBoarding(t *testing.T) {
	tests := []struct {
		name     string
		input    gtfs.WheelchairBoarding
		expected string
	}{
		{
			name:     "Possible",
			input:    gtfs.WheelchairBoarding_Possible,
			expected: models.Accessible,
		},
		{
			name:     "Not possible",
			input:    gtfs.WheelchairBoarding_NotPossible,
			expected: models.NotAccessible,
		},
		{
			name:     "Not specified (default)",
			input:    gtfs.WheelchairBoarding_NotSpecified,
			expected: models.UnknownValue,
		},
		{
			name:     "Invalid value (defaults to unknown)",
			input:    gtfs.WheelchairBoarding(99),
			expected: models.UnknownValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapWheelchairBoarding(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseFloatParam(t *testing.T) {
	tests := []struct {
		name          string
		params        url.Values
		key           string
		initialErrors map[string][]string
		expectedValue float64
		expectError   bool
	}{
		{
			name: "Valid float",
			params: url.Values{
				"lat": []string{"40.5"},
			},
			key:           "lat",
			initialErrors: nil,
			expectedValue: 40.5,
			expectError:   false,
		},
		{
			name: "Valid negative float",
			params: url.Values{
				"lon": []string{"-122.3"},
			},
			key:           "lon",
			initialErrors: nil,
			expectedValue: -122.3,
			expectError:   false,
		},
		{
			name: "Invalid float",
			params: url.Values{
				"lat": []string{"not_a_number"},
			},
			key:           "lat",
			initialErrors: nil,
			expectedValue: 0,
			expectError:   true,
		},
		{
			name:          "Missing parameter",
			params:        url.Values{},
			key:           "lat",
			initialErrors: nil,
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Empty string value",
			params: url.Values{
				"lat": []string{""},
			},
			key:           "lat",
			initialErrors: nil,
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "With existing errors",
			params: url.Values{
				"lat": []string{"invalid"},
			},
			key: "lat",
			initialErrors: map[string][]string{
				"other": {"some error"},
			},
			expectedValue: 0,
			expectError:   true,
		},
		{
			name: "Zero value",
			params: url.Values{
				"value": []string{"0"},
			},
			key:           "value",
			initialErrors: nil,
			expectedValue: 0,
			expectError:   false,
		},
		{
			name: "Scientific notation",
			params: url.Values{
				"value": []string{"1.5e2"},
			},
			key:           "value",
			initialErrors: nil,
			expectedValue: 150.0,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, errors := ParseFloatParam(tt.params, tt.key, tt.initialErrors)
			assert.Equal(t, tt.expectedValue, value)

			if tt.expectError {
				assert.Contains(t, errors, tt.key)
				assert.NotEmpty(t, errors[tt.key])
			} else {
				if _, exists := errors[tt.key]; exists {
					assert.Empty(t, errors[tt.key])
				}
			}

			// Verify initial errors are preserved
			if tt.initialErrors != nil {
				for key, vals := range tt.initialErrors {
					if key != tt.key {
						assert.Equal(t, vals, errors[key])
					}
				}
			}
		})
	}
}

func TestParseTimeParameter(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// Get current time for testing
	now := time.Now().In(loc)
	todayFormatted := now.Format("20060102")

	// Calculate yesterday
	yesterday := now.AddDate(0, 0, -1)
	yesterdayFormatted := yesterday.Format("20060102")
	yesterdayDateString := yesterday.Format("2006-01-02")
	yesterdayEpochMs := yesterday.Unix() * 1000

	tests := []struct {
		name               string
		timeParam          string
		expectedDate       string
		expectError        bool
		expectedErrorKey   string
		validateParsedTime func(t *testing.T, parsedTime time.Time)
	}{
		{
			name:         "Empty parameter uses current time",
			timeParam:    "",
			expectedDate: todayFormatted,
			expectError:  false,
			validateParsedTime: func(t *testing.T, parsedTime time.Time) {
				assert.Equal(t, now.Year(), parsedTime.Year())
				assert.Equal(t, now.Month(), parsedTime.Month())
				assert.Equal(t, now.Day(), parsedTime.Day())
			},
		},
		{
			name:         "Valid epoch timestamp (yesterday)",
			timeParam:    fmt.Sprintf("%d", yesterdayEpochMs),
			expectedDate: yesterdayFormatted,
			expectError:  false,
			validateParsedTime: func(t *testing.T, parsedTime time.Time) {
				assert.Equal(t, yesterday.Year(), parsedTime.Year())
				assert.Equal(t, yesterday.Month(), parsedTime.Month())
				assert.Equal(t, yesterday.Day(), parsedTime.Day())
			},
		},
		{
			name:         "Valid YYYY-MM-DD format (yesterday)",
			timeParam:    yesterdayDateString,
			expectedDate: yesterdayFormatted,
			expectError:  false,
			validateParsedTime: func(t *testing.T, parsedTime time.Time) {
				assert.Equal(t, yesterday.Year(), parsedTime.Year())
				assert.Equal(t, yesterday.Month(), parsedTime.Month())
				assert.Equal(t, yesterday.Day(), parsedTime.Day())
			},
		},
		{
			name:             "Invalid format",
			timeParam:        "invalid-date",
			expectedDate:     "",
			expectError:      true,
			expectedErrorKey: "time",
		},
		{
			name:         "Future date",
			timeParam:    now.AddDate(0, 0, 1).Format("2006-01-02"),
			expectedDate: now.AddDate(0, 0, 1).Format("20060102"),
			expectError:  false,
			validateParsedTime: func(t *testing.T, parsedTime time.Time) {
				tomorrow := now.AddDate(0, 0, 1)
				assert.Equal(t, tomorrow.Year(), parsedTime.Year())
				assert.Equal(t, tomorrow.Month(), parsedTime.Month())
				assert.Equal(t, tomorrow.Day(), parsedTime.Day())
			},
		},
		{
			name:         "Future epoch",
			timeParam:    fmt.Sprintf("%d", now.AddDate(0, 0, 1).Unix()*1000),
			expectedDate: now.AddDate(0, 0, 1).Format("20060102"),
			expectError:  false,
			validateParsedTime: func(t *testing.T, parsedTime time.Time) {
				tomorrow := now.AddDate(0, 0, 1)
				assert.Equal(t, tomorrow.Year(), parsedTime.Year())
				assert.Equal(t, tomorrow.Month(), parsedTime.Month())
				assert.Equal(t, tomorrow.Day(), parsedTime.Day())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dateStr, parsedTime, fieldErrors, valid := ParseTimeParameter(tt.timeParam, loc)

			if tt.expectError {
				assert.False(t, valid)
				assert.NotNil(t, fieldErrors)
				assert.Contains(t, fieldErrors, tt.expectedErrorKey)
			} else {
				assert.True(t, valid)
				assert.Nil(t, fieldErrors)
				assert.Equal(t, tt.expectedDate, dateStr)
				if tt.validateParsedTime != nil {
					tt.validateParsedTime(t, parsedTime)
				}
			}
		})
	}
}

func TestParseTimeParameter_EdgeCases(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	now := time.Now().In(loc)

	t.Run("Today at midnight (edge case)", func(t *testing.T) {
		todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		todayDateStr := todayMidnight.Format("2006-01-02")

		dateStr, parsedTime, fieldErrors, valid := ParseTimeParameter(todayDateStr, loc)

		assert.True(t, valid)
		assert.Nil(t, fieldErrors)
		assert.Equal(t, todayMidnight.Format("20060102"), dateStr)
		assert.Equal(t, todayMidnight.Year(), parsedTime.Year())
		assert.Equal(t, todayMidnight.Month(), parsedTime.Month())
		assert.Equal(t, todayMidnight.Day(), parsedTime.Day())
	})

	t.Run("Malformed YYYY-MM-DD", func(t *testing.T) {
		_, _, fieldErrors, valid := ParseTimeParameter("2024-13-45", loc)

		assert.False(t, valid)
		assert.NotNil(t, fieldErrors)
		assert.Contains(t, fieldErrors, "time")
	})

	t.Run("Non-numeric epoch", func(t *testing.T) {
		_, _, fieldErrors, valid := ParseTimeParameter("not-a-number", loc)

		assert.False(t, valid)
		assert.NotNil(t, fieldErrors)
		assert.Contains(t, fieldErrors, "time")
	})
}

func TestLoadLocationWithUTCFallBack(t *testing.T) {
	t.Run("Valid location", func(t *testing.T) {
		loc := LoadLocationWithUTCFallBack("America/Los_Angeles", "test-agency")

		assert.NotNil(t, loc)
		assert.Equal(t, "America/Los_Angeles", loc.String())
	})

	t.Run("Invalid location falls back to UTC", func(t *testing.T) {
		loc := LoadLocationWithUTCFallBack("Invalid/Timezone", "test-agency")

		assert.NotNil(t, loc)
		assert.Equal(t, time.UTC, loc)
	})
}

func TestParseMaxCount(t *testing.T) {
	tests := []struct {
		name             string
		expectError      bool
		expectedErrorKey string
		countQueryParams url.Values
		defaultCount     int
		expectedMaxCount int
	}{
		{
			name:             "Default Value, No MaxCount Provided",
			defaultCount:     100,
			expectedMaxCount: 100,
			expectError:      false,
			countQueryParams: url.Values{},
			expectedErrorKey: "maxCount",
		},
		{
			name: "Boundary Values, MaxCount is 1",
			countQueryParams: url.Values{
				"maxCount": []string{"1"},
			},
			defaultCount:     100,
			expectedMaxCount: 1,
			expectError:      false,
			expectedErrorKey: "maxCount",
		},
		{
			name: "Boundary Values, MaxCount is 250",
			countQueryParams: url.Values{
				"maxCount": []string{"250"},
			},
			defaultCount:     100,
			expectedMaxCount: 250,
			expectError:      false,
			expectedErrorKey: "maxCount",
		},
		{
			name: "Boundary Values, MaxCount is 251",
			countQueryParams: url.Values{
				"maxCount": []string{"251"},
			},
			defaultCount:     100,
			expectError:      true,
			expectedErrorKey: "maxCount",
		},
		{
			name: "MaxCount is 0",
			countQueryParams: url.Values{
				"maxCount": []string{"0"},
			},
			defaultCount:     100,
			expectError:      true,
			expectedErrorKey: "maxCount",
		},
		{
			name: "maxCount is float",
			countQueryParams: url.Values{
				"maxCount": []string{"5.9"},
			},
			defaultCount:     100,
			expectError:      true,
			expectedErrorKey: "maxCount",
		},
		{
			name: "maxCount is non numeric",
			countQueryParams: url.Values{
				"maxCount": []string{"Not a number"},
			},
			defaultCount:     100,
			expectError:      true,
			expectedErrorKey: "maxCount",
		},
		{
			name: "maxCount is negative",
			countQueryParams: url.Values{
				"maxCount": []string{"-1"},
			},
			defaultCount:     100,
			expectError:      true,
			expectedErrorKey: "maxCount",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultedMaxCount, fieldErrors := ParseMaxCount(tt.countQueryParams, tt.defaultCount, nil)
			if tt.expectError {
				assert.Contains(t, fieldErrors, tt.expectedErrorKey)

			} else {
				assert.NotContains(t, fieldErrors, tt.expectedErrorKey)
				assert.Equal(t, tt.expectedMaxCount, resultedMaxCount)
			}
		})
	}
}

func TestParsePaginationParams(t *testing.T) {
	tests := []struct {
		name           string
		urlParams      string
		expectedOffset int
		expectedLimit  int
	}{
		{
			name:           "Default values (no limit)",
			urlParams:      "",
			expectedOffset: 0,
			expectedLimit:  -1,
		},
		{
			name:           "Valid offset and limit",
			urlParams:      "?offset=10&limit=50",
			expectedOffset: 10,
			expectedLimit:  50,
		},
		{
			name:           "Valid offset and maxCount (maxCount takes priority)",
			urlParams:      "?offset=10&maxCount=50",
			expectedOffset: 10,
			expectedLimit:  50,
		},
		{
			name:           "Both limit and maxCount (maxCount wins)",
			urlParams:      "?limit=20&maxCount=50",
			expectedOffset: 0,
			expectedLimit:  50,
		},
		{
			name:           "Invalid offset (negative)",
			urlParams:      "?offset=-5",
			expectedOffset: 0,
			expectedLimit:  -1,
		},
		{
			name:           "Invalid limit (zero)",
			urlParams:      "?limit=0",
			expectedOffset: 0,
			expectedLimit:  -1,
		},
		{
			name:           "Invalid limit (negative)",
			urlParams:      "?limit=-10",
			expectedOffset: 0,
			expectedLimit:  -1,
		},
		{
			name:           "Limit exceeds max",
			urlParams:      "?limit=5000",
			expectedOffset: 0,
			expectedLimit:  1000,
		},
		{
			name:           "maxCount exceeds max",
			urlParams:      "?maxCount=5000",
			expectedOffset: 0,
			expectedLimit:  1000,
		},
		{
			name:           "Non-numeric values",
			urlParams:      "?offset=abc&limit=xyz",
			expectedOffset: 0,
			expectedLimit:  -1,
		},
		{
			name:           "Explicit offset zero and small limit",
			urlParams:      "?offset=0&limit=1",
			expectedOffset: 0,
			expectedLimit:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/test"+tt.urlParams, nil)
			offset, limit := ParsePaginationParams(req)

			assert.Equal(t, tt.expectedOffset, offset)
			assert.Equal(t, tt.expectedLimit, limit)
		})
	}
}

func TestPaginateSlice(t *testing.T) {
	tests := []struct {
		name          string
		items         []int
		offset        int
		limit         int
		expected      []int
		limitExceeded bool
	}{
		{
			name:          "No limit",
			items:         []int{1, 2, 3},
			offset:        0,
			limit:         -1,
			expected:      []int{1, 2, 3},
			limitExceeded: false,
		},
		{
			name:          "No limit with offset",
			items:         []int{1, 2, 3, 4, 5},
			offset:        2,
			limit:         -1,
			expected:      []int{3, 4, 5},
			limitExceeded: false,
		},
		{
			name:          "Limit fits exactly",
			items:         []int{1, 2, 3},
			offset:        0,
			limit:         3,
			expected:      []int{1, 2, 3},
			limitExceeded: false,
		},
		{
			name:          "Limit exceeds length",
			items:         []int{1, 2, 3},
			offset:        0,
			limit:         5,
			expected:      []int{1, 2, 3},
			limitExceeded: false,
		},
		{
			name:          "Limit causes truncation",
			items:         []int{1, 2, 3, 4, 5},
			offset:        0,
			limit:         3,
			expected:      []int{1, 2, 3},
			limitExceeded: true,
		},
		{
			name:          "Offset and limit within bounds",
			items:         []int{1, 2, 3, 4, 5},
			offset:        1,
			limit:         2,
			expected:      []int{2, 3},
			limitExceeded: true,
		},
		{
			name:          "Offset at end",
			items:         []int{1, 2, 3},
			offset:        3,
			limit:         5,
			expected:      []int{},
			limitExceeded: false,
		},
		{
			name:          "Offset beyond end",
			items:         []int{1, 2, 3},
			offset:        5,
			limit:         5,
			expected:      []int{},
			limitExceeded: false,
		},
		{
			name:          "Empty slice",
			items:         []int{},
			offset:        0,
			limit:         5,
			expected:      []int{},
			limitExceeded: false,
		},
		{
			name:          "Nil slice",
			items:         nil,
			offset:        0,
			limit:         5,
			expected:      []int{},
			limitExceeded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, limitExceeded := PaginateSlice(tt.items, tt.offset, tt.limit)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.limitExceeded, limitExceeded)
		})
	}
}

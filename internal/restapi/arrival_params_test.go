package restapi

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseMinutesValue(t *testing.T) {
	fallback := 35 * time.Minute

	tests := []struct {
		name         string
		params       url.Values
		key          string
		expected     time.Duration
		expectErrMsg string
	}{
		{
			name:     "returns fallback when key is missing",
			params:   url.Values{},
			key:      "minutesBefore",
			expected: fallback,
		},
		{
			name:     "returns fallback when value slice is empty",
			params:   url.Values{"minutesBefore": {}},
			key:      "minutesBefore",
			expected: fallback,
		},
		{
			name:     "returns fallback when value is empty string",
			params:   url.Values{"minutesBefore": {""}},
			key:      "minutesBefore",
			expected: fallback,
		},
		{
			name:     "parses valid positive minutes",
			params:   url.Values{"minutesBefore": {"15"}},
			key:      "minutesBefore",
			expected: 15 * time.Minute,
		},
		{
			name:     "parses zero minutes without error",
			params:   url.Values{"minutesBefore": {"0"}},
			key:      "minutesBefore",
			expected: 0,
		},
		{
			name:     "accepts value at 1440 minutes",
			params:   url.Values{"minutesAfter": {"1440"}},
			key:      "minutesAfter",
			expected: 1440 * time.Minute,
		},
		{
			name:     "accepts value above 1440 minutes",
			params:   url.Values{"minutesAfter": {"2000"}},
			key:      "minutesAfter",
			expected: 2000 * time.Minute,
		},
		{
			name:     "accepts value at maxArrivalWindowMinutes (35791)",
			params:   url.Values{"minutesAfter": {"35791"}},
			key:      "minutesAfter",
			expected: 35791 * time.Minute,
		},
		{
			name:     "bounds value above maxArrivalWindowMinutes to 35791",
			params:   url.Values{"minutesAfter": {"36000"}},
			key:      "minutesAfter",
			expected: 35791 * time.Minute,
		},
		{
			name:     "bounds huge integer before time.Duration conversion to prevent overflow",
			params:   url.Values{"minutesAfter": {"153722868"}},
			key:      "minutesAfter",
			expected: 35791 * time.Minute,
		},
		{
			name:     "bounds max int32 at maxArrivalWindowMinutes",
			params:   url.Values{"minutesAfter": {"2147483647"}},
			key:      "minutesAfter",
			expected: 35791 * time.Minute,
		},
		{
			name:         "reports error on int32 overflow 2147483648 and returns fallback",
			params:       url.Values{"minutesAfter": {"2147483648"}},
			key:          "minutesAfter",
			expected:     fallback,
			expectErrMsg: "must be a valid integer",
		},
		{
			name:         "reports error on 64-bit integer overflow and returns fallback",
			params:       url.Values{"minutesAfter": {"99999999999"}},
			key:          "minutesAfter",
			expected:     fallback,
			expectErrMsg: "must be a valid integer",
		},
		{
			name:         "reports error on int32 underflow and returns fallback",
			params:       url.Values{"minutesAfter": {"-2147483649"}},
			key:          "minutesAfter",
			expected:     fallback,
			expectErrMsg: "must be a valid integer",
		},
		{
			name:         "reports error on negative integer and returns fallback",
			params:       url.Values{"minutesBefore": {"-5"}},
			key:          "minutesBefore",
			expected:     fallback,
			expectErrMsg: "must be a non-negative integer",
		},
		{
			name:         "reports error on non-integer string and returns fallback",
			params:       url.Values{"minutesBefore": {"abc"}},
			key:          "minutesBefore",
			expected:     fallback,
			expectErrMsg: "must be a valid integer",
		},
		{
			name:         "reports error on floating point input and returns fallback",
			params:       url.Values{"minutesBefore": {"15.5"}},
			key:          "minutesBefore",
			expected:     fallback,
			expectErrMsg: "must be a valid integer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var recordedField, recordedMsg string
			addError := func(field, msg string) {
				recordedField = field
				recordedMsg = msg
			}

			result := parseMinutesValue(tc.params, tc.key, fallback, addError)
			assert.Equal(t, tc.expected, result)

			if tc.expectErrMsg != "" {
				assert.Equal(t, tc.key, recordedField)
				assert.Equal(t, tc.expectErrMsg, recordedMsg)
			} else {
				assert.Empty(t, recordedField)
				assert.Empty(t, recordedMsg)
			}
		})
	}
}

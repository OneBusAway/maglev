package restapi

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseMinutesValue(t *testing.T) {
	fallback := 35 * time.Minute
	maxWindow := 24 * time.Hour // 1440 minutes

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
			name:     "accepts value at maxWindow",
			params:   url.Values{"minutesAfter": {"1440"}},
			key:      "minutesAfter",
			expected: maxWindow,
		},
		{
			name:     "bounds first value above maxWindow",
			params:   url.Values{"minutesAfter": {"1441"}},
			key:      "minutesAfter",
			expected: maxWindow,
		},
		{
			name:     "bounds value at maxWindow when input exceeds limit",
			params:   url.Values{"minutesAfter": {"2000"}},
			key:      "minutesAfter",
			expected: maxWindow,
		},
		{
			name:     "bounds huge integer before time.Duration conversion to prevent overflow",
			params:   url.Values{"minutesAfter": {"153722868"}},
			key:      "minutesAfter",
			expected: maxWindow,
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

			result := parseMinutesValue(tc.params, tc.key, fallback, maxWindow, addError)
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

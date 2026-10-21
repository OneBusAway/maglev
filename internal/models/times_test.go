package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTime(t *testing.T) {
	t.Run("MarshalJSON", func(t *testing.T) {
		tests := []struct {
			name     string
			input    ModelTime
			expected string
		}{
			{
				name:     "serializes non-zero time as unix milliseconds",
				input:    NewModelTime(time.UnixMilli(1609459200000)), // 2021-01-01 00:00:00 UTC
				expected: "1609459200000",
			},
			{
				name:     "serializes zero time value as 0",
				input:    NewModelTime(time.Time{}),
				expected: "0",
			},
			{
				name:     "serializes unix epoch 0 as 0",
				input:    NewModelTime(time.UnixMilli(0)),
				expected: "0",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				bytes, err := json.Marshal(tc.input)
				require.NoError(t, err)
				assert.Equal(t, tc.expected, string(bytes))
			})
		}
	})

	t.Run("UnmarshalJSON", func(t *testing.T) {
		tests := []struct {
			name        string
			input       string
			expected    time.Time
			expectError bool
		}{
			{
				name:     "unmarshals valid unix milliseconds into time.Time",
				input:    "1609459200000",
				expected: time.UnixMilli(1609459200000),
			},
			{
				name:     "unmarshals 0 into zero time.Time value",
				input:    "0",
				expected: time.Time{},
			},
			{
				name:        "returns error on invalid non-numeric string",
				input:       `"not-a-number"`,
				expectError: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var mt ModelTime
				err := json.Unmarshal([]byte(tc.input), &mt)
				if tc.expectError {
					assert.Error(t, err)
					return
				}
				require.NoError(t, err)
				if tc.expected.IsZero() {
					assert.True(t, mt.IsZero())
				} else {
					assert.Equal(t, tc.expected.UnixMilli(), mt.UnixMilli())
				}
			})
		}
	})

	t.Run("lossy roundtrip: epoch zero roundtrips to zero time.Time", func(t *testing.T) {
		epochZero := NewModelTime(time.UnixMilli(0))
		bytes, err := json.Marshal(epochZero)
		require.NoError(t, err)
		assert.Equal(t, "0", string(bytes))

		var unmarshaled ModelTime
		err = json.Unmarshal(bytes, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, time.Time{}, unmarshaled.Time)
		assert.True(t, unmarshaled.IsZero())
	})

	t.Run("roundtrips struct with ModelTime field", func(t *testing.T) {
		type testContainer struct {
			EventTime ModelTime `json:"event_time"`
		}

		expectedTime := time.UnixMilli(1705305600000)
		original := testContainer{EventTime: NewModelTime(expectedTime)}

		bytes, err := json.Marshal(original)
		require.NoError(t, err)
		assert.Equal(t, `{"event_time":1705305600000}`, string(bytes))

		var unmarshaled testContainer
		err = json.Unmarshal(bytes, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, expectedTime.UnixMilli(), unmarshaled.EventTime.UnixMilli())
	})
}

func TestModelDuration(t *testing.T) {
	t.Run("MarshalJSON", func(t *testing.T) {
		tests := []struct {
			name     string
			input    ModelDuration
			expected string
		}{
			{
				name:     "serializes whole duration as seconds integer",
				input:    NewModelDuration(120 * time.Second),
				expected: "120",
			},
			{
				name:     "serializes zero duration as 0",
				input:    NewModelDuration(0),
				expected: "0",
			},
			{
				name:     "truncates positive sub-second duration towards zero",
				input:    NewModelDuration(1500 * time.Millisecond),
				expected: "1",
			},
			{
				name:     "truncates negative sub-second duration towards zero",
				input:    NewModelDuration(-1500 * time.Millisecond),
				expected: "-1",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				bytes, err := json.Marshal(tc.input)
				require.NoError(t, err)
				assert.Equal(t, tc.expected, string(bytes))
			})
		}
	})

	t.Run("UnmarshalJSON", func(t *testing.T) {
		tests := []struct {
			name        string
			input       string
			expected    time.Duration
			expectError bool
		}{
			{
				name:     "unmarshals seconds integer into time.Duration",
				input:    "90",
				expected: 90 * time.Second,
			},
			{
				name:     "unmarshals 0 into zero duration",
				input:    "0",
				expected: 0,
			},
			{
				name:        "returns error on invalid non-numeric string",
				input:       `"invalid-duration"`,
				expectError: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var md ModelDuration
				err := json.Unmarshal([]byte(tc.input), &md)
				if tc.expectError {
					assert.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.expected, md.Duration)
			})
		}
	})

	t.Run("roundtrips struct with ModelDuration field", func(t *testing.T) {
		type testContainer struct {
			Interval ModelDuration `json:"interval"`
		}

		original := testContainer{Interval: NewModelDuration(300 * time.Second)}

		bytes, err := json.Marshal(original)
		require.NoError(t, err)
		assert.Equal(t, `{"interval":300}`, string(bytes))

		var unmarshaled testContainer
		err = json.Unmarshal(bytes, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, 300*time.Second, unmarshaled.Interval.Duration)
	})
}

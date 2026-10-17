package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelTime(t *testing.T) {
	t.Run("creates model time correctly", func(t *testing.T) {
		now := time.Now()
		mt := NewModelTime(now)
		assert.Equal(t, now, mt.Time)
	})

	t.Run("serializes non-zero time as unix milliseconds", func(t *testing.T) {
		ts := time.UnixMilli(1609459200000) // 2021-01-01 00:00:00 UTC
		mt := NewModelTime(ts)

		bytes, err := json.Marshal(mt)
		require.NoError(t, err)
		assert.Equal(t, "1609459200000", string(bytes))
	})

	t.Run("serializes zero time and epoch zero as 0 and unmarshals back to zero time", func(t *testing.T) {
		// Zero value time.Time marshals to 0
		mtZero := NewModelTime(time.Time{})
		bytesZero, err := json.Marshal(mtZero)
		require.NoError(t, err)
		assert.Equal(t, "0", string(bytesZero))

		// Unix epoch 0 (1970-01-01 00:00:00 UTC) also marshals to 0
		mtEpoch := NewModelTime(time.UnixMilli(0))
		bytesEpoch, err := json.Marshal(mtEpoch)
		require.NoError(t, err)
		assert.Equal(t, "0", string(bytesEpoch))

		// Unmarshaling 0 yields zero time.Time{} as documented
		var unmarshaled ModelTime
		err = json.Unmarshal([]byte("0"), &unmarshaled)
		require.NoError(t, err)
		assert.True(t, unmarshaled.IsZero())
	})

	t.Run("unmarshals valid unix milliseconds into time.Time", func(t *testing.T) {
		raw := []byte("1609459200000")
		var mt ModelTime
		err := json.Unmarshal(raw, &mt)
		require.NoError(t, err)
		assert.Equal(t, int64(1609459200000), mt.UnixMilli())
	})

	t.Run("returns error when unmarshaling invalid time string", func(t *testing.T) {
		raw := []byte(`"not-a-number"`)
		var mt ModelTime
		err := json.Unmarshal(raw, &mt)
		assert.Error(t, err)
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
	t.Run("creates model duration correctly", func(t *testing.T) {
		d := 45 * time.Second
		md := NewModelDuration(d)
		assert.Equal(t, d, md.Duration)
	})

	t.Run("serializes duration as seconds integer", func(t *testing.T) {
		d := 120 * time.Second
		md := NewModelDuration(d)

		bytes, err := json.Marshal(md)
		require.NoError(t, err)
		assert.Equal(t, "120", string(bytes))
	})

	t.Run("truncates sub-second durations to whole seconds", func(t *testing.T) {
		// Positive sub-second duration truncates towards zero (1500ms -> 1s)
		pos := NewModelDuration(1500 * time.Millisecond)
		bytesPos, err := json.Marshal(pos)
		require.NoError(t, err)
		assert.Equal(t, "1", string(bytesPos))

		// Negative sub-second duration truncates towards zero (-1500ms -> -1s)
		neg := NewModelDuration(-1500 * time.Millisecond)
		bytesNeg, err := json.Marshal(neg)
		require.NoError(t, err)
		assert.Equal(t, "-1", string(bytesNeg))
	})

	t.Run("serializes zero duration as 0", func(t *testing.T) {
		md := NewModelDuration(0)

		bytes, err := json.Marshal(md)
		require.NoError(t, err)
		assert.Equal(t, "0", string(bytes))
	})

	t.Run("unmarshals seconds integer into time.Duration", func(t *testing.T) {
		raw := []byte("90")
		var md ModelDuration
		err := json.Unmarshal(raw, &md)
		require.NoError(t, err)
		assert.Equal(t, 90*time.Second, md.Duration)
	})

	t.Run("returns error when unmarshaling invalid duration string", func(t *testing.T) {
		raw := []byte(`"invalid-duration"`)
		var md ModelDuration
		err := json.Unmarshal(raw, &md)
		assert.Error(t, err)
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

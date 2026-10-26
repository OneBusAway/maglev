package restapi

import (
	"math"
	"net/url"
	"strconv"
	"time"

	"maglev.onebusaway.org/internal/utils"
)

// maxDurationMinutes caps minute-valued arrival-window parameters.
// Java's effective ceiling is 35,791 minutes (math.MaxInt32 / 60000) because
// its internal time calculations multiply integer minutes by 60,000 milliseconds,
// overflowing 32-bit signed integers above that value. Capping at 35,791 minutes
// preserves parity for every window Java answers while bounding multi-day scans.
const maxDurationMinutes = math.MaxInt32 / 60000

const (
	defaultFrequencyBefore = 2 * time.Minute
	defaultFrequencyAfter  = 30 * time.Minute
)

// parseMinutesValue reads a minute-valued window parameter, bounding it at
// maxDurationMinutes. The bound is applied before the time.Duration conversion so huge
// values cannot overflow into a negative duration.
func parseMinutesValue(queryParams url.Values, key string, fallback time.Duration, addError func(string, string)) time.Duration {
	values, ok := queryParams[key]
	if !ok || len(values) == 0 || values[0] == "" {
		return fallback
	}

	minutes, err := strconv.Atoi(values[0])
	if err != nil {
		addError(key, "must be a valid integer")
		return fallback
	}
	if minutes < 0 {
		addError(key, "must be a non-negative integer")
		return fallback
	}
	if minutes > maxDurationMinutes {
		return time.Duration(maxDurationMinutes) * time.Minute
	}
	return time.Duration(minutes) * time.Minute
}

// parseEpochMillisValue reads a time expressed as Unix milliseconds.
func parseEpochMillisValue(queryParams url.Values, key string, fallback time.Time, addError func(string, string)) time.Time {
	values, ok := queryParams[key]
	if !ok || len(values) == 0 || values[0] == "" {
		return fallback
	}

	timeMs, ok := utils.ParseEpochMillis(values[0])
	if !ok {
		addError(key, "must be a valid Unix timestamp in milliseconds")
		return fallback
	}
	return time.UnixMilli(timeMs)
}

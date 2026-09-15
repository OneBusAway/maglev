package restapi

import (
	"net/url"
	"strconv"
	"time"
)

// maxArrivalWindow caps minute-valued arrival-window parameters at one service
// day to bound the per-request stop_time scan.
const maxArrivalWindow = 24 * time.Hour

// parseMinutesValue reads a minute-valued window parameter, bounding it at
// maxWindow. The bound is applied before the time.Duration conversion so huge
// values cannot overflow into a negative duration.
func parseMinutesValue(queryParams url.Values, key string, fallback, maxWindow time.Duration, addError func(string, string)) time.Duration {
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
	if minutes > int(maxWindow/time.Minute) {
		return maxWindow
	}
	return time.Duration(minutes) * time.Minute
}

// parseEpochMillisValue reads a time expressed as Unix milliseconds.
func parseEpochMillisValue(queryParams url.Values, key string, fallback time.Time, addError func(string, string)) time.Time {
	values, ok := queryParams[key]
	if !ok || len(values) == 0 || values[0] == "" {
		return fallback
	}

	timeMs, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		addError(key, "must be a valid Unix timestamp in milliseconds")
		return fallback
	}
	return time.UnixMilli(timeMs)
}

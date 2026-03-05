package utils

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
)

// Pointer helper functions for nullable JSON fields.
// These are used to convert primitive values to pointers for fields that should
// serialize as null when no data is available, matching Java OBA's nullable wrapper types.

// IntPtr returns a pointer to the given int value.
func IntPtr(v int) *int { return &v }

// Int64Ptr returns a pointer to the given int64 value.
func Int64Ptr(v int64) *int64 { return &v }

// Float64Ptr returns a pointer to the given float64 value.
func Float64Ptr(v float64) *float64 { return &v }

func CalculateServiceDate(currentTime time.Time) time.Time {
	year, month, day := currentTime.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, currentTime.Location())
}

func ServiceDateMillis(explicitServiceDate *time.Time, currentTime time.Time) (time.Time, int64) {
	var serviceDate time.Time
	if explicitServiceDate != nil {
		serviceDate = *explicitServiceDate
	} else {
		serviceDate = CalculateServiceDate(currentTime)
	}
	// Always return midnight of the service date in the date's own timezone.
	// This ensures all endpoints return a consistent serviceDate millis value.
	midnight := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(),
		0, 0, 0, 0, serviceDate.Location())
	return serviceDate, midnight.UnixMilli()
}

// CalculateSecondsSinceServiceDate returns the number of wall-clock seconds elapsed
// since the start of the service date (midnight in the agency's timezone).
//
// Wall-clock seconds are used intentionally: GTFS stop_time values are stored as
// plain seconds-since-midnight offsets with no DST awareness. Using real elapsed
// seconds (time.Sub) would diverge from GTFS by 3600 s during the DST fallback
// ambiguous hour (e.g. when 1:30 AM occurs twice), causing wrong closest-stop
// selections and schedule offsets. Wall-clock math keeps the two in sync.
//
// The day difference is computed using UTC-normalised calendar dates to avoid any
// DST interference in the date arithmetic itself, which correctly handles overnight
// and post-midnight trips (GTFS allows stop times > 24:00:00).
func CalculateSecondsSinceServiceDate(currentTime time.Time, serviceDate time.Time) int64 {
	loc := serviceDate.Location()
	t := currentTime.In(loc)
	h, m, s := t.Clock()
	wallSeconds := int64(h*3600 + m*60 + s)

	// Normalise both calendar dates to UTC midnight so that the subtraction is
	// purely a date difference with no DST offset interference.
	syear, smonth, sday := serviceDate.In(loc).Date()
	tyear, tmonth, tday := t.Date()
	sd := time.Date(syear, smonth, sday, 0, 0, 0, 0, time.UTC)
	td := time.Date(tyear, tmonth, tday, 0, 0, 0, 0, time.UTC)
	dayDiff := int64(td.Sub(sd).Hours() / 24)

	return wallSeconds + dayDiff*86400
}

// Converts a GTFS stop-time value (stored as nanoseconds in db since midnight)
// to seconds since midnight.
func NanosToSeconds(nanos int64) int64 {
	return nanos / 1e9
}

// EffectiveStopTimeSeconds returns the effective stop time in seconds since midnight,
// using arrivalTimeNanos with a fallback to departureTimeNanos when arrival is zero.
// Both inputs are nanoseconds since midnight (the GTFS database storage format).
func EffectiveStopTimeSeconds(arrivalTimeNanos, departureTimeNanos int64) int64 {
	if arrivalTimeNanos > 0 {
		return arrivalTimeNanos / 1e9
	}
	return departureTimeNanos / 1e9
}

// ExtractCodeID extracts the `code_id` from a string in the format `{agency_id}_{code_id}`.
func ExtractCodeID(combinedID string) (string, error) {
	parts := strings.SplitN(combinedID, "_", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid format: %s", combinedID)
	}
	return parts[1], nil
}

// ExtractAgencyID extracts the `agency_id` from a string in the format `{agency_id}_{code_id}`.
func ExtractAgencyID(combinedID string) (string, error) {
	parts := strings.SplitN(combinedID, "_", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid format: %s", combinedID)
	}
	return parts[0], nil
}

// ExtractAgencyIDAndCodeID Extract AgencyIDAndCodeID extracts both `agency_id` and `code_id` from a string in the format `{agency_id}_{code_id}`.
func ExtractAgencyIDAndCodeID(combinedID string) (string, string, error) {
	parts := strings.SplitN(combinedID, "_", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", combinedID)
	}
	return parts[0], parts[1], nil
}

// FormCombinedID forms a combined ID in the format `{agency_id}_{code_id}` using the given `agencyID` and `codeID`.
func FormCombinedID(agencyID, codeID string) string {
	if codeID == "" || agencyID == "" {
		return ""
	}
	return fmt.Sprintf("%s_%s", agencyID, codeID)
}

// MapWheelchairBoarding converts GTFS wheelchair boarding values to our API format
func MapWheelchairBoarding(wheelchairBoarding gtfs.WheelchairBoarding) string {
	switch wheelchairBoarding {
	case gtfs.WheelchairBoarding_Possible:
		return models.Accessible
	case gtfs.WheelchairBoarding_NotPossible:
		return models.NotAccessible
	default:
		return models.UnknownValue
	}
}

// ParseFloatParam retrieves a float64 value from the provided URL query parameters.
// If the key is not present or the value is invalid, it returns 0 and updates the fieldErrors map.
// - params: URL query parameters.
// - key: The key to look for in the query parameters.
// - fieldErrors: A map to collect validation errors for fields.
// Returns:
// - The parsed float64 value (or 0 if invalid).
// - The updated fieldErrors map containing any validation errors.
func ParseFloatParam(params url.Values, key string, fieldErrors map[string][]string) (float64, map[string][]string) {
	if fieldErrors == nil {
		fieldErrors = make(map[string][]string)
	}

	val := params.Get(key)
	if val == "" {
		return 0, fieldErrors
	}

	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		fieldErrors[key] = append(fieldErrors[key], fmt.Sprintf("Invalid field value for field %q.", key))
	}
	return f, fieldErrors
}

func ParseRequiredFloatParam(params url.Values, key string, fieldErrors map[string][]string) (float64, map[string][]string) {
	if fieldErrors == nil {
		fieldErrors = make(map[string][]string)
	}

	val := params.Get(key)
	if val == "" {
		fieldErrors[key] = append(fieldErrors[key], fmt.Sprintf("Missing required field %q.", key))
		return 0, fieldErrors
	}

	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		fieldErrors[key] = append(fieldErrors[key], fmt.Sprintf("Invalid field value for field %q.", key))
		return 0, fieldErrors
	}
	return f, fieldErrors

}

func ParseTimeParameter(timeParam string, currentLocation *time.Location) (string, time.Time, map[string][]string, bool) {
	if timeParam == "" {
		// No time parameter, use current date
		now := time.Now().In(currentLocation)
		return now.Format("20060102"), now, nil, true
	}

	var parsedTime time.Time
	validFormat := false

	// Check if it's epoch timestamp
	if epochTime, err := strconv.ParseInt(timeParam, 10, 64); err == nil {
		// Convert epoch to time
		parsedTime = time.Unix(epochTime/1000, 0).In(currentLocation)
		validFormat = true
	} else if strings.Contains(timeParam, "-") {
		// Assume YYYY-MM-DD format
		parsedTime, err = time.Parse("2006-01-02", timeParam)
		if err == nil {
			validFormat = true
		}
	}

	if !validFormat {
		// Invalid format
		fieldErrors := map[string][]string{
			"time": {"Invalid field value for field \"time\"."},
		}
		return "", time.Time{}, fieldErrors, false
	}

	// Valid date, use it
	return parsedTime.Format("20060102"), parsedTime, nil, true
}

func LoadLocationWithUTCFallBack(timeZone string, agencyId string) *time.Location {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		slog.Warn("invalid agency timezone, using UTC",
			slog.String("agencyID", agencyId),
			slog.String("timezone", timeZone),
			slog.String("error", err.Error()))
		loc = time.UTC
	}
	return loc
}

// ParseMaxCount parses the maxCount query parameter with validation.
// It accepts a default value and enforces a maximum of 250 (matching Java's MaxCountSupport).
// Returns an error in fieldErrors if the value is <= 0 or > 250.
func ParseMaxCount(queryParams url.Values, defaultCount int, fieldErrors map[string][]string) (int, map[string][]string) {
	if fieldErrors == nil {
		fieldErrors = make(map[string][]string)
	}

	maxCount := defaultCount
	if maxCountStr := queryParams.Get("maxCount"); maxCountStr != "" {
		parsedMaxCount, err := strconv.Atoi(maxCountStr)
		if err == nil {
			maxCount = parsedMaxCount
			if maxCount <= 0 {
				fieldErrors["maxCount"] = []string{"must be greater than zero"}
				maxCount = defaultCount
			} else if maxCount > models.MaxAllowedCount {
				fieldErrors["maxCount"] = []string{"must not exceed 250"}
				maxCount = defaultCount
			}
		} else {
			fieldErrors["maxCount"] = []string{"Invalid field value for field \"maxCount\"."}
		}
	}
	return maxCount, fieldErrors
}

// a custom type for context keys to avoid collisions
type contextKey string

const (
	// ContextKeyID is the key for the raw validated ID
	ContextKeyID contextKey = "validated_id"
	// ContextKeyParsedID is the key for the split AgencyID/CodeID struct
	ContextKeyParsedID contextKey = "parsed_id"
)

// ParsedID holds the components of a combined ID
type ParsedID struct {
	CombinedID string
	AgencyID   string
	CodeID     string
}

// WithValidatedID returns a new context with the validated ID
func WithValidatedID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ContextKeyID, id)
}

// GetIDFromContext retrieves the validated ID from the context
func GetIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ContextKeyID).(string)
	return id, ok
}

// WithParsedID returns a new context with the parsed ID components
func WithParsedID(ctx context.Context, parsed ParsedID) context.Context {
	return context.WithValue(ctx, ContextKeyParsedID, parsed)
}

// GetParsedIDFromContext retrieves the parsed ID components from the context.
// Handlers using this function can assume the ID has already been pre-validated
// and safely parsed by the ID middleware.
func GetParsedIDFromContext(ctx context.Context) (ParsedID, bool) {
	parsed, ok := ctx.Value(ContextKeyParsedID).(ParsedID)
	return parsed, ok
}

// ParsePaginationParams parses offset and limit from request parameters.
// maxCount is the primary parameter for limit, falling back to limit.
// If neither is present, limit is -1 (return all).
// Default offset is 0.
func ParsePaginationParams(r *http.Request) (offset int, limit int) {
	queryParams := r.URL.Query()

	offset = 0
	if val := queryParams.Get("offset"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	limit = -1 // Default to no limit

	// Check maxCount first (OBA convention)
	if val := queryParams.Get("maxCount"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			limit = parsed
		}
	} else if val := queryParams.Get("limit"); val != "" {
		// Fallback to limit
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// Cap limit at 1000 if it's set
	if limit > 1000 {
		limit = 1000
	}

	return offset, limit
}

// PaginateSlice slices a slice based on offset and limit.
// Returns the sliced items and a boolean indicating if the limit was exceeded (more items exist).
func PaginateSlice[T any](items []T, offset, limit int) ([]T, bool) {
	if offset >= len(items) {
		return []T{}, false
	}

	// If limit is -1, return everything from offset
	if limit == -1 {
		return items[offset:], false
	}

	end := offset + limit
	limitExceeded := false
	if end < len(items) {
		limitExceeded = true
	} else {
		end = len(items)
	}

	return items[offset:end], limitExceeded
}

// MaxCommentLength defines the maximum allowed characters for a user comment
const MaxCommentLength = 500

// TruncateComment safely truncates a comment to MaxCommentLength runes.
func TruncateComment(s string) string {
	runes := []rune(s)
	if len(runes) > MaxCommentLength {
		return string(runes[:MaxCommentLength])
	}
	return s
}

// ValidateNumericParam returns the string if it's a valid float, empty string otherwise.
func ValidateNumericParam(s string) string {
	if s == "" {
		return ""
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return ""
	}
	return s
}

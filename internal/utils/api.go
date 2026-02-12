package utils

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
)

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

// GetParsedIDFromContext retrieves the parsed ID components from the context
func GetParsedIDFromContext(ctx context.Context) (ParsedID, bool) {
	parsed, ok := ctx.Value(ContextKeyParsedID).(ParsedID)
	return parsed, ok
}

package utils

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Compiled regular expressions for validation
var (
	// Allow alphanumeric, underscore, hyphen, dot, colon - common in transit IDs
	validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)

	// Detect potentially dangerous characters - more focused on injection patterns
	dangerousPattern = regexp.MustCompile(`[<>]|--|\/\*|\*\/|;.*--`)

	// Detect HTML/script tags
	htmlTagPattern = regexp.MustCompile(`<[^>]*>`)
)

// ValidateID validates that an ID is safe and within reasonable limits
func ValidateID(id string) error {
	if id == "" {
		return errors.New("id cannot be empty")
	}

	if len(id) > 100 {
		return errors.New("id too long (max 100 characters)")
	}

	if !validIDPattern.MatchString(id) {
		return errors.New("id contains invalid characters")
	}

	return nil
}

// ValidateQuery validates search query strings
func ValidateQuery(query string) error {
	// Empty queries are allowed
	if query == "" {
		return nil
	}

	if len(query) > 200 {
		return errors.New("query too long (max 200 characters)")
	}

	// Check for dangerous characters that could indicate injection attempts
	if dangerousPattern.MatchString(query) {
		return errors.New("query contains invalid characters")
	}

	return nil
}

// ValidateLatitude validates latitude values
func ValidateLatitude(lat float64) error {
	if lat < -90.0 || lat > 90.0 {
		return errors.New("latitude must be between -90 and 90")
	}
	return nil
}

// ValidateLongitude validates longitude values
func ValidateLongitude(lon float64) error {
	if lon < -180.0 || lon > 180.0 {
		return errors.New("longitude must be between -180 and 180")
	}
	return nil
}

// ValidateRadius validates radius values for location searches
func ValidateRadius(radius float64) error {
	if radius < 0 {
		return errors.New("radius must be non-negative")
	}

	// Reasonable maximum radius of 10km for transit searches
	if radius > 10000 {
		return errors.New("radius too large (max 10000 meters)")
	}

	return nil
}

// ValidateSpan validates latitude/longitude span values
func ValidateSpan(span float64) error {
	if span < 0 {
		return errors.New("span must be non-negative")
	}

	// Maximum span of 5 degrees (roughly 500km at equator)
	if span > 5.0 {
		return errors.New("span too large (max 5.0 degrees)")
	}

	return nil
}

// ValidateDate validates date strings in YYYY-MM-DD format or as a Unix millisecond integer
func ValidateDate(date string) error {
	// Empty dates are allowed (will default to current date)
	if date == "" {
		return nil
	}

	// Parsing as an integer (Unix milliseconds)
	if _, err := strconv.ParseInt(date, 10, 64); err == nil {
		return nil
	}

	// Parsing in YYYY-MM-DD format
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return errors.New("invalid date format, use YYYY-MM-DD or a Unix millisecond integer")
	}

	return nil
}

// SanitizeInput removes HTML tags and other potentially dangerous content
func SanitizeInput(input string) string {
	// Remove HTML tags
	sanitized := htmlTagPattern.ReplaceAllString(input, "")

	// Trim whitespace
	sanitized = strings.TrimSpace(sanitized)

	return sanitized
}

// ValidateLocationParams validates a complete set of location parameters
func ValidateLocationParams(lat, lon, radius, latSpan, lonSpan float64) map[string][]string {
	fieldErrors := make(map[string][]string)

	if err := ValidateLatitude(lat); err != nil {
		fieldErrors["lat"] = append(fieldErrors["lat"], err.Error())
	}

	if err := ValidateLongitude(lon); err != nil {
		fieldErrors["lon"] = append(fieldErrors["lon"], err.Error())
	}

	if radius != 0 {
		if err := ValidateRadius(radius); err != nil {
			fieldErrors["radius"] = append(fieldErrors["radius"], err.Error())
		}
	}

	if latSpan != 0 {
		if err := ValidateSpan(latSpan); err != nil {
			fieldErrors["latSpan"] = append(fieldErrors["latSpan"], err.Error())
		}
	}

	if lonSpan != 0 {
		if err := ValidateSpan(lonSpan); err != nil {
			fieldErrors["lonSpan"] = append(fieldErrors["lonSpan"], err.Error())
		}
	}

	return fieldErrors
}

// ValidateAndSanitizeQuery validates and sanitizes a search query
func ValidateAndSanitizeQuery(query string) (string, error) {
	if err := ValidateQuery(query); err != nil {
		return "", err
	}

	return SanitizeInput(query), nil
}

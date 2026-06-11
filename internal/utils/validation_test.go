package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid simple ID",
			id:      "agency_123",
			wantErr: false,
		},
		{
			name:    "valid complex ID",
			id:      "raba_1234567890",
			wantErr: false,
		},
		{
			name:    "empty ID",
			id:      "",
			wantErr: true,
			errMsg:  "id cannot be empty",
		},
		{
			name:    "ID too long",
			id:      strings.Repeat("a", 101),
			wantErr: true,
			errMsg:  "id too long (max 100 characters)",
		},
		{
			name:    "ID with invalid characters",
			id:      "agency_123<script>",
			wantErr: true,
			errMsg:  "id contains invalid characters",
		},
		{
			name:    "ID with SQL injection attempt",
			id:      "agency_'; DROP TABLE stops; --",
			wantErr: true,
			errMsg:  "id contains invalid characters",
		},
		{
			name:    "ID with path traversal",
			id:      "../../../etc/passwd",
			wantErr: true,
			errMsg:  "id contains invalid characters",
		},
		{
			name:    "valid ID with hyphens",
			id:      "agency-123_stop-456",
			wantErr: false,
		},
		{
			name:    "valid ID with dots",
			id:      "agency.123_stop.456",
			wantErr: false,
		},
		{
			name:    "valid ID with colon",
			id:      "agency_shp_654:456",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateID(tt.id)
			if tt.wantErr {
				assert.Error(t, err, "ValidateID should return error for invalid ID")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateID should not return error for valid ID")
			}
		})
	}
}

func TestValidateQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid simple query",
			query:   "downtown",
			wantErr: false,
		},
		{
			name:    "valid query with spaces",
			query:   "main street station",
			wantErr: false,
		},
		{
			name:    "empty query is valid",
			query:   "",
			wantErr: false,
		},
		{
			name:    "query too long",
			query:   strings.Repeat("a", 201),
			wantErr: true,
			errMsg:  "query too long (max 200 characters)",
		},
		{
			name:    "query with special characters",
			query:   "St. Mary's Hospital & Clinic",
			wantErr: false,
		},
		{
			name:    "query with script tags",
			query:   "<script>alert('xss')</script>",
			wantErr: true,
			errMsg:  "query contains invalid characters",
		},
		{
			name:    "query with SQL injection",
			query:   "'; DROP TABLE stops; --",
			wantErr: true,
			errMsg:  "query contains invalid characters",
		},
		{
			name:    "valid query with numbers",
			query:   "Route 123",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQuery(tt.query)
			if tt.wantErr {
				assert.Error(t, err, "ValidateQuery should return error for invalid query")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateQuery should not return error for valid query")
			}
		})
	}
}

func TestValidateLatitude(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid latitude",
			lat:     38.9072,
			wantErr: false,
		},
		{
			name:    "valid latitude at equator",
			lat:     0.0,
			wantErr: false,
		},
		{
			name:    "valid latitude at north pole",
			lat:     90.0,
			wantErr: false,
		},
		{
			name:    "valid latitude at south pole",
			lat:     -90.0,
			wantErr: false,
		},
		{
			name:    "latitude too high",
			lat:     90.1,
			wantErr: true,
			errMsg:  "latitude must be between -90 and 90",
		},
		{
			name:    "latitude too low",
			lat:     -90.1,
			wantErr: true,
			errMsg:  "latitude must be between -90 and 90",
		},
		{
			name:    "latitude way too high",
			lat:     180.0,
			wantErr: true,
			errMsg:  "latitude must be between -90 and 90",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLatitude(tt.lat)
			if tt.wantErr {
				assert.Error(t, err, "ValidateLatitude should return error for invalid latitude")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateLatitude should not return error for valid latitude")
			}
		})
	}
}

func TestValidateLongitude(t *testing.T) {
	tests := []struct {
		name    string
		lon     float64
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid longitude",
			lon:     -77.0369,
			wantErr: false,
		},
		{
			name:    "valid longitude at prime meridian",
			lon:     0.0,
			wantErr: false,
		},
		{
			name:    "valid longitude at international date line east",
			lon:     180.0,
			wantErr: false,
		},
		{
			name:    "valid longitude at international date line west",
			lon:     -180.0,
			wantErr: false,
		},
		{
			name:    "longitude too high",
			lon:     180.1,
			wantErr: true,
			errMsg:  "longitude must be between -180 and 180",
		},
		{
			name:    "longitude too low",
			lon:     -180.1,
			wantErr: true,
			errMsg:  "longitude must be between -180 and 180",
		},
		{
			name:    "longitude way too high",
			lon:     360.0,
			wantErr: true,
			errMsg:  "longitude must be between -180 and 180",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLongitude(tt.lon)
			if tt.wantErr {
				assert.Error(t, err, "ValidateLongitude should return error for invalid longitude")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateLongitude should not return error for valid longitude")
			}
		})
	}
}

func TestValidateRadius(t *testing.T) {
	tests := []struct {
		name    string
		radius  float64
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid small radius",
			radius:  100.0,
			wantErr: false,
		},
		{
			name:    "valid large radius",
			radius:  5000.0,
			wantErr: false,
		},
		{
			name:    "valid max radius",
			radius:  10000.0,
			wantErr: false,
		},
		{
			name:    "zero radius is valid",
			radius:  0.0,
			wantErr: false,
		},
		{
			name:    "negative radius",
			radius:  -100.0,
			wantErr: true,
			errMsg:  "radius must be non-negative",
		},
		{
			name:    "radius too large",
			radius:  10001.0,
			wantErr: true,
			errMsg:  "radius too large (max 10000 meters)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRadius(tt.radius)
			if tt.wantErr {
				assert.Error(t, err, "ValidateRadius should return error for invalid radius")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateRadius should not return error for valid radius")
			}
		})
	}
}

func TestValidateSpan(t *testing.T) {
	tests := []struct {
		name    string
		span    float64
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid small span",
			span:    0.01,
			wantErr: false,
		},
		{
			name:    "valid large span",
			span:    1.0,
			wantErr: false,
		},
		{
			name:    "zero span is valid",
			span:    0.0,
			wantErr: false,
		},
		{
			name:    "negative span",
			span:    -0.01,
			wantErr: true,
			errMsg:  "span must be non-negative",
		},
		{
			name:    "span too large",
			span:    5.1,
			wantErr: true,
			errMsg:  "span too large (max 5.0 degrees)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSpan(tt.span)
			if tt.wantErr {
				assert.Error(t, err, "ValidateSpan should return error for invalid span")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateSpan should not return error for valid span")
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal input unchanged",
			input:    "normal input",
			expected: "normal input",
		},
		{
			name:     "script tags removed",
			input:    "<script>alert('xss')</script>normal",
			expected: "alert('xss')normal",
		},
		{
			name:     "html tags removed",
			input:    "<div>content</div>",
			expected: "content",
		},
		{
			name:     "multiple tags removed",
			input:    "<p><strong>bold</strong> text</p>",
			expected: "bold text",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "only tags",
			input:    "<script></script><div></div>",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeInput(tt.input)
			assert.Equal(t, tt.expected, result, "SanitizeInput should return expected result")
		})
	}
}

func TestValidateDate(t *testing.T) {
	// The expected error message based on your updated validation.go
	expectedErrMsg := "invalid date format, use YYYY-MM-DD or a Unix millisecond integer"

	tests := []struct {
		name    string
		date    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid date",
			date:    "2023-12-25",
			wantErr: false,
		},
		{
			name:    "valid leap year date",
			date:    "2024-02-29",
			wantErr: false,
		},
		{
			name:    "valid unix timestamp in milliseconds",
			date:    "1749686400000",
			wantErr: false,
		},
		{
			name:    "zero timestamp",
			date:    "0",
			wantErr: false,
		},
		{
			name:    "negative timestamp",
			date:    "-1000",
			wantErr: true,
			errMsg:  "unix millisecond timestamp out of reasonable bounds",
		},
		{
			name:    "extremely large timestamp",
			date:    "999999999999999999",
			wantErr: true,
			errMsg:  "unix millisecond timestamp out of reasonable bounds",
		},
		{
			name:    "empty date is valid",
			date:    "",
			wantErr: false,
		},
		{
			name:    "invalid date format",
			date:    "12/25/2023",
			wantErr: true,
			errMsg:  expectedErrMsg,
		},
		{
			name:    "invalid date",
			date:    "2023-13-01",
			wantErr: true,
			errMsg:  expectedErrMsg,
		},
		{
			name:    "invalid leap year date",
			date:    "2023-02-29",
			wantErr: true,
			errMsg:  expectedErrMsg,
		},
		{
			name:    "date with invalid characters",
			date:    "2023-01-01<script>",
			wantErr: true,
			errMsg:  expectedErrMsg,
		},
		{
			name:    "completely invalid string",
			date:    "not-a-date-or-number",
			wantErr: true,
			errMsg:  expectedErrMsg,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDate(tt.date)
			if tt.wantErr {
				assert.Error(t, err, "ValidateDate should return error for invalid date")
				assert.Contains(t, err.Error(), tt.errMsg, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "ValidateDate should not return error for valid date")
			}
		})
	}
}

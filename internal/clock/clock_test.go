package clock

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClock_Now(t *testing.T) {
	c := RealClock{}
	before := time.Now()
	result := c.Now()
	after := time.Now()

	assert.False(t, result.Before(before), "RealClock.Now() should not be before the call")
	assert.False(t, result.After(after), "RealClock.Now() should not be after the call")
}

func TestRealClock_NowUnixMilli(t *testing.T) {
	c := RealClock{}
	before := time.Now().UnixMilli()
	result := c.NowUnixMilli()
	after := time.Now().UnixMilli()

	assert.GreaterOrEqual(t, result, before)
	assert.LessOrEqual(t, result, after)
}

func TestMockClock_Now(t *testing.T) {
	fixedTime := time.Date(2024, 6, 15, 8, 30, 0, 0, time.UTC)
	c := NewMockClock(fixedTime)

	assert.Equal(t, fixedTime, c.Now())
	// Should return the same time on repeated calls
	assert.Equal(t, fixedTime, c.Now())
}

func TestMockClock_NowUnixMilli(t *testing.T) {
	fixedTime := time.Date(2024, 6, 15, 8, 30, 0, 0, time.UTC)
	c := NewMockClock(fixedTime)

	expected := fixedTime.UnixMilli()
	assert.Equal(t, expected, c.NowUnixMilli())
}

func TestMockClock_Set(t *testing.T) {
	initialTime := time.Date(2024, 6, 15, 8, 0, 0, 0, time.UTC)
	newTime := time.Date(2024, 12, 25, 12, 0, 0, 0, time.UTC)

	c := NewMockClock(initialTime)
	assert.Equal(t, initialTime, c.Now())

	c.Set(newTime)
	assert.Equal(t, newTime, c.Now())
}

func TestMockClock_Advance(t *testing.T) {
	initialTime := time.Date(2024, 6, 15, 8, 0, 0, 0, time.UTC)
	c := NewMockClock(initialTime)

	// Advance by 1 hour
	c.Advance(1 * time.Hour)
	expected := time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, c.Now())

	// Advance by 30 minutes
	c.Advance(30 * time.Minute)
	expected = time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)
	assert.Equal(t, expected, c.Now())

	// Advance by negative duration (go back in time)
	c.Advance(-1 * time.Hour)
	expected = time.Date(2024, 6, 15, 8, 30, 0, 0, time.UTC)
	assert.Equal(t, expected, c.Now())
}

func TestEnvironmentClock_FallbackToSystemTime(t *testing.T) {
	// When no sources are configured, should fall back to system time
	c := NewEnvironmentClock("", "", time.Local)

	before := time.Now()
	result := c.Now()
	after := time.Now()

	assert.False(t, result.Before(before), "EnvironmentClock.Now() should not be before the call")
	assert.False(t, result.After(after), "EnvironmentClock.Now() should not be after the call")
}

func TestEnvironmentClock_FromEnvVar(t *testing.T) {
	const envVarName = "TEST_CLOCK_TIME"
	expectedTime := time.Date(2024, 12, 25, 10, 30, 0, 0, time.UTC)

	// Set the environment variable
	t.Setenv(envVarName, expectedTime.Format(time.RFC3339))

	c := NewEnvironmentClock(envVarName, "", time.UTC)
	result := c.Now()

	assert.Equal(t, expectedTime, result)
}

func TestEnvironmentClock_FromEnvVar_InvalidValue(t *testing.T) {
	const envVarName = "TEST_CLOCK_EMPTY"

	// Ensure env var is empty
	t.Setenv(envVarName, "")

	c := NewEnvironmentClock(envVarName, "", time.Local)

	before := time.Now()
	result := c.Now()
	after := time.Now()

	// Should fall back to system time
	assert.False(t, result.Before(before))
	assert.False(t, result.After(after))

	// Should return current time on invalid value
	t.Setenv(envVarName, "2025-1")
	before = time.Now()
	result = c.Now()
	after = time.Now()

	assert.False(t, result.Before(before))
	assert.False(t, result.After(after))
}

func TestEnvironmentClock_FromFile(t *testing.T) {
	expectedTime := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)

	// Create a temp file with the time
	tmpFile, err := os.CreateTemp("", "clock_test_*.txt")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(expectedTime.Format(time.RFC3339))
	assert.NoError(t, err)
	_ = tmpFile.Close()

	c := NewEnvironmentClock("", tmpFile.Name(), time.UTC)
	result := c.Now()

	assert.Equal(t, expectedTime, result)
}

func TestEnvironmentClock_FromFile_WithNewline(t *testing.T) {
	expectedTime := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)

	// Create a temp file with the time and trailing newline
	tmpFile, err := os.CreateTemp("", "clock_test_*.txt")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(expectedTime.Format(time.RFC3339) + "\n")
	assert.NoError(t, err)
	_ = tmpFile.Close()

	c := NewEnvironmentClock("", tmpFile.Name(), time.UTC)
	result := c.Now()

	assert.Equal(t, expectedTime, result)
}

func TestEnvironmentClock_EnvVarPriorityOverFile(t *testing.T) {
	const envVarName = "TEST_CLOCK_PRIORITY"
	envTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fileTime := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	// Set env var
	t.Setenv(envVarName, envTime.Format(time.RFC3339))

	// Create file
	tmpFile, err := os.CreateTemp("", "clock_test_*.txt")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(fileTime.Format(time.RFC3339))
	assert.NoError(t, err)
	_ = tmpFile.Close()

	c := NewEnvironmentClock(envVarName, tmpFile.Name(), time.UTC)
	result := c.Now()

	// Should use env var, not file
	assert.Equal(t, envTime, result)
}

func TestEnvironmentClock_ParseTimeFormats(t *testing.T) {
	const envVarName = "TEST_CLOCK_FORMAT"
	loc := time.UTC

	tests := []struct {
		name     string
		input    string
		expected time.Time
	}{
		{
			name:     "RFC3339",
			input:    "2024-06-15T10:30:00Z",
			expected: time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "RFC3339 with timezone",
			input:    "2024-06-15T10:30:00+05:30",
			expected: time.Date(2024, 6, 15, 10, 30, 0, 0, time.FixedZone("", 5*3600+30*60)),
		},
		{
			name:     "DateTime with space",
			input:    "2024-06-15 10:30:00",
			expected: time.Date(2024, 6, 15, 10, 30, 0, 0, loc),
		},
		{
			name:     "DateTime with T",
			input:    "2024-06-15T10:30:00",
			expected: time.Date(2024, 6, 15, 10, 30, 0, 0, loc),
		},
		{
			name:     "Date only",
			input:    "2024-06-15",
			expected: time.Date(2024, 6, 15, 0, 0, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		// Test env var source
		t.Run("env_"+tt.name, func(t *testing.T) {
			t.Setenv(envVarName, tt.input)

			c := NewEnvironmentClock(envVarName, "", loc)
			result := c.Now()

			assert.True(t, tt.expected.Equal(result),
				"expected %v, got %v", tt.expected, result)
		})

		// Test file source
		t.Run("file_"+tt.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "clock_test_*.txt")
			assert.NoError(t, err)
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			_, err = tmpFile.WriteString(tt.input)
			assert.NoError(t, err)
			_ = tmpFile.Close()

			c := NewEnvironmentClock("", tmpFile.Name(), loc)
			result := c.Now()

			assert.True(t, tt.expected.Equal(result),
				"expected %v, got %v", tt.expected, result)
		})
	}
}

func TestEnvironmentClock_NowUnixMilli(t *testing.T) {
	const envVarName = "TEST_CLOCK_MILLI"
	expectedTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	t.Setenv(envVarName, expectedTime.Format(time.RFC3339))

	c := NewEnvironmentClock(envVarName, "", time.UTC)

	assert.Equal(t, expectedTime.UnixMilli(), c.NowUnixMilli())
}

func TestEnvironmentClock_InvalidTimeFormat(t *testing.T) {
	const envVarName = "TEST_CLOCK_INVALID"

	tests := []string{
		"not-a-valid-time",
		"2025-",
		"2025-01-010",
		"-2021-01-01",
	}

	for _, tt := range tests {
		// Test env var source
		t.Run("env_"+tt, func(t *testing.T) {
			t.Setenv(envVarName, tt)

			c := NewEnvironmentClock(envVarName, "", time.Local)

			before := time.Now()
			result := c.Now()
			after := time.Now()

			// Should fall back to system time
			assert.False(t, result.Before(before))
			assert.False(t, result.After(after))
		})

		// Test file source
		t.Run("file_"+tt, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "clock_test_*.txt")
			assert.NoError(t, err)
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			_, err = tmpFile.WriteString(tt)
			assert.NoError(t, err)
			_ = tmpFile.Close()

			c := NewEnvironmentClock("", tmpFile.Name(), time.Local)

			before := time.Now()
			result := c.Now()
			after := time.Now()

			// Should fall back to system time
			assert.False(t, result.Before(before))
			assert.False(t, result.After(after))
		})
	}
}

func TestEnvironmentClock_NonExistentFile(t *testing.T) {
	c := NewEnvironmentClock("", "/nonexistent/path/to/file.txt", time.Local)

	before := time.Now()
	result := c.Now()
	after := time.Now()

	// Should fall back to system time
	assert.False(t, result.Before(before))
	assert.False(t, result.After(after))
}

func TestEnvironmentClock_NilLocation(t *testing.T) {
	const envVarName = "TEST_CLOCK_NIL_LOC"

	// Set a non-RFC3339 format that requires location for parsing
	t.Setenv(envVarName, "2024-06-15 10:30:00")

	c := NewEnvironmentClock(envVarName, "", nil)

	// Should fall back to system time since location is nil and format is not RFC3339
	before := time.Now()
	result := c.Now()
	after := time.Now()

	assert.False(t, result.Before(before), "should fall back to system time")
	assert.False(t, result.After(after), "should fall back to system time")
}

func TestEnvironmentClock_NilLocation_RFC3339Works(t *testing.T) {
	const envVarName = "TEST_CLOCK_NIL_LOC_RFC"
	expectedTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	// Set an RFC3339 format (includes timezone, so location is not needed)
	t.Setenv(envVarName, "2024-06-15T10:30:00Z")

	c := NewEnvironmentClock(envVarName, "", nil)

	result := c.Now()

	// RFC3339 should work even with nil location since it has embedded timezone
	assert.Equal(t, expectedTime, result)
}

// TestMockClock_ConcurrentAccess verifies thread-safety of MockClock.
// Run with '-race' flag to detect race conditions.
func TestMockClock_ConcurrentAccess(t *testing.T) {
	initialTime := time.Date(2024, 6, 15, 8, 0, 0, 0, time.UTC)
	c := NewMockClock(initialTime)

	const goroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3) // readers, setters, and advancers

	// Concurrent readers
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				_ = c.Now()
				_ = c.NowUnixMilli()
			}
		}()
	}

	// Concurrent setters
	for i := range goroutines {
		go func(offset int) {
			defer wg.Done()
			for j := range iterations {
				c.Set(initialTime.Add(time.Duration(offset+j) * time.Second))
			}
		}(i)
	}

	// Concurrent advancers
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				c.Advance(time.Millisecond)
			}
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// If we reach here without panics or race detector errors, the test passes
	// Just verify the clock still works
	_ = c.Now()
}

// Package clock provides time abstraction for testing and production use.
// It enables deterministic testing of time-dependent logic by allowing
// injection of mock clocks that return controlled time values.
package clock

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// Clock provides an abstraction for time operations.
// Use RealClock in production and MockClock in tests.
type Clock interface {
	// Now returns the current time
	Now() time.Time
	// NowUnixMilli returns the current time as Unix milliseconds
	NowUnixMilli() int64
}

// RealClock implements Clock using actual system time.
// This is the default implementation for production use.
type RealClock struct{}

// Now returns the current system time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// NowUnixMilli returns the current time as Unix milliseconds.
func (RealClock) NowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

// MockClock implements Clock with a controllable time for testing.
// Use NewMockClock to create instances.
type MockClock struct {
	currentTime time.Time
	mu          sync.Mutex
}

// NewMockClock creates a new MockClock set to the specified time.
func NewMockClock(t time.Time) *MockClock {
	return &MockClock{currentTime: t}
}

// Now returns the mock clock's current time.
func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime
}

// NowUnixMilli returns the mock clock's current time as Unix milliseconds.
func (m *MockClock) NowUnixMilli() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime.UnixMilli()
}

// Set changes the mock clock's current time.
func (m *MockClock) Set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTime = t
}

// Advance moves the mock clock forward by the specified duration.
func (m *MockClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTime = m.currentTime.Add(d)
}

// EnvironmentClock implements Clock using a time from an environment variable or file.
// The time is synced on each call to Now() or NowUnixMilli().
type EnvironmentClock struct {
	envVar   string
	filePath string
	mutex    *sync.Mutex
	location *time.Location
}

// NewEnvironmentClock creates a new EnvironmentClock with the given options.
// If no sources are configured, it will fall back to system time.
func NewEnvironmentClock(envVar string, filePath string, location *time.Location) *EnvironmentClock {
	e := &EnvironmentClock{
		envVar:   envVar,
		filePath: filePath,
		mutex:    &sync.Mutex{},
		location: location,
	}
	return e
}

// Now returns the current time by checking sources in priority order:
// 1. Environment variable
// 2. File
// 3. System time (fallback)
func (e *EnvironmentClock) Now() time.Time {
	if t, err := e.syncFromEnvVar(); err == nil {
		return t
	}
	if t, err := e.syncFromFile(); err == nil {
		return t
	}
	return time.Now()
}

// NowUnixMilli returns the current time as Unix milliseconds.
func (e *EnvironmentClock) NowUnixMilli() int64 {
	return e.Now().UnixMilli()
}

// syncFromEnvVar attempts to read and parse time from the configured environment variable.
// Returns the parsed time or an error if the env var is not set, empty, or contains invalid time.
func (e *EnvironmentClock) syncFromEnvVar() (time.Time, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if e.envVar == "" {
		return time.Time{}, errors.New("environment variable name not configured")
	}
	timeStr := os.Getenv(e.envVar)
	if timeStr == "" {
		return time.Time{}, errors.New("environment variable is empty: " + e.envVar)
	}
	t, err := e.parseTime(timeStr)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// syncFromFile attempts to read and parse time from the configured file.
// Returns the parsed time or an error if the file path is not set, unreadable, or contains invalid time.
func (e *EnvironmentClock) syncFromFile() (time.Time, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if e.filePath == "" {
		return time.Time{}, errors.New("file path not configured")
	}
	data, err := os.ReadFile(e.filePath)
	if err != nil {
		return time.Time{}, err
	}
	timeStr := string(data)
	t, err := e.parseTime(timeStr)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// parseTime attempts to parse a time string using multiple common formats.
func (e *EnvironmentClock) parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	// Try RFC3339 first (includes timezone)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try common formats without timezone, using configured location
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, format := range formats {
		if t, err := time.ParseInLocation(format, s, e.location); err == nil {
			return t, nil
		}
	}

	return time.Time{}, errors.New("unable to parse time: " + s)
}

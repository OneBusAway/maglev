package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/appconf"
)

func TestNewConfig(t *testing.T) {
	dbPath := "/path/to/database.db"
	env := appconf.Production

	config := NewConfig(dbPath, env)

	assert.Equal(t, dbPath, config.DBPath, "DBPath should match input")
	assert.Equal(t, env, config.Env, "Env should match input")
}

func TestNewConfigWithDevelopmentEnv(t *testing.T) {
	dbPath := ":memory:"
	env := appconf.Development

	config := NewConfig(dbPath, env)

	assert.Equal(t, dbPath, config.DBPath)
	assert.Equal(t, env, config.Env)
}

func TestNewConfigWithTestEnv(t *testing.T) {
	dbPath := "test.db"
	env := appconf.Test

	config := NewConfig(dbPath, env)

	assert.Equal(t, dbPath, config.DBPath)
	assert.Equal(t, env, config.Env)
}

func TestNewConfigWithEmptyDBPath(t *testing.T) {
	dbPath := ""
	env := appconf.Development

	config := NewConfig(dbPath, env)

	assert.Equal(t, "", config.DBPath, "Empty DBPath should be allowed")
	assert.Equal(t, env, config.Env)
}

func TestConfigStruct(t *testing.T) {
	// Test that Config struct can be created directly
	config := Config{
		DBPath: "/custom/path.db",
		Env:    appconf.Production,
	}

	assert.Equal(t, "/custom/path.db", config.DBPath)
	assert.Equal(t, appconf.Production, config.Env)
}

func TestNewConfigAllEnvironments(t *testing.T) {
	tests := []struct {
		name string
		env  appconf.Environment
	}{
		{"Development environment", appconf.Development},
		{"Test environment", appconf.Test},
		{"Production environment", appconf.Production},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewConfig("test.db", tt.env)

			assert.Equal(t, "test.db", config.DBPath)
			assert.Equal(t, tt.env, config.Env)
		})
	}
}

func TestSafeBatchSize(t *testing.T) {
	config := Config{}

	tests := []struct {
		name         string
		fieldsPerRow int
		want         int
		expectPanic  bool
	}{
		{
			name:         "zero fieldsPerRow panics",
			fieldsPerRow: 0,
			expectPanic:  true,
		},
		{
			name:         "negative fieldsPerRow panics",
			fieldsPerRow: -1,
			expectPanic:  true,
		},
		{
			name:         "10 fields per row (stop_times)",
			fieldsPerRow: 10,
			want:         3276, // 32766 / 10
		},
		{
			name:         "5 fields per row (shapes)",
			fieldsPerRow: 5,
			want:         6553, // 32766 / 5
		},
		{
			name:         "1 field per row",
			fieldsPerRow: 1,
			want:         32766,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectPanic {
				assert.Panics(t, func() { config.SafeBatchSize(tt.fieldsPerRow) })
				return
			}
			got := config.SafeBatchSize(tt.fieldsPerRow)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, got*tt.fieldsPerRow, sqliteMaxVariables)
		})
	}
}

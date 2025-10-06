package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/appconf"
)

func TestNewConfig(t *testing.T) {
	dbPath := "/path/to/database.db"
	env := appconf.Production
	verbose := true

	config := NewConfig(dbPath, env, verbose)

	assert.Equal(t, dbPath, config.DBPath, "DBPath should match input")
	assert.Equal(t, env, config.Env, "Env should match input")
	assert.Equal(t, verbose, config.verbose, "verbose should match input")
}

func TestNewConfigWithDevelopmentEnv(t *testing.T) {
	dbPath := ":memory:"
	env := appconf.Development
	verbose := false

	config := NewConfig(dbPath, env, verbose)

	assert.Equal(t, dbPath, config.DBPath)
	assert.Equal(t, env, config.Env)
	assert.Equal(t, false, config.verbose)
}

func TestNewConfigWithTestEnv(t *testing.T) {
	dbPath := "test.db"
	env := appconf.Test
	verbose := true

	config := NewConfig(dbPath, env, verbose)

	assert.Equal(t, dbPath, config.DBPath)
	assert.Equal(t, env, config.Env)
	assert.Equal(t, true, config.verbose)
}

func TestNewConfigWithEmptyDBPath(t *testing.T) {
	dbPath := ""
	env := appconf.Development
	verbose := false

	config := NewConfig(dbPath, env, verbose)

	assert.Equal(t, "", config.DBPath, "Empty DBPath should be allowed")
	assert.Equal(t, env, config.Env)
	assert.Equal(t, verbose, config.verbose)
}

func TestConfigStruct(t *testing.T) {
	// Test that Config struct can be created directly
	config := Config{
		DBPath:  "/custom/path.db",
		Env:     appconf.Production,
		verbose: true,
	}

	assert.Equal(t, "/custom/path.db", config.DBPath)
	assert.Equal(t, appconf.Production, config.Env)
	assert.Equal(t, true, config.verbose)
}

func TestNewConfigAllEnvironments(t *testing.T) {
	tests := []struct {
		name    string
		env     appconf.Environment
		verbose bool
	}{
		{"Development environment", appconf.Development, false},
		{"Test environment", appconf.Test, true},
		{"Production environment", appconf.Production, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := NewConfig("test.db", tt.env, tt.verbose)

			assert.Equal(t, "test.db", config.DBPath)
			assert.Equal(t, tt.env, config.Env)
			assert.Equal(t, tt.verbose, config.verbose)
		})
	}
}

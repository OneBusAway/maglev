package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// runMainEnvVar makes the test binary run main() with the arguments in
// runMainArgsEnvVar instead of running tests, so main can exit the process.
const (
	runMainEnvVar     = "MAGLEV_TEST_RUN_MAIN"
	runMainArgsEnvVar = "MAGLEV_TEST_MAIN_ARGS"
)

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnvVar) == "1" {
		os.Args = append([]string{"maglev"}, strings.Fields(os.Getenv(runMainArgsEnvVar))...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// flagPathConfigEnvVars are applied on the flag startup path. Subprocess tests
// always force these (and GTFS_PROTECTED_API_KEYS) to known values so ambient
// host/CI env cannot flake dumps.
var flagPathConfigEnvVars = []string{
	"GTFS_API_KEYS",
	"GTFS_STATIC_AUTH_NAME",
	"GTFS_STATIC_AUTH_VALUE",
	"GTFS_REALTIME_AUTH_NAME",
	"GTFS_REALTIME_AUTH_VALUE",
	"GTFS_PROTECTED_API_KEYS",
}

func withoutEnvKeys(environ []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := drop[key]; skip {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// runMainWithArgs runs main() in a subprocess and returns its exit code and output.
// env overlays replace the scrubbed flag-path config vars (default empty).
func runMainWithArgs(t *testing.T, args string, env map[string]string) (int, string) {
	t.Helper()

	cmd := exec.Command(os.Args[0])
	childEnv := withoutEnvKeys(os.Environ(), flagPathConfigEnvVars...)
	merged := make(map[string]string, len(flagPathConfigEnvVars))
	for _, key := range flagPathConfigEnvVars {
		merged[key] = ""
	}
	for key, value := range env {
		merged[key] = value
	}
	for key, value := range merged {
		childEnv = append(childEnv, key+"="+value)
	}
	cmd.Env = append(childEnv, runMainEnvVar+"=1", runMainArgsEnvVar+"="+args)
	output, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(output)
	}
	require.NoError(t, err)
	return 0, string(output)
}

func parseDumpConfigJSON(t *testing.T, output string) map[string]any {
	t.Helper()
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	require.GreaterOrEqual(t, start, 0, "dump JSON object start not found: %s", output)
	require.Greater(t, end, start, "dump JSON object end not found: %s", output)

	var dumped map[string]any
	require.NoError(t, json.Unmarshal([]byte(output[start:end+1]), &dumped), output)
	return dumped
}

func TestMain_CommandLineFlagsPassValidation(t *testing.T) {
	tests := []struct {
		name                string
		args                string
		protectedAPIKeysEnv string
		wantExitCode        int
		wantOutput          string
		wantProtectedKeys   string
	}{
		{"development uses default protected key", "-env development", "", 0, `"env": "development"`, `"protected-api-keys": "***REDACTED*** (1 keys)"`},
		{"test uses default protected key", "-env test", "", 0, `"env": "test"`, `"protected-api-keys": "***REDACTED*** (1 keys)"`},
		{"production accepts protected keys flag", "-env production -protected-api-keys prod-key,prod-key-2", "", 0, `"env": "production"`, `"protected-api-keys": "***REDACTED*** (2 keys)"`},
		{"production accepts protected keys env var", "-env production", "prod-key, prod-key-2", 0, `"env": "production"`, `"protected-api-keys": "***REDACTED*** (2 keys)"`},
		{"production requires a protected key", "-env production", "", 1, "protected-api-keys cannot be empty", ""},
		{"default role is all", "-env development", "", 0, `"role": "all"`, ""},
		{"server role is accepted", "-env development -role server", "", 0, `"role": "server"`, ""},
		{"ingester role is accepted", "-env development -role ingester", "", 0, `"role": "ingester"`, ""},
		{"invalid role is rejected", "-env development -role invalid", "", 1, "role must be one of [all, ingester, server]", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, output := runMainWithArgs(t, "-dump-config "+tc.args, map[string]string{
				"GTFS_PROTECTED_API_KEYS": tc.protectedAPIKeysEnv,
			})

			assert.Equal(t, tc.wantExitCode, exitCode, output)
			assert.Contains(t, output, tc.wantOutput)
			if tc.wantProtectedKeys != "" {
				assert.Contains(t, output, tc.wantProtectedKeys)
				assert.NotContains(t, output, "prod-key")
				assert.NotContains(t, output, "protected-test-key")
			}
		})
	}
}

func TestMain_FlagPathEnvironmentOverrides(t *testing.T) {
	tests := []struct {
		name  string
		args  string
		env   map[string]string
		check func(t *testing.T, exitCode int, output string)
	}{
		{
			name: "GTFS_API_KEYS replaces default test key",
			args: "-env development -data-path :memory:",
			env:  map[string]string{"GTFS_API_KEYS": "env1,env2"},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 0, exitCode, output)
				dumped := parseDumpConfigJSON(t, output)
				assert.Equal(t, "***REDACTED*** (2 keys)", dumped["api-keys"])
				assert.NotContains(t, output, "env1")
				assert.NotContains(t, output, "env2")
			},
		},
		{
			name: "explicit -api-keys wins over GTFS_API_KEYS",
			args: "-env development -data-path :memory: -api-keys one",
			env:  map[string]string{"GTFS_API_KEYS": "env1,env2"},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 0, exitCode, output)
				dumped := parseDumpConfigJSON(t, output)
				assert.Equal(t, "***REDACTED*** (1 keys)", dumped["api-keys"])
				assert.NotContains(t, output, "env1")
			},
		},
		{
			name: "explicit -api-keys=test still skips GTFS_API_KEYS",
			args: "-env development -data-path :memory: -api-keys test",
			env:  map[string]string{"GTFS_API_KEYS": "env1,env2"},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 0, exitCode, output)
				dumped := parseDumpConfigJSON(t, output)
				assert.Equal(t, "***REDACTED*** (1 keys)", dumped["api-keys"])
			},
		},
		{
			name: "duplicate GTFS_API_KEYS fail validation",
			args: "-env development -data-path :memory:",
			env:  map[string]string{"GTFS_API_KEYS": "dup,dup"},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 1, exitCode, output)
				assert.Contains(t, output, "duplicate API key")
			},
		},
		{
			name: "static and realtime auth env appear in dump",
			args: "-env development -data-path :memory:",
			env: map[string]string{
				"GTFS_STATIC_AUTH_NAME":    "X-Api-Key",
				"GTFS_STATIC_AUTH_VALUE":   "static-secret",
				"GTFS_REALTIME_AUTH_NAME":  "X-Api-Key",
				"GTFS_REALTIME_AUTH_VALUE": "rt-secret",
			},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 0, exitCode, output)
				dumped := parseDumpConfigJSON(t, output)

				staticFeeds, ok := dumped["gtfs-static-feeds"].([]any)
				require.True(t, ok)
				require.Len(t, staticFeeds, 1)
				staticFeed := staticFeeds[0].(map[string]any)
				assert.Equal(t, "X-Api-Key", staticFeed["auth-header-name"])
				assert.Equal(t, appconf.RedactedValue, staticFeed["auth-header-value"])

				rtFeeds, ok := dumped["gtfs-rt-feeds"].([]any)
				require.True(t, ok)
				require.Len(t, rtFeeds, 1)
				rtFeed := rtFeeds[0].(map[string]any)
				headers, ok := rtFeed["headers"].(map[string]any)
				require.True(t, ok, "realtime headers missing: %v", rtFeed)
				assert.Equal(t, appconf.RedactedValue, headers["X-Api-Key"])

				assert.NotContains(t, output, "static-secret")
				assert.NotContains(t, output, "rt-secret")
			},
		},
		{
			name: "auth env overwrites CLI auth header flags",
			args: "-env development -data-path :memory: -gtfs-static-auth-header-name Cli-Static -gtfs-static-auth-header-value cli-static-secret -realtime-auth-header-name Cli-RT -realtime-auth-header-value cli-rt-secret",
			env: map[string]string{
				"GTFS_STATIC_AUTH_NAME":    "Env-Static",
				"GTFS_STATIC_AUTH_VALUE":   "env-static-secret",
				"GTFS_REALTIME_AUTH_NAME":  "Env-RT",
				"GTFS_REALTIME_AUTH_VALUE": "env-rt-secret",
			},
			check: func(t *testing.T, exitCode int, output string) {
				assert.Equal(t, 0, exitCode, output)
				dumped := parseDumpConfigJSON(t, output)

				staticFeeds := dumped["gtfs-static-feeds"].([]any)
				staticFeed := staticFeeds[0].(map[string]any)
				assert.Equal(t, "Env-Static", staticFeed["auth-header-name"])
				assert.Equal(t, appconf.RedactedValue, staticFeed["auth-header-value"])

				rtFeeds := dumped["gtfs-rt-feeds"].([]any)
				rtFeed := rtFeeds[0].(map[string]any)
				headers := rtFeed["headers"].(map[string]any)
				assert.Equal(t, appconf.RedactedValue, headers["Env-RT"])
				_, hasCLIHeader := headers["Cli-RT"]
				assert.False(t, hasCLIHeader)

				assert.NotContains(t, output, "cli-static-secret")
				assert.NotContains(t, output, "cli-rt-secret")
				assert.NotContains(t, output, "env-static-secret")
				assert.NotContains(t, output, "env-rt-secret")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, output := runMainWithArgs(t, "-dump-config "+tc.args, tc.env)
			tc.check(t, exitCode, output)
		})
	}
}

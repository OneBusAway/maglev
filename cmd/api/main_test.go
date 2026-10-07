package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// runMainWithArgs runs main() in a subprocess and returns its exit code and output.
func runMainWithArgs(t *testing.T, args string) (int, string) {
	t.Helper()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), runMainEnvVar+"=1", runMainArgsEnvVar+"="+args)
	output, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(output)
	}
	require.NoError(t, err)
	return 0, string(output)
}

func TestMain_CommandLineFlagsPassValidation(t *testing.T) {
	tests := []struct {
		name         string
		env          string
		wantExitCode int
		wantOutput   string
	}{
		{"development uses default protected key", "development", 0, `"env": "development"`},
		{"test uses default protected key", "test", 0, `"env": "test"`},
		{"production requires a protected key", "production", 1, "protected-api-keys cannot be empty"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, output := runMainWithArgs(t, "-dump-config -env "+tc.env)

			assert.Equal(t, tc.wantExitCode, exitCode, output)
			assert.Contains(t, output, tc.wantOutput)
		})
	}
}

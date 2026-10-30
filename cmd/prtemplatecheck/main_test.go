package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestRun(t *testing.T) {
	filled := filledBody(readTemplate(t))
	unchecked := replaceOnce(t, filled, "- [x] This PR addresses one issue.", "- [ ] This PR addresses one issue.")

	tests := []struct {
		name         string
		args         func(t *testing.T) []string
		wantExitCode int
		wantStdout   []string
		rejectStdout []string
		wantStderr   bool
	}{
		{
			name:         "pass_exit_0",
			args:         func(t *testing.T) []string { return []string{"-template", templatePath, "-body", writeBody(t, filled)} },
			wantExitCode: 0,
			wantStdout:   []string{reportMarker, "follows the PR template"},
		},
		{
			name:         "fail_exit_1",
			args:         func(t *testing.T) []string { return []string{"-template", templatePath, "-body", writeBody(t, "")} },
			wantExitCode: 1,
			wantStdout:   []string{reportMarker, "doesn't use the PR template", "Keep this PR as Draft"},
		},
		{
			name: "mentions_author_on_failure",
			args: func(t *testing.T) []string {
				return []string{"-template", templatePath, "-body", writeBody(t, ""), "-author", "tejasva-vardhan"}
			},
			wantExitCode: 1,
			wantStdout:   []string{"@tejasva-vardhan, the PR description does not follow"},
		},
		{
			name: "no_mention_on_pass",
			args: func(t *testing.T) []string {
				return []string{"-template", templatePath, "-body", writeBody(t, filled), "-author", "tejasva-vardhan"}
			},
			wantExitCode: 0,
			rejectStdout: []string{"@tejasva-vardhan"},
		},
		{
			name: "invalid_author_not_mentioned",
			args: func(t *testing.T) []string {
				return []string{"-template", templatePath, "-body", writeBody(t, ""), "-author", "x --><b>y</b>"}
			},
			wantExitCode: 1,
			wantStdout:   []string{"The PR description does not follow"},
			rejectStdout: []string{"@x", "<b>"},
		},
		{
			name: "unchecked_box_hint",
			args: func(t *testing.T) []string {
				return []string{"-template", templatePath, "-body", writeBody(t, unchecked)}
			},
			wantExitCode: 1,
			wantStdout:   []string{"\"This PR addresses one issue.\" If this item doesn't apply to your PR, ask a maintainer."},
		},
		{
			name: "draft_flag",
			args: func(t *testing.T) []string {
				return []string{"-template", templatePath, "-body", writeBody(t, unchecked), "-draft"}
			},
			wantExitCode: 0,
		},
		{
			name: "empty_response_comparison_suggests_deletion",
			args: func(t *testing.T) []string {
				body := replaceOnce(t, filled, "## Response comparison (delete if no Java-served response changed)\n\nWritten by the author.", "## Response comparison (delete if no Java-served response changed)")
				for _, label := range []string{
					"Request URL(s)",
					"Java server, feeds and date checked",
					"Maglev before",
					"Maglev after",
					"Java",
					"If Maglev intentionally differs from Java",
				} {
					body = setField(t, body, label, "")
				}
				return []string{"-template", templatePath, "-body", writeBody(t, body)}
			},
			wantExitCode: 1,
			wantStdout: []string{
				"Section **Response comparison** is empty. Delete it if no Java-served response changed, or fill it in.",
			},
			rejectStdout: []string{
				"\"Request URL(s):\" is empty",
				"\"Java server, feeds and date checked:\" is empty",
				"\"Maglev before:\" is empty",
				"\"Maglev after:\" is empty",
				"\"Java:\" is empty",
			},
		},
		{
			name: "missing_template_file",
			args: func(t *testing.T) []string {
				return []string{"-template", filepath.Join(t.TempDir(), "missing.md"), "-body", writeBody(t, filled)}
			},
			wantExitCode: 2,
			wantStderr:   true,
		},
		{
			name:         "missing_flag",
			args:         func(t *testing.T) []string { return []string{"-template", templatePath} },
			wantExitCode: 2,
			wantStderr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			exitCode := run(tt.args(t), &stdout, &stderr)

			assert.Equal(t, tt.wantExitCode, exitCode, "stderr: %s", stderr.String())
			for _, want := range tt.wantStdout {
				assert.Contains(t, stdout.String(), want)
			}
			for _, reject := range tt.rejectStdout {
				assert.NotContains(t, stdout.String(), reject)
			}
			if tt.wantStderr {
				assert.NotEmpty(t, strings.TrimSpace(stderr.String()))
				assert.Empty(t, stdout.String())
			}
		})
	}
}

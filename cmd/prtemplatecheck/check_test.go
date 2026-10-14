package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const templatePath = "../../.github/pull_request_template.md"

func readTemplate(t *testing.T) string {
	t.Helper()
	template, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	return string(template)
}

// filledBody turns the real template into a description an author has fully
// filled in: an issue link, text under every heading, a value for every
// labeled field and every checklist box checked.
func filledBody(template string) string {
	fieldLine := regexp.MustCompile(`^- \*\*[^*]+:\*\*`)
	var lines []string
	for line := range strings.SplitSeq(template, "\n") {
		switch {
		case line == "Fixes #":
			line = "Fixes #12"
		case strings.HasPrefix(line, "## "):
			line += "\n\nWritten by the author."
		case fieldLine.MatchString(line):
			line += " filled in"
		case strings.HasPrefix(line, "- [ ] "):
			line = "- [x] " + strings.TrimPrefix(line, "- [ ] ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func replaceOnce(t *testing.T, body, old, replacement string) string {
	t.Helper()
	require.Equal(t, 1, strings.Count(body, old), "fixture must contain %q exactly once", old)
	return strings.Replace(body, old, replacement, 1)
}

// setField replaces everything after a labeled field's "**Label:**" prefix.
func setField(t *testing.T, body, label, value string) string {
	t.Helper()
	field := regexp.MustCompile(`(?m)^(- \*\*` + regexp.QuoteMeta(label) + `:\*\*).*$`)
	require.True(t, field.MatchString(body), "fixture must contain field %q", label)
	return field.ReplaceAllLiteralString(body, "- **"+label+":**"+value)
}

// removeSection drops a "## " heading that starts with headingPrefix and
// everything up to the next "## " heading.
func removeSection(t *testing.T, body, headingPrefix string) string {
	t.Helper()
	start := strings.Index(body, "\n## "+headingPrefix)
	require.NotEqual(t, -1, start, "fixture must contain section %q", headingPrefix)
	end := strings.Index(body[start+1:], "\n## ")
	if end == -1 {
		return body[:start]
	}
	return body[:start] + body[start+1+end:]
}

func TestCheckBody(t *testing.T) {
	template := readTemplate(t)
	rules, err := parseTemplate(template)
	require.NoError(t, err)
	filled := filledBody(template)

	tests := []struct {
		name         string
		body         func(t *testing.T) string
		isDraft      bool
		wantRules    []string
		wantMentions []string
	}{
		{
			name: "filled_ready",
			body: func(t *testing.T) string { return filled },
		},
		{
			name:    "filled_draft_unchecked",
			body:    func(t *testing.T) string { return strings.ReplaceAll(filled, "- [x] ", "- [ ] ") },
			isDraft: true,
		},
		{
			name: "ready_one_unchecked",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "- [x] This PR addresses one issue.", "- [ ] This PR addresses one issue.")
			},
			wantRules:    []string{ruleChecklist},
			wantMentions: []string{"is not checked", "This PR addresses one issue."},
		},
		{
			name: "checklist_item_deleted",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "- [x] I read every line of this diff and can explain it if asked.\n", "")
			},
			wantRules:    []string{ruleChecklist},
			wantMentions: []string{"missing or its text was changed", "I read every line of this diff"},
		},
		{
			name: "checklist_item_reworded",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "- [x] This PR addresses one issue.", "- [x] This PR mostly addresses one issue.")
			},
			wantRules:    []string{ruleChecklist},
			wantMentions: []string{"missing or its text was changed", "This PR addresses one issue."},
		},
		{
			name: "uppercase_and_star",
			body: func(t *testing.T) string { return strings.ReplaceAll(filled, "- [x] ", "* [X] ") },
		},
		{
			name:      "empty_body",
			body:      func(t *testing.T) string { return "" },
			wantRules: []string{ruleTemplate, ruleIssueLink},
		},
		{
			name:      "template_deleted",
			body:      func(t *testing.T) string { return "Fixed the arrivals bug.\n\nFixes #12" },
			wantRules: []string{ruleTemplate},
		},
		{
			name:      "no_issue_number",
			body:      func(t *testing.T) string { return replaceOnce(t, filled, "Fixes #12", "Fixes #") },
			wantRules: []string{ruleIssueLink},
		},
		{
			name: "part_of_and_url",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "Fixes #12", "Part of https://github.com/OneBusAway/maglev/issues/7")
			},
		},
		{
			name:      "issue_link_in_comment",
			body:      func(t *testing.T) string { return replaceOnce(t, filled, "Fixes #12", "<!-- Fixes #12 -->") },
			wantRules: []string{ruleIssueLink},
		},
		{
			name: "section_only_comment",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "## Root cause\n\nWritten by the author.", "## Root cause\n\n<!-- In your own words. -->")
			},
			wantRules:    []string{ruleSection},
			wantMentions: []string{"Root cause"},
		},
		{
			name:         "section_heading_deleted",
			body:         func(t *testing.T) string { return replaceOnce(t, filled, "## What it does\n", "") },
			wantRules:    []string{ruleSection},
			wantMentions: []string{"What it does"},
		},
		{
			name: "optional_sections_deleted",
			body: func(t *testing.T) string {
				return removeSection(t, removeSection(t, filled, "Response comparison"), "Notes for reviewers")
			},
		},
		{
			name: "response_fields_empty",
			body: func(t *testing.T) string {
				body := setField(t, filled, "Maglev after", "")
				return setField(t, body, "If Maglev intentionally differs from Java", " ")
			},
			wantRules:    []string{ruleField},
			wantMentions: []string{"Maglev after"},
		},
		{
			name: "testing_field_na",
			body: func(t *testing.T) string {
				return setField(t, filled, "Test failing on `main` before this change", " n/a")
			},
		},
		{
			name: "field_value_next_line_fence",
			body: func(t *testing.T) string {
				return setField(t, filled, "Manual check", "\n\n  ```\n  curl localhost:4000/api/where/current-time.json\n  ```")
			},
		},
		{
			name: "field_value_in_details",
			body: func(t *testing.T) string {
				return setField(t, filled, "Maglev before", "\n<details>\n\n```json\n{\"code\": 200}\n```\n</details>")
			},
		},
		{
			name: "checkbox_in_comment",
			body: func(t *testing.T) string {
				return replaceOnce(t, filled, "- [x] This PR addresses one issue.",
					"- [ ] This PR addresses one issue.\n<!--\n- [x] This PR addresses one issue.\n-->")
			},
			wantRules:    []string{ruleChecklist},
			wantMentions: []string{"This PR addresses one issue."},
		},
		{
			name: "heading_in_code_fence",
			body: func(t *testing.T) string {
				body := replaceOnce(t, filled, "## Root cause\n\nWritten by the author.", "## Root cause\n")
				return replaceOnce(t, body, "## Testing\n\nWritten by the author.",
					"## Testing\n\nWritten by the author.\n\n```\n## Root cause\nfrom the log\n- [x] This PR addresses one issue.\n```")
			},
			wantRules:    []string{ruleSection},
			wantMentions: []string{"Root cause"},
		},
		{
			name: "crlf_body",
			body: func(t *testing.T) string { return strings.ReplaceAll(filled, "\n", "\r\n") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := checkBody(rules, tt.body(t), tt.isDraft)

			gotRules := make([]string, 0, len(problems))
			messages := make([]string, 0, len(problems))
			for _, p := range problems {
				gotRules = append(gotRules, p.Rule)
				messages = append(messages, p.Message)
			}
			if len(tt.wantRules) == 0 {
				assert.Empty(t, problems)
				return
			}
			assert.Equal(t, tt.wantRules, gotRules, "messages: %q", messages)
			for _, mention := range tt.wantMentions {
				assert.Contains(t, strings.Join(messages, "\n"), mention)
			}
		})
	}
}

func TestParseTemplateRejectsBrokenTemplates(t *testing.T) {
	template := readTemplate(t)

	tests := []struct {
		name     string
		template string
	}{
		{
			name:     "template_without_checklist",
			template: template[:strings.Index(template, "\n## Checklist")],
		},
		{
			name:     "template_without_marker",
			template: strings.Replace(template, "<!-- maglev-pr-template:v1 -->", "", 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEqual(t, template, tt.template, "fixture must change the template")
			_, err := parseTemplate(tt.template)
			assert.Error(t, err)
		})
	}
}

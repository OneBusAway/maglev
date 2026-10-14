package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Rule names reported on each problem, in the order checkBody reports them.
const (
	ruleTemplate  = "template"
	ruleIssueLink = "issue-link"
	ruleSection   = "section"
	ruleField     = "field"
	ruleChecklist = "checklist"
)

const (
	preambleKey  = ""
	checklistKey = "checklist"
	// A labeled field whose label starts with this is only needed in some
	// PRs, e.g. "If Maglev intentionally differs from Java:".
	conditionalFieldPrefix = "If "
)

var (
	markerPattern  = regexp.MustCompile(`<!--\s*([\w-]+):v\d+\s*-->`)
	commentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
	// GitHub's closing keywords, plus "Part of", followed by an issue
	// reference in any form GitHub links.
	issueLinkPattern = regexp.MustCompile(`(?i)\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|part of):?\s+` +
		`(?:#\d+|[\w.-]+/[\w.-]+#\d+|https://github\.com/[\w.-]+/[\w.-]+/issues/\d+)\b`)
	headingPattern       = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	fieldPattern         = regexp.MustCompile(`^\s*[-*]\s+\*\*([^*]+):\*\*(.*)$`)
	checkboxPattern      = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.+?)\s*$`)
	headingSuffixPattern = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
)

// problem is one way a PR description falls short of the template.
type problem struct {
	Rule    string
	Message string
}

// templateRules is what parseTemplate learns from the PR template.
type templateRules struct {
	marker    *regexp.Regexp
	sections  []templateSection
	checklist []string
}

type templateSection struct {
	key      string
	title    string
	required bool
	fields   []templateField
}

type templateField struct {
	label       string
	conditional bool
}

// document is a Markdown description split into "## " sections, with
// comments removed and fenced code blocks kept as plain text.
type document struct {
	text     string
	sections map[string]*section
	order    []string
}

type section struct {
	heading    string
	hasText    bool
	fields     []field
	checkboxes []checkbox
	// fieldOpen is set while lines may still belong to the last field.
	fieldOpen bool
}

type field struct {
	label    string
	hasValue bool
}

type checkbox struct {
	text    string
	checked bool
}

// parseTemplate derives the rules from the PR template itself, so editing the
// template changes what is checked.
func parseTemplate(template string) (templateRules, error) {
	marker := markerPattern.FindStringSubmatch(template)
	if marker == nil {
		return templateRules{}, errors.New("template has no marker line like <!-- name:v1 -->")
	}

	doc := parseDocument(template)
	rules := templateRules{
		marker: regexp.MustCompile(`<!--\s*` + regexp.QuoteMeta(marker[1]) + `:v\d+\s*-->`),
	}
	for _, key := range doc.order {
		if key == preambleKey {
			continue
		}
		rules.sections = append(rules.sections, newTemplateSection(key, doc.sections[key]))
	}

	if checklist, ok := doc.sections[checklistKey]; ok {
		for _, box := range checklist.checkboxes {
			rules.checklist = append(rules.checklist, box.text)
		}
	}
	if len(rules.checklist) == 0 {
		return templateRules{}, errors.New("template has no checkboxes under ## Checklist")
	}
	return rules, nil
}

func newTemplateSection(key string, s *section) templateSection {
	heading := strings.ToLower(s.heading)
	isOptional := strings.Contains(heading, "(optional)") || strings.Contains(heading, "(delete if")

	ts := templateSection{key: key, title: sectionTitle(s.heading), required: !isOptional}
	for _, f := range s.fields {
		ts.fields = append(ts.fields, templateField{
			label:       f.label,
			conditional: strings.HasPrefix(f.label, conditionalFieldPrefix),
		})
	}
	return ts
}

// checkBody lists every way body falls short of rules. The checklist is only
// enforced once the PR is ready for review.
func checkBody(rules templateRules, body string, isDraft bool) []problem {
	doc := parseDocument(body)
	if !rules.marker.MatchString(body) {
		// Without the template every section would be reported missing, so
		// report the one fix that covers them all.
		return append([]problem{{
			Rule:    ruleTemplate,
			Message: "The description doesn't use the PR template. Copy `.github/pull_request_template.md` into the description and fill it in.",
		}}, issueLinkProblems(doc)...)
	}

	problems := issueLinkProblems(doc)
	for _, ts := range rules.sections {
		problems = append(problems, sectionProblems(ts, doc)...)
	}
	if !isDraft {
		problems = append(problems, checklistProblems(rules.checklist, doc)...)
	}
	return problems
}

func issueLinkProblems(doc document) []problem {
	if issueLinkPattern.MatchString(doc.text) {
		return nil
	}
	return []problem{{
		Rule:    ruleIssueLink,
		Message: "No linked issue. Add `Fixes #N` (or `Part of #N`) for a triaged issue.",
	}}
}

func sectionProblems(ts templateSection, doc document) []problem {
	s, ok := doc.sections[ts.key]
	switch {
	case !ok && ts.required:
		return []problem{{Rule: ruleSection, Message: fmt.Sprintf("Section **%s** is missing.", ts.title)}}
	case !ok:
		return nil
	case ts.required && !s.hasText:
		return []problem{{Rule: ruleSection, Message: fmt.Sprintf("Section **%s** is empty.", ts.title)}}
	}

	var problems []problem
	for _, tf := range ts.fields {
		if tf.conditional || s.fieldHasValue(tf.label) {
			continue
		}
		problems = append(problems, problem{
			Rule:    ruleField,
			Message: fmt.Sprintf("**%s**: \"%s:\" is empty.", ts.title, tf.label),
		})
	}
	return problems
}

func checklistProblems(items []string, doc document) []problem {
	checklist, ok := doc.sections[checklistKey]
	if !ok {
		// sectionProblems already reports the missing Checklist section.
		return nil
	}

	var problems []problem
	for _, item := range items {
		found, checked := checklist.checkboxState(item)
		switch {
		case checked:
			continue
		case found:
			problems = append(problems, problem{
				Rule:    ruleChecklist,
				Message: fmt.Sprintf("Checklist item is not checked: \"%s\"", item),
			})
		default:
			problems = append(problems, problem{
				Rule:    ruleChecklist,
				Message: fmt.Sprintf("Checklist item is missing or its text was changed. Copy this exact line from the template: \"%s\"", item),
			})
		}
	}
	return problems
}

func parseDocument(text string) document {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = commentPattern.ReplaceAllString(text, "")

	doc := document{text: text, sections: map[string]*section{}}
	current := doc.addSection(preambleKey, "")
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if heading := headingPattern.FindStringSubmatch(line); heading != nil && !inFence {
			current = doc.addSection(sectionKey(heading[1]), heading[1])
			continue
		}
		current.addLine(line, inFence)
	}
	return doc
}

// addSection returns the section for key, creating it on first use so a
// repeated heading keeps adding to the same section.
func (d *document) addSection(key, heading string) *section {
	if s, ok := d.sections[key]; ok {
		return s
	}
	s := &section{heading: heading}
	d.sections[key] = s
	d.order = append(d.order, key)
	return s
}

func (s *section) addLine(line string, inFence bool) {
	hasText := strings.TrimSpace(line) != ""
	if hasText {
		s.hasText = true
	}
	if !inFence {
		if f := fieldPattern.FindStringSubmatch(line); f != nil {
			s.fields = append(s.fields, field{label: strings.TrimSpace(f[1]), hasValue: strings.TrimSpace(f[2]) != ""})
			s.fieldOpen = true
			return
		}
		if box := checkboxPattern.FindStringSubmatch(line); box != nil {
			s.checkboxes = append(s.checkboxes, checkbox{text: collapseSpaces(box[2]), checked: box[1] != " "})
			s.fieldOpen = false
			return
		}
	}
	// A value can start on the lines after its label, such as a pasted code
	// block or a <details> block under "Manual check:".
	if s.fieldOpen && hasText {
		s.fields[len(s.fields)-1].hasValue = true
	}
}

func (s *section) fieldHasValue(label string) bool {
	for _, f := range s.fields {
		if f.label == label {
			return f.hasValue
		}
	}
	return false
}

// checkboxState reports whether the section has a box with exactly this
// text, and whether any such box is checked.
func (s *section) checkboxState(item string) (found, checked bool) {
	for _, box := range s.checkboxes {
		if box.text == item {
			found = true
			checked = checked || box.checked
		}
	}
	return found, checked
}

// sectionKey matches headings case-insensitively and ignores a trailing
// "(optional)"-style note, which authors sometimes delete.
func sectionKey(heading string) string {
	return strings.ToLower(sectionTitle(heading))
}

func sectionTitle(heading string) string {
	return strings.TrimSpace(headingSuffixPattern.ReplaceAllString(heading, ""))
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

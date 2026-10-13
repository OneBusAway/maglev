// Command prtemplatecheck checks a pull request description against
// .github/pull_request_template.md. It prints a Markdown report for the PR
// comment and exits 0 when the description passes, 1 when it doesn't and 2
// on a usage or read error.
//
//	go run ./cmd/prtemplatecheck -template .github/pull_request_template.md -body body.md [-draft]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// reportMarker tags the bot's PR comment so the workflow can find and update
// it instead of posting a new one.
const reportMarker = "<!-- maglev-pr-template-check -->"

const templateURL = "https://github.com/OneBusAway/maglev/blob/main/.github/pull_request_template.md"

const (
	exitPass   = 0
	exitFail   = 1
	exitErrors = 2
)

const checklistHint = " If this item doesn't apply to your PR, ask a maintainer."

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prtemplatecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	templatePath := flags.String("template", "", "path to the PR template")
	bodyPath := flags.String("body", "", "path to a file holding the PR description")
	isDraft := flags.Bool("draft", false, "the PR is a draft, so the checklist is not enforced")
	if err := flags.Parse(args); err != nil {
		return exitErrors
	}
	if *templatePath == "" || *bodyPath == "" {
		fmt.Fprintln(stderr, "both -template and -body are required")
		return exitErrors
	}

	rules, body, err := loadInputs(*templatePath, *bodyPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitErrors
	}

	problems := checkBody(rules, body, *isDraft)
	fmt.Fprint(stdout, renderReport(problems))
	if len(problems) > 0 {
		return exitFail
	}
	return exitPass
}

func loadInputs(templatePath, bodyPath string) (templateRules, string, error) {
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return templateRules{}, "", err
	}
	rules, err := parseTemplate(string(template))
	if err != nil {
		return templateRules{}, "", fmt.Errorf("%s: %w", templatePath, err)
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return templateRules{}, "", err
	}
	return rules, string(body), nil
}

func renderReport(problems []problem) string {
	var report strings.Builder
	report.WriteString(reportMarker + "\n")
	if len(problems) == 0 {
		report.WriteString("The description follows the PR template.\n")
		return report.String()
	}

	fmt.Fprintf(&report, "**The PR description does not follow the [PR template](%s).**\n\n", templateURL)
	for _, p := range problems {
		message := p.Message
		if p.Rule == ruleChecklist {
			message += checklistHint
		}
		fmt.Fprintf(&report, "- %s\n", message)
	}
	report.WriteString("\nKeep this PR as Draft until these are fixed. This comment updates when the description changes.\n")
	return report.String()
}

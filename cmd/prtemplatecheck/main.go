// Command prtemplatecheck checks a pull request description against
// .github/pull_request_template.md. It prints a Markdown report for the PR
// comment and exits 0 when the description passes, 1 when it doesn't and 2
// on a usage or read error.
//
//	go run ./cmd/prtemplatecheck -template .github/pull_request_template.md -body body.md [-draft] [-author login]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
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

// githubLoginPattern is the shape of a GitHub username. Anything else is not
// mentioned, so the comment can't carry markup from the event payload.
var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prtemplatecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	templatePath := flags.String("template", "", "path to the PR template")
	bodyPath := flags.String("body", "", "path to a file holding the PR description")
	isDraft := flags.Bool("draft", false, "the PR is a draft, so the checklist is not enforced")
	author := flags.String("author", "", "GitHub login of the PR author, mentioned when the check fails")
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
	fmt.Fprint(stdout, renderReport(problems, *author))
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

// renderReport builds the bot comment. A failing report mentions the author
// so the thread shows who needs to act.
func renderReport(problems []problem, author string) string {
	var report strings.Builder
	report.WriteString(reportMarker + "\n")
	if len(problems) == 0 {
		report.WriteString("The description follows the PR template.\n")
		return report.String()
	}

	intro := "The PR description does not follow"
	if githubLoginPattern.MatchString(author) {
		intro = fmt.Sprintf("@%s, the PR description does not follow", author)
	}
	fmt.Fprintf(&report, "%s the [PR template](%s):\n\n", intro, templateURL)
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

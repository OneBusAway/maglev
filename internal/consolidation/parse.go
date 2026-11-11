// Package consolidation reads the stop consolidation files used by Java
// OneBusAway. Each line of a file lists one canonical stop followed by the
// stops absorbed into it, so riders see one stop where several feeds list
// the same curb.
//
// Parsing reproduces Java's EntityReplacementStrategyFactory exactly,
// quirks included, so a file behaves the same on both servers.
package consolidation

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
)

// StopID is a stop ID in the "<agency>_<id>" form Java OneBusAway uses.
type StopID struct {
	AgencyID string
	ID       string
}

// String returns the ID in "<agency>_<id>" form.
func (s StopID) String() string {
	return s.AgencyID + "_" + s.ID
}

// Rule is one line of a consolidation file. A line with only a canonical
// stop has no Absorbed stops and changes nothing.
type Rule struct {
	Line      int
	Canonical StopID
	Absorbed  []StopID
}

// File is a parsed consolidation file, with rules in file order.
type File struct {
	Rules []Rule
}

// Replacements maps each absorbed stop to its canonical stop. When an
// absorbed stop appears on more than one line, the later line wins, as in
// Java.
func (f *File) Replacements() map[StopID]StopID {
	replacements := make(map[StopID]StopID)
	for _, rule := range f.Rules {
		for _, absorbed := range rule.Absorbed {
			replacements[absorbed] = rule.Canonical
		}
	}
	return replacements
}

// Parse reads a consolidation file. It fails on any token without an
// underscore, naming the line, because Java rejects the whole file then.
func Parse(r io.Reader) (*File, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading stop consolidation file: %w", err)
	}

	file := &File{}
	for index, line := range splitLines(string(b)) {
		lineNumber := index + 1
		tokens := tokenize(line)
		if len(tokens) == 0 {
			continue
		}
		rule, err := ruleFromTokens(lineNumber, tokens)
		if err != nil {
			return nil, err
		}
		file.Rules = append(file.Rules, rule)
	}
	return file, nil
}

func ruleFromTokens(lineNumber int, tokens []string) (Rule, error) {
	var ids []StopID
	for _, token := range tokens {
		id, ok := parseStopID(token)
		if !ok {
			return Rule{}, fmt.Errorf("stop consolidation file line %d: %q is not an <agency>_<id> stop ID", lineNumber, token)
		}
		ids = append(ids, id)
	}
	rule := Rule{Line: lineNumber, Canonical: ids[0]}
	if len(ids) > 1 {
		rule.Absorbed = ids[1:]
	}
	return rule, nil
}

// parseStopID splits on the first underscore, like AgencyAndIdLibrary.
func parseStopID(token string) (StopID, bool) {
	agencyID, id, found := strings.Cut(token, "_")
	return StopID{AgencyID: agencyID, ID: id}, found
}

// splitLines splits like Java's BufferedReader.readLine, which ends a line
// at "\n", "\r" or "\r\n".
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// tokenize returns a line's stop ID tokens, or none for a blank, comment or
// wiki-markup line. A line with a double quote is split on quotes alone and
// its pieces are not trimmed, so `1_a "2_b"` yields "1_a " with its space.
func tokenize(line string) []string {
	line = strings.TrimFunc(line, isJavaTrimmable)
	if isSkippedLine(line) {
		return nil
	}

	var pieces []string
	if strings.Contains(line, `"`) {
		pieces = strings.Split(line, `"`)
	} else {
		pieces = strings.FieldsFunc(line, isJavaRegexWhitespace)
	}

	return slices.DeleteFunc(pieces, isJavaBlank)
}

func isSkippedLine(line string) bool {
	return line == "" ||
		strings.HasPrefix(line, "#") ||
		strings.HasPrefix(line, "{{{") ||
		strings.HasPrefix(line, "}}}")
}

// isJavaTrimmable matches what String.trim removes: every code point up to
// and including the space character.
func isJavaTrimmable(r rune) bool {
	return r <= ' '
}

// isJavaRegexWhitespace matches the \s character class in Java regular
// expressions, which is ASCII only.
func isJavaRegexWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// isJavaBlank reports what StringUtils.isBlank does: whether every code
// point is whitespace by Character.isWhitespace.
func isJavaBlank(s string) bool {
	return strings.TrimFunc(s, isJavaWhitespace) == ""
}

// isJavaWhitespace matches Character.isWhitespace: Unicode space, line and
// paragraph separators other than the no-break spaces, plus a fixed set of
// ASCII control characters.
func isJavaWhitespace(r rune) bool {
	switch r {
	case '\u00A0', '\u2007', '\u202F':
		return false
	case '\t', '\n', '\v', '\f', '\r', '\u001C', '\u001D', '\u001E', '\u001F':
		return true
	}
	return unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp)
}

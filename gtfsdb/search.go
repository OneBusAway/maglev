package gtfsdb

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
)

// SearchOperator specifies how terms in a search query are combined.
type SearchOperator int

const (
	// SearchOperatorAnd requires all terms to match.
	SearchOperatorAnd SearchOperator = iota
	// SearchOperatorOr requires at least one term to match.
	SearchOperatorOr
)

// SearchRequest defines structured parameters for full-text search.
type SearchRequest struct {
	// Query is the raw search text provided by the caller.
	Query string
	// Operator specifies whether terms are combined with AND or OR.
	Operator SearchOperator
	// PrefixMatch specifies whether terms match as prefixes (e.g. "term"* in FTS5).
	PrefixMatch bool
	// Limit is the maximum number of results to return.
	Limit int64
}

// fts5SpecialCharsRegex matches special characters in SQLite FTS5 query syntax.
var fts5SpecialCharsRegex = regexp.MustCompile(`[*"():^$@#~<>{}[\]\\|&!]`)

// sanitizeSearchInput replaces FTS5 special characters with spaces.
func sanitizeSearchInput(input string) string {
	sanitized := fts5SpecialCharsRegex.ReplaceAllString(input, " ")
	sanitized = strings.TrimSpace(sanitized)
	return strings.Join(strings.Fields(sanitized), " ")
}

// containsLetterOrDigit reports whether s has at least one letter or digit.
// FTS5 tokenizes punctuation-only terms (e.g. "%") to nothing, which makes
// MATCH raise a syntax error at query time.
func containsLetterOrDigit(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	})
}

// extractSearchTerms splits sanitized stop search input into searchable terms.
func extractSearchTerms(sanitized string) []string {
	rawTerms := strings.Fields(sanitized)
	terms := make([]string, 0, len(rawTerms))
	for _, term := range rawTerms {
		if containsLetterOrDigit(term) {
			terms = append(terms, term)
		}
	}
	return terms
}

// tokenizeRouteQuery normalizes route search input into terms. Route search
// preserves punctuation (e.g. "route!", "north/south", "the \"quick\" route")
// rather than stripping it, lowercases terms, and drops punctuation-only terms.
func tokenizeRouteQuery(input string) []string {
	rawTerms := strings.Fields(strings.ToLower(input))
	terms := make([]string, 0, len(rawTerms))
	for _, term := range rawTerms {
		trimmed := strings.TrimSpace(term)
		if trimmed != "" && containsLetterOrDigit(trimmed) {
			terms = append(terms, trimmed)
		}
	}
	return terms
}

// buildFTS5Query constructs an engine-specific SQLite FTS5 query string from terms.
func buildFTS5Query(terms []string, op SearchOperator, prefixMatch bool) string {
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" || !containsLetterOrDigit(trimmed) {
			continue
		}
		escaped := strings.ReplaceAll(trimmed, `"`, `""`)
		if prefixMatch {
			quoted = append(quoted, `"`+escaped+`"*`)
		} else {
			quoted = append(quoted, `"`+escaped+`"`)
		}
	}
	if len(quoted) == 0 {
		return ""
	}

	joinOp := " AND "
	if op == SearchOperatorOr {
		joinOp = " OR "
	}
	return strings.Join(quoted, joinOp)
}

// isFTSSyntaxError reports whether err represents an FTS query syntax error.
func isFTSSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "fts5") || strings.Contains(errStr, "syntax")
}

// SearchStops performs a full-text search for stops using structured parameters.
// If a prefix-matching wildcard query fails due to an FTS5 syntax error, it
// automatically falls back to retrying without wildcards.
func (q *Queries) SearchStops(ctx context.Context, req SearchRequest) ([]SearchStopsByNameRow, error) {
	sanitized := sanitizeSearchInput(req.Query)
	terms := extractSearchTerms(sanitized)
	if len(terms) == 0 {
		return []SearchStopsByNameRow{}, nil
	}

	ftsQuery := buildFTS5Query(terms, req.Operator, req.PrefixMatch)
	if ftsQuery == "" {
		return []SearchStopsByNameRow{}, nil
	}

	rows, err := q.SearchStopsByName(ctx, SearchStopsByNameParams{
		SearchQuery: ftsQuery,
		Limit:       req.Limit,
	})
	if err != nil && req.PrefixMatch && isFTSSyntaxError(err) {
		fallbackQuery := buildFTS5Query(terms, req.Operator, false)
		if fallbackQuery != "" {
			slog.WarnContext(ctx, "FTS5 wildcard query failed, retrying without wildcard",
				"original_error", err,
				"fts_query", ftsQuery,
			)
			rows, err = q.SearchStopsByName(ctx, SearchStopsByNameParams{
				SearchQuery: fallbackQuery,
				Limit:       req.Limit,
			})
			if err != nil {
				return nil, fmt.Errorf("SearchStops failed for query %q: %w", fallbackQuery, err)
			}
			return rows, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("SearchStops failed for query %q: %w", ftsQuery, err)
	}
	return rows, nil
}

// SearchRoutes performs a full-text search for routes using structured parameters.
func (q *Queries) SearchRoutes(ctx context.Context, req SearchRequest) ([]Route, error) {
	terms := tokenizeRouteQuery(req.Query)
	if len(terms) == 0 {
		return []Route{}, nil
	}

	ftsQuery := buildFTS5Query(terms, req.Operator, req.PrefixMatch)
	if ftsQuery == "" {
		return []Route{}, nil
	}

	routes, err := q.SearchRoutesByFullText(ctx, SearchRoutesByFullTextParams{
		Query: ftsQuery,
		Limit: req.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("SearchRoutes failed for query %q: %w", ftsQuery, err)
	}
	return routes, nil
}

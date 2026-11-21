package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeSearchInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"whitespace only", "     ", ""},
		{"all special characters", `"*()`, ""},
		// Operator words are kept verbatim - the caller quotes every term, so FTS5 reads
		// them as literal tokens. Stop names such as "Park and Ride" depend on this.
		{"operator word AND preserved", "test AND foo", "test AND foo"},
		{"operator word And preserved", "test And foo", "test And foo"},
		{"operator word aNd preserved", "test aNd foo", "test aNd foo"},
		{"consecutive operator words preserved", "foo AND AND bar", "foo AND AND bar"},
		{"operator word at beginning preserved", "AND test", "AND test"},
		{"operator word at end preserved", "test OR", "test OR"},
		{"unicode input", "中央駅 テスト", "中央駅 テスト"},
		{"colon character", "column:value", "column value"},
		{"caret character", "test^2", "test 2"},
		{"curly braces", "test{foo}bar", "test foo bar"},
		{"square brackets", "test[foo]bar", "test foo bar"},
		{"angle brackets", "test<foo>bar", "test foo bar"},
		{"tilde character", "test~2", "test 2"},
		{"pipe character", "test|foo", "test foo"},
		{"operator word NEAR preserved", "test NEAR foo", "test NEAR foo"},
		{"operator word near preserved", "test near foo", "test near foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := sanitizeSearchInput(tt.input)
			assert.Equal(t, tt.expected, out)
		})
	}
}

func TestBuildFTS5Query(t *testing.T) {
	tests := []struct {
		name        string
		terms       []string
		op          SearchOperator
		prefixMatch bool
		expected    string
	}{
		{
			name:        "SingleWord Prefix",
			terms:       []string{"airport"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"airport"*`,
		},
		{
			name:        "MultipleWords OR",
			terms:       []string{"redding", "express"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"redding"* OR "express"*`,
		},
		{
			name:        "MultipleWords AND",
			terms:       []string{"redding", "express"},
			op:          SearchOperatorAnd,
			prefixMatch: true,
			expected:    `"redding"* AND "express"*`,
		},
		{
			name:        "MultipleWords AND Exact Match",
			terms:       []string{"redding", "express"},
			op:          SearchOperatorAnd,
			prefixMatch: false,
			expected:    `"redding" AND "express"`,
		},
		{
			name:        "Empty terms",
			terms:       []string{},
			op:          SearchOperatorAnd,
			prefixMatch: true,
			expected:    "",
		},
		{
			name:        "Embedded quotes escaped",
			terms:       []string{"the", `"quick"`, "route"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"the"* OR """quick"""* OR "route"*`,
		},
		{
			name:        "SpecialCharactersBang",
			terms:       []string{"route!"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"route!"*`,
		},
		{
			name:        "SpecialCharactersAt",
			terms:       []string{"stop@here"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"stop@here"*`,
		},
		{
			name:        "SpecialCharactersSlash",
			terms:       []string{"north/south"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"north/south"*`,
		},
		{
			name:        "UnicodeAccented",
			terms:       []string{"café"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"café"*`,
		},
		{
			name:        "UnicodeCJK",
			terms:       []string{"日本", "電車"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"日本"* OR "電車"*`,
		},
		{
			name:        "AmpersandPreservedInRouteTerm",
			terms:       []string{"park&ride"},
			op:          SearchOperatorOr,
			prefixMatch: true,
			expected:    `"park&ride"*`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTS5Query(tt.terms, tt.op, tt.prefixMatch)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestTokenizeRouteQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "SingleWord",
			input:    "airport",
			expected: []string{"airport"},
		},
		{
			name:     "MultipleWords",
			input:    "redding express",
			expected: []string{"redding", "express"},
		},
		{
			name:     "InputIsLowered",
			input:    "Airport Express",
			expected: []string{"airport", "express"},
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: []string{},
		},
		{
			name:     "WhitespaceOnly",
			input:    "   \t  ",
			expected: []string{},
		},
		{
			name:     "SpecialCharactersBang",
			input:    "route!",
			expected: []string{"route!"},
		},
		{
			name:     "SpecialCharactersAt",
			input:    "stop@here",
			expected: []string{"stop@here"},
		},
		{
			name:     "SpecialCharactersSlash",
			input:    "north/south",
			expected: []string{"north/south"},
		},
		{
			name:     "UnicodeAccented",
			input:    "café",
			expected: []string{"café"},
		},
		{
			name:     "UnicodeCJK",
			input:    "日本 電車",
			expected: []string{"日本", "電車"},
		},
		{
			name:     "EmbeddedDoubleQuotes",
			input:    `the "quick" route`,
			expected: []string{"the", `"quick"`, "route"},
		},
		{
			name:     "ExtraWhitespace",
			input:    "  route   one  ",
			expected: []string{"route", "one"},
		},
		{
			name:     "PunctuationOnlyDropped",
			input:    "route % 5",
			expected: []string{"route", "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeRouteQuery(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

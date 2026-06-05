package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractChunk(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		start     int
		wantChunk string
		wantIsNum bool
		wantNext  int
	}{
		{"digits only", "123", 0, "123", true, 3},
		{"letters only", "abc", 0, "abc", false, 3},
		{"mixed starts with letter", "a123", 0, "a", false, 1},
		{"mixed starts with digit", "123a", 0, "123", true, 3},
		{"unicode letters", "世2", 0, "世", false, 3},
		{"unicode digits", "123世", 0, "123", true, 3},
		{"mixed long", "Route101", 0, "Route", false, 5},
		{"mixed long digit", "Route101", 5, "101", true, 8},
		{"out of bounds", "123", 5, "", false, 5},
		{"unicode full-width digits", "１２３", 0, "１２３", false, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk, isNum, next := extractChunk(tt.s, tt.start)
			assert.Equal(t, tt.wantChunk, chunk)
			assert.Equal(t, tt.wantIsNum, isNum)
			assert.Equal(t, tt.wantNext, next)
		})
	}
}

func TestCompareNumericChunks(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"equal", "123", "123", 0},
		{"less", "12", "123", -1},
		{"greater", "123", "12", 1},
		{"leading zero equal val, lex less", "002", "2", -1},
		{"leading zero equal val, lex greater", "2", "002", 1},
		{"leading zero equal", "002", "002", 0},
		{"overflow equal", "100000000000000000000", "100000000000000000000", 0},
		{"overflow less", "100000000000000000000", "200000000000000000000", -1},
		{"overflow greater", "200000000000000000000", "100000000000000000000", 1},
		{"overflow mixed leading zero", "00100000000000000000000", "100000000000000000000", -1}, // value is 10^20, lex <
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareNumericChunks(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNaturalCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"equal", "A", "A", 0},
		{"alpha less", "A", "B", -1},
		{"alpha greater", "B", "A", 1},
		{"numeric less", "2", "14", -1},
		{"numeric greater", "14", "2", 1},
		{"mixed less", "Route 2", "Route 14", -1},
		{"mixed greater", "Route 14", "Route 2", 1},
		{"prefix", "A", "AA", -1},
		{"prefix inverse", "AA", "A", 1},
		{"leading zeros", "001", "1", -1},
		{"large num less", "Route 100000000000000000000", "Route 200000000000000000000", -1},
		{"large num greater", "Route 200000000000000000000", "Route 100000000000000000000", 1},
		{"empty a", "", "A", -1},
		{"empty b", "A", "", 1},
		{"both empty", "", "", 0},
		{"unicode digits compare lexically", "１", "２", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NaturalCompare(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

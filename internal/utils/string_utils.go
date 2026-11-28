package utils

import (
	"cmp"
	"math/big"
	"strconv"
)

// isASCIIDigit checks if a rune is an ASCII digit (0-9)
func isASCIIDigit(r int32) bool {
	return r >= '0' && r <= '9'
}

// extractChunk grabs either a contiguous sequence of digits or non-digits.
func extractChunk(s string, start int) (string, bool, int) {
	if start >= len(s) {
		return "", false, start
	}

	var isDigit bool
	for idx, r := range s[start:] {
		if idx == 0 {
			isDigit = isASCIIDigit(r)
			continue
		}
		if isASCIIDigit(r) != isDigit {
			return s[start : start+idx], isDigit, start + idx
		}
	}
	return s[start:], isDigit, len(s)
}

// compareNumericChunks compares two string chunks as numbers.
// If they are numerically equal, it falls back to lexical comparison to handle leading zeros.
func compareNumericChunks(chunkA, chunkB string) int {
	numA, errA := strconv.ParseUint(chunkA, 10, 64)
	numB, errB := strconv.ParseUint(chunkB, 10, 64)

	if errA != nil || errB != nil {
		bigA, okA := new(big.Int).SetString(chunkA, 10)
		bigB, okB := new(big.Int).SetString(chunkB, 10)
		if okA && okB {
			if res := bigA.Cmp(bigB); res != 0 {
				return res
			}
			return cmp.Compare(chunkA, chunkB)
		}
	}

	if numA != numB {
		return cmp.Compare(numA, numB)
	}
	return cmp.Compare(chunkA, chunkB)
}

// NaturalCompare compares two strings using natural sort order (e.g., "2" < "14" < "101" < "B").
// Returns -1 if a < b, 0 if a == b, and 1 if a > b.
func NaturalCompare(a, b string) int {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		chunkA, isDigitA, nextI := extractChunk(a, i)
		chunkB, isDigitB, nextJ := extractChunk(b, j)

		var res int
		if isDigitA && isDigitB {
			res = compareNumericChunks(chunkA, chunkB)
		} else {
			res = cmp.Compare(chunkA, chunkB)
		}

		if res != 0 {
			return res
		}

		i, j = nextI, nextJ
	}

	return cmp.Compare(len(a), len(b))
}

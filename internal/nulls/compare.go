package nulls

import "cmp"

// ComparePtr orders a nil pointer before any value and otherwise compares the
// pointed-to values with cmp.Compare. Use it to sort by optional fields.
func ComparePtr[T cmp.Ordered](a, b *T) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return cmp.Compare(*a, *b)
	}
}

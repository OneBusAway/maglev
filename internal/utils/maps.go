package utils

// MapValues returns the values of a map as a slice.
// The order of the returned values is non-deterministic.
func MapValues[K comparable, V any](m map[K]V) []V {
	result := make([]V, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// MapValuesAs returns the values of a map as a []any slice.
// The order of the returned values is non-deterministic.
func MapValuesAs[K comparable, V any](m map[K]V) []any {
	result := make([]any, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// MapValuesFiltered returns the values of a map as a []any slice,
// including only values for which the predicate returns true.
// The order of the returned values is non-deterministic.
func MapValuesFiltered[K comparable, V any](m map[K]V, predicate func(V) bool) []any {
	result := make([]any, 0, len(m))
	for _, v := range m {
		if predicate(v) {
			result = append(result, v)
		}
	}
	return result
}

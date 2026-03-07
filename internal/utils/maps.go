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

// MapValuesAsInterface returns the values of a map as an []interface{} slice.
// The order of the returned values is non-deterministic.
func MapValuesAsInterface[K comparable, V any](m map[K]V) []interface{} {
	result := make([]interface{}, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// MapValuesFiltered returns the values of a map as an []interface{} slice.
// The order of the returned values is non-deterministic.
func MapValuesFiltered[K comparable, V any](m map[K]V, predicate func(V) bool) []interface{} {
	result := make([]interface{}, 0, len(m))
	for _, v := range m {
		if predicate(v) {
			result = append(result, v)
		}
	}
	return result
}

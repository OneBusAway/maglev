// test_helper.go contains shared utilities for extracting
// IDs from JSON response structures in integration tests.
package restapi

type testingFatalf interface {
	Fatalf(format string, args ...any)
}

// collectAllNestedIdsFromObjects extracts string IDs from a nested array field
// across all objects in the list. For example, extracting all routeIds from
// a list of stop objects where each stop has a routeIds array.
func collectAllNestedIdsFromObjects(t testingFatalf, list []interface{}, key string) (ids []string) {
	for i, item := range list {
		object, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("item %d is not a map[string]interface{}", i)
		}
		value, ok := object[key]
		if !ok {
			t.Fatalf("item %d missing key %q", i, key)
		}
		objectList, ok := value.([]interface{})
		if !ok {
			t.Fatalf("item %d key %q is not a []interface{}: %T", i, key, value)
		}
		for j, nestedItem := range objectList {
			id, ok := nestedItem.(string)
			if !ok {
				t.Fatalf("item %d key %q index %d is not a string: %T", i, key, j, nestedItem)
			}
			ids = append(ids, id)
		}
	}
	return ids
}

// collectAllIdsFromObjects extracts string IDs from all objects in this list.
// For example, extracting all agency IDs from a list of agency objects.
func collectAllIdsFromObjects(t testingFatalf, list []interface{}, key string) (ids []string) {
	for i, item := range list {
		object, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("item %d is not a map[string]interface{}", i)
		}
		value, ok := object[key]
		if !ok {
			t.Fatalf("item %d missing key %q", i, key)
		}
		id, ok := value.(string)
		if !ok {
			t.Fatalf("item %d key %q is not a string: %T", i, key, value)
		}
		ids = append(ids, id)
	}
	return ids
}

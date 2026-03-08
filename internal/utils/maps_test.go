package utils

import (
	"sort"
	"testing"
)

func TestMapValues(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		var m map[string]int
		result := MapValues(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		m := map[string]int{}
		result := MapValues(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("single element", func(t *testing.T) {
		m := map[string]int{"a": 1}
		result := MapValues(m)
		if len(result) != 1 || result[0] != 1 {
			t.Errorf("expected [1], got %v", result)
		}
	})

	t.Run("multiple elements", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 2, "c": 3}
		result := MapValues(m)
		if len(result) != 3 {
			t.Errorf("expected 3 elements, got %d", len(result))
		}
		sort.Ints(result)
		expected := []int{1, 2, 3}
		for i, v := range result {
			if v != expected[i] {
				t.Errorf("index %d: expected %d, got %d", i, expected[i], v)
			}
		}
	})
}

func TestMapValuesAs(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		var m map[string]int
		result := MapValuesAs(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		m := map[string]int{}
		result := MapValuesAs(m)
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("returns all values as any", func(t *testing.T) {
		m := map[string]string{"a": "hello", "b": "world"}
		result := MapValuesAs(m)
		if len(result) != 2 {
			t.Errorf("expected 2 elements, got %d", len(result))
		}
		// Verify all values are present
		found := map[string]bool{}
		for _, v := range result {
			found[v.(string)] = true
		}
		if !found["hello"] || !found["world"] {
			t.Errorf("expected hello and world, got %v", result)
		}
	})
}

func TestMapValuesFiltered(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		var m map[string]int
		result := MapValuesFiltered(m, func(v int) bool { return v > 0 })
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		m := map[string]int{}
		result := MapValuesFiltered(m, func(v int) bool { return v > 0 })
		if result == nil {
			t.Errorf("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("filters values", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 0, "c": 3}
		result := MapValuesFiltered(m, func(v int) bool { return v > 0 })
		if len(result) != 2 {
			t.Errorf("expected 2 elements, got %d", len(result))
		}
	})

	t.Run("no values pass filter", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 2}
		result := MapValuesFiltered(m, func(v int) bool { return v > 10 })
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("all values pass filter", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 2}
		result := MapValuesFiltered(m, func(v int) bool { return v > 0 })
		if len(result) != 2 {
			t.Errorf("expected 2 elements, got %d", len(result))
		}
	})
}

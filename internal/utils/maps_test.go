package utils

import (
	"slices"
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
		slices.Sort(result)
		expected := []int{1, 2, 3}
		for i, v := range result {
			if v != expected[i] {
				t.Errorf("index %d: expected %d, got %d", i, expected[i], v)
			}
		}
	})
}

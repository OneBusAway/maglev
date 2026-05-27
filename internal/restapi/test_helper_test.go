package restapi

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockTestingFatalf struct {
	failed bool
	err    string
}

func (m *mockTestingFatalf) Fatalf(format string, args ...any) {
	m.failed = true
	m.err = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestCollectAllNestedIdsFromObjects(t *testing.T) {
	data := []any{
		map[string]any{"routes": []any{"234", "235"}},
		map[string]any{"routes": []any{"345"}},
	}
	expected := []string{"234", "235", "345"}
	actual := collectAllNestedIdsFromObjects(t, data, "routes")

	assert.Equal(t, expected, actual)
}

func TestCollectAllNestedIdsFromObjectsFailures(t *testing.T) {
	tests := []struct {
		name          string
		data          []any
		expectedError string
	}{
		{
			name: "Invalid object type in the array",
			data: []any{
				map[int]any{1: "234"},
			},
			expectedError: "item 0 is not a map[string]interface{}",
		},
		{
			name: "Missing key from the object",
			data: []any{
				map[string]any{"id": "234"},
			},
			expectedError: "item 0 missing key \"routes\"",
		},
		{
			name: "Invalid nested object",
			data: []any{
				map[string]any{"routes": "234"},
			},
			expectedError: "item 0 key \"routes\" is not a []interface{}: string",
		},
		{
			name: "Invalid nested array type",
			data: []any{
				map[string]any{"routes": []any{234}},
			},
			expectedError: "item 0 key \"routes\" index 0 is not a string: int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockFatalf := &mockTestingFatalf{}

			var running sync.WaitGroup
			running.Add(1)
			go func() {
				defer running.Done()
				collectAllNestedIdsFromObjects(mockFatalf, tt.data, "routes")
			}()
			running.Wait()

			assert.True(t, mockFatalf.failed)
			assert.Equal(t, tt.expectedError, mockFatalf.err)
		})
	}
}

func TestCollectAllIdsFromObjects(t *testing.T) {
	data := []any{
		map[string]any{"id": "234"},
		map[string]any{"id": "345"},
	}
	expected := []string{"234", "345"}
	actual := collectAllIdsFromObjects(t, data, "id")

	assert.Equal(t, expected, actual)
}

func TestCollectAllIdsFromObjectsFailures(t *testing.T) {
	tests := []struct {
		name          string
		data          []any
		expectedError string
	}{
		{
			name: "Invalid object type in the array",
			data: []any{
				map[int]any{1: "234"},
			},
			expectedError: "item 0 is not a map[string]interface{}",
		},
		{
			name: "Missing key from the object",
			data: []any{
				map[string]any{"name": "234"},
			},
			expectedError: "item 0 missing key \"id\"",
		},
		{
			name: "Invalid nested object",
			data: []any{
				map[string]any{"id": 234},
			},
			expectedError: "item 0 key \"id\" is not a string: int",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockFatalf := &mockTestingFatalf{}

			var running sync.WaitGroup
			running.Add(1)
			go func() {
				defer running.Done()
				collectAllIdsFromObjects(mockFatalf, tt.data, "id")
			}()
			running.Wait()

			assert.True(t, mockFatalf.failed)
			assert.Equal(t, tt.expectedError, mockFatalf.err)
		})
	}
}

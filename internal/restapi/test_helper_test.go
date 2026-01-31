package restapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectAllNestedIdsFromObjects(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"routes":
			[]interface{}{"234", "235"},
		},
		map[string]interface{}{"routes":
			[]interface{}{"345"},
		},
	}
	expected := []string{"234", "235", "345"}
	actual := collectAllNestedIdsFromObjects(t, data, "routes")

	assert.Equal(t, expected, actual)
}

func TestCollectAllIdsFromObjects(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"id": "234"},
		map[string]interface{}{"id": "345"},
	}
	expected := []string{"234", "345"}
	actual := collectAllIdsFromObjects(t, data, "id")

	assert.Equal(t, expected, actual)
}

package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// stopsOnMeridian places each stop a hundredth of a degree north of the one
// before it, so distances grow with position in the list.
func stopsOnMeridian(ids ...string) map[string]models.Location {
	coordinates := make(map[string]models.Location, len(ids))
	for i, id := range ids {
		coordinates[id] = models.Location{Lat: 37.0 + float64(i)*0.01, Lon: -122.0}
	}
	return coordinates
}

func TestOrderStopsAlongRoute(t *testing.T) {
	tests := []struct {
		name        string
		sequences   [][]string
		coordinates map[string]models.Location
		want        []string
	}{
		{
			name:        "short-turn variant merges into the full pattern",
			sequences:   [][]string{{"A", "B", "C", "D", "E"}, {"C", "D", "E", "F"}},
			coordinates: stopsOnMeridian("A", "B", "C", "D", "E", "F"),
			want:        []string{"A", "B", "C", "D", "E", "F"},
		},
		{
			name:        "loop route lists its terminal once",
			sequences:   [][]string{{"A", "B", "C", "A"}},
			coordinates: stopsOnMeridian("A", "B", "C"),
			want:        []string{"A", "B", "C"},
		},
		{
			name:        "longer branch is emitted before the shorter one",
			sequences:   [][]string{{"near", "trunk", "end"}, {"far", "trunk", "end"}},
			coordinates: stopsOnMeridian("far", "near", "trunk", "end"),
			want:        []string{"far", "near", "trunk", "end"},
		},
		{
			name:      "stops without coordinates keep first-seen order",
			sequences: [][]string{{"near", "trunk", "end"}, {"far", "trunk", "end"}},
			want:      []string{"near", "far", "trunk", "end"},
		},
		{
			name:        "variants that disagree on order drop no stops",
			sequences:   [][]string{{"A", "B", "C"}, {"C", "B", "D"}},
			coordinates: stopsOnMeridian("A", "B", "C", "D"),
			want:        []string{"A", "B", "C", "D"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, utils.OrderStopsAlongRoute(tt.sequences, tt.coordinates))
		})
	}
}

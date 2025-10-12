package scorers

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestRouteScorer_SameProperties(t *testing.T) {
	agency := &gtfs.Agency{Id: "agency1", Name: "Test Agency"}

	routeA := &gtfs.Route{
		Id:        "A",
		ShortName: "1",
		LongName:  "Main Line",
		Agency:    agency,
	}

	routeB := &gtfs.Route{
		Id:        "B",         // Different ID
		ShortName: "1",         // Same short name
		LongName:  "Main Line", // Same long name
		Agency:    agency,      // Same agency
	}

	scorer := &RouteScorer{}
	score := scorer.Score(routeA, routeB)

	assert.Equal(t, 1.0, score, "Should score 1.0 for matching agency and names")
}

func TestRouteScorer_DifferentAgency(t *testing.T) {
	routeA := &gtfs.Route{
		Id:        "A",
		ShortName: "1",
		Agency:    &gtfs.Agency{Id: "agency1"},
	}

	routeB := &gtfs.Route{
		Id:        "B",
		ShortName: "1",
		Agency:    &gtfs.Agency{Id: "agency2"}, // Different agency
	}

	scorer := &RouteScorer{}
	score := scorer.Score(routeA, routeB)

	assert.Equal(t, 0.5, score, "Should score 0.5 when short name matches but agency differs")
}

func TestRouteScorer_InvalidTypes(t *testing.T) {
	scorer := &RouteScorer{}

	score := scorer.Score("not a route", &gtfs.Route{})
	assert.Equal(t, 0.0, score)

	score = scorer.Score(&gtfs.Route{}, "not a route")
	assert.Equal(t, 0.0, score)
}

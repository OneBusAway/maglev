package utils

import (
	"slices"

	"maglev.onebusaway.org/internal/models"
)

// OrderStopsAlongRoute infers a canonical travel order for the stops visited
// by a set of trip stop sequences, porting the legacy Java algorithm
// (RouteBeanServiceImpl.getStopsInOrder with StopGraphComparator). Each
// sequence contributes prev→next edges to a directed graph, skipping any edge
// that would close a cycle, and the graph is topologically sorted. When several
// stops could come next, the one with the longest downstream path in metres is
// emitted first, so a longer branch precedes a shorter one merging into the
// same trunk. Ties keep first-seen order, which makes the result deterministic
// for a given sequence order. Stops missing from coordinates contribute
// zero-length edges.
func OrderStopsAlongRoute(sequences [][]string, coordinates map[string]models.Location) []string {
	graph := stopGraph{outbound: make(map[string][]string)}
	for _, sequence := range sequences {
		for i, stopID := range sequence {
			graph.addNode(stopID)
			// A path back from this stop to the previous one means the edge
			// would form a cycle (a loop returning to its terminal, or variants
			// that disagree on order), so it is dropped.
			if i > 0 && !graph.reaches(stopID, sequence[i-1], map[string]bool{}) {
				graph.addEdge(sequence[i-1], stopID)
			}
		}
	}
	return graph.topologicalSort(coordinates)
}

// stopGraph is a directed graph of stop adjacencies that remembers the order in
// which stops were first added, so sorting ties resolve deterministically.
type stopGraph struct {
	nodes    []string
	outbound map[string][]string
}

func (g *stopGraph) addNode(stopID string) {
	if _, seen := g.outbound[stopID]; !seen {
		g.outbound[stopID] = nil
		g.nodes = append(g.nodes, stopID)
	}
}

func (g *stopGraph) addEdge(from, to string) {
	if !slices.Contains(g.outbound[from], to) {
		g.outbound[from] = append(g.outbound[from], to)
	}
}

// reaches reports whether to is reachable from from, including from == to.
func (g *stopGraph) reaches(from, to string, visited map[string]bool) bool {
	if from == to {
		return true
	}
	visited[from] = true
	for _, next := range g.outbound[from] {
		if !visited[next] && g.reaches(next, to, visited) {
			return true
		}
	}
	return false
}

// topologicalSort repeatedly emits the stop with no remaining inbound edges
// that has the longest downstream path.
func (g *stopGraph) topologicalSort(coordinates map[string]models.Location) []string {
	downstream := make(map[string]float64, len(g.nodes))
	inbound := make(map[string]int, len(g.nodes))
	for _, stopID := range g.nodes {
		g.downstreamDistance(stopID, coordinates, downstream)
		for _, to := range g.outbound[stopID] {
			inbound[to]++
		}
	}

	remaining := slices.Clone(g.nodes)
	order := make([]string, 0, len(remaining))
	for len(remaining) > 0 {
		best := -1
		for i, stopID := range remaining {
			if inbound[stopID] == 0 && (best < 0 || downstream[stopID] > downstream[remaining[best]]) {
				best = i
			}
		}
		next := remaining[best]
		order = append(order, next)
		remaining = slices.Delete(remaining, best, best+1)
		for _, to := range g.outbound[next] {
			inbound[to]--
		}
	}
	return order
}

// downstreamDistance memoizes the length in metres of the longest path that
// starts at stopID and follows outbound edges.
func (g *stopGraph) downstreamDistance(stopID string, coordinates map[string]models.Location, memo map[string]float64) float64 {
	if distance, done := memo[stopID]; done {
		return distance
	}
	var longest float64
	for _, next := range g.outbound[stopID] {
		longest = max(longest, edgeLength(coordinates, stopID, next)+g.downstreamDistance(next, coordinates, memo))
	}
	memo[stopID] = longest
	return longest
}

func edgeLength(coordinates map[string]models.Location, from, to string) float64 {
	fromLocation, fromKnown := coordinates[from]
	toLocation, toKnown := coordinates[to]
	if !fromKnown || !toKnown {
		return 0
	}
	return Distance(fromLocation.Lat, fromLocation.Lon, toLocation.Lat, toLocation.Lon)
}

package merge

import (
	"fmt"
	"github.com/OneBusAway/go-gtfs"
)

// Merger handles the merging of multiple GTFS feeds
type Merger struct {
	opts    Options
	ctx     *Context
	scorers map[string]DuplicateScorer
	refMap  *ReferenceMap
}

// NewMerger creates a new Merger with the given options
func NewMerger(opts Options) *Merger {
	return &Merger{
		opts:    opts,
		ctx:     NewContext(),
		scorers: make(map[string]DuplicateScorer),
		refMap:  NewReferenceMap(),
	}
}

// RegisterScorer adds a custom scorer for an entity type
func (m *Merger) RegisterScorer(entityType string, scorer DuplicateScorer) {
	m.scorers[entityType] = scorer
}

// Merge combines multiple GTFS feeds into one
// Feeds are processed in reverse order (newest first)
func (m *Merger) Merge(feeds []*Feed) (*MergeResult, error) {
	if len(feeds) == 0 {
		return nil, fmt.Errorf("no feeds provided")
	}

	if len(feeds) == 1 {
		return &MergeResult{
			Merged:   feeds[0].Data,
			Strategy: NONE,
		}, nil
	}

	// Initialize result with empty slices
	result := &gtfs.Static{
		Agencies:  make([]gtfs.Agency, 0),
		Stops:     make([]gtfs.Stop, 0),
		Routes:    make([]gtfs.Route, 0),
		Trips:     make([]gtfs.ScheduledTrip, 0),
		Services:  make([]gtfs.Service, 0),
		Shapes:    make([]gtfs.Shape, 0),
		Transfers: make([]gtfs.Transfer, 0),
	}

	// Start with newest feed
	newest := feeds[len(feeds)-1]
	m.copyFeed(result, newest.Data)
	m.markFeedEntities(newest)

	// Process older feeds in reverse chronological order
	for i := len(feeds) - 2; i >= 0; i-- {
		feed := feeds[i]

		// Auto-detect strategy if needed
		strategy := m.opts.Strategy
		if strategy == IDENTITY {
			// For now, use IDENTITY strategy
			// Auto-detection will be implemented later
			strategy = IDENTITY
		}

		// Merge this feed into result
		if err := m.mergeFeed(result, feed, strategy); err != nil {
			return nil, fmt.Errorf("error merging feed %d: %w", i, err)
		}
	}

	duplicates, renamings := m.ctx.GetStatistics()
	return &MergeResult{
		Merged:      result,
		Strategy:    m.opts.Strategy,
		DuplicatesA: duplicates,
		RenamingsA:  renamings,
	}, nil
}

// copyFeed copies all entities from source to destination
func (m *Merger) copyFeed(dest, src *gtfs.Static) {
	dest.Agencies = append(dest.Agencies, src.Agencies...)
	dest.Stops = append(dest.Stops, src.Stops...)
	dest.Routes = append(dest.Routes, src.Routes...)
	dest.Trips = append(dest.Trips, src.Trips...)
	dest.Services = append(dest.Services, src.Services...)
	dest.Shapes = append(dest.Shapes, src.Shapes...)
	dest.Transfers = append(dest.Transfers, src.Transfers...)
}

// markFeedEntities marks all entities from a feed with their source
func (m *Merger) markFeedEntities(feed *Feed) {
	for _, agency := range feed.Data.Agencies {
		m.ctx.MarkEntitySource(agency.Id, feed.Index)
	}
	for _, stop := range feed.Data.Stops {
		m.ctx.MarkEntitySource(stop.Id, feed.Index)
	}
	for _, route := range feed.Data.Routes {
		m.ctx.MarkEntitySource(route.Id, feed.Index)
	}
	for _, trip := range feed.Data.Trips {
		m.ctx.MarkEntitySource(trip.ID, feed.Index)
	}
}

// mergeFeed merges a single feed into the result using the specified strategy
func (m *Merger) mergeFeed(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	// Process entities in dependency order: agencies → stops → routes → trips

	// 1. Merge agencies
	if err := m.mergeAgencies(result, feed, strategy); err != nil {
		return fmt.Errorf("merging agencies: %w", err)
	}

	// 2. Merge stops
	if err := m.mergeStops(result, feed, strategy); err != nil {
		return fmt.Errorf("merging stops: %w", err)
	}

	// 3. Merge routes
	if err := m.mergeRoutes(result, feed, strategy); err != nil {
		return fmt.Errorf("merging routes: %w", err)
	}

	// 4. Merge shapes (before trips, since trips reference shapes)
	if err := m.mergeShapes(result, feed); err != nil {
		return fmt.Errorf("merging shapes: %w", err)
	}

	// 5. Update all references in the feed BEFORE merging child entities
	// This ensures trips reference the correct (merged/renamed) stops, routes, shapes, etc.
	updater := NewReferenceUpdater(m.refMap)
	updater.UpdateAllReferences(feed.Data)

	// 6. Merge trips (now with updated references)
	if err := m.mergeTrips(result, feed, strategy); err != nil {
		return fmt.Errorf("merging trips: %w", err)
	}

	// 7. Merge services
	if err := m.mergeServices(result, feed, strategy); err != nil {
		return fmt.Errorf("merging services: %w", err)
	}

	// 8. Merge other entities
	result.Transfers = append(result.Transfers, feed.Data.Transfers...)

	return nil
}

// mergeAgencies merges agencies from feed into result
func (m *Merger) mergeAgencies(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	for _, agency := range feed.Data.Agencies {
		// Check if this agency is a duplicate
		duplicate := m.findDuplicateAgency(result, &agency, strategy)

		if duplicate != nil {
			// Mark as duplicate, don't add
			m.ctx.RecordDuplicate()
			// Record reference replacement
			m.refMap.RecordReplacement("agency", agency.Id, duplicate.Id)
		} else {
			// Check for ID collision
			if m.hasAgencyID(result, agency.Id) {
				// Rename to avoid collision
				oldID := agency.Id
				agency.Id = m.renameID(agency.Id, feed.Index)
				m.ctx.RecordRenaming()
				// Record reference replacement
				m.refMap.RecordReplacement("agency", oldID, agency.Id)
			}
			result.Agencies = append(result.Agencies, agency)
			m.ctx.MarkEntitySource(agency.Id, feed.Index)
		}
	}
	return nil
}

// mergeStops merges stops from feed into result
func (m *Merger) mergeStops(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	for _, stop := range feed.Data.Stops {
		// Check if this stop is a duplicate
		duplicate := m.findDuplicateStop(result, &stop, strategy)

		if duplicate != nil {
			// Mark as duplicate, don't add
			m.ctx.RecordDuplicate()
			// Record reference replacement
			m.refMap.RecordReplacement("stop", stop.Id, duplicate.Id)
		} else {
			// Check for ID collision
			if m.hasStopID(result, stop.Id) {
				// Rename to avoid collision
				oldID := stop.Id
				stop.Id = m.renameID(stop.Id, feed.Index)
				m.ctx.RecordRenaming()
				// Record reference replacement
				m.refMap.RecordReplacement("stop", oldID, stop.Id)
			}
			result.Stops = append(result.Stops, stop)
			m.ctx.MarkEntitySource(stop.Id, feed.Index)
		}
	}
	return nil
}

// mergeRoutes merges routes from feed into result
func (m *Merger) mergeRoutes(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	for _, route := range feed.Data.Routes {
		// Check for ID collision
		if m.hasRouteID(result, route.Id) {
			// Rename to avoid collision
			oldID := route.Id
			route.Id = m.renameID(route.Id, feed.Index)
			m.ctx.RecordRenaming()
			// Record reference replacement
			m.refMap.RecordReplacement("route", oldID, route.Id)
		}
		result.Routes = append(result.Routes, route)
		m.ctx.MarkEntitySource(route.Id, feed.Index)
	}
	return nil
}

// mergeTrips merges trips from feed into result
func (m *Merger) mergeTrips(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	for _, trip := range feed.Data.Trips {
		// Check if this trip is a duplicate
		duplicate := m.findDuplicateTrip(result, &trip, strategy)

		if duplicate != nil {
			// Mark as duplicate, don't add
			m.ctx.RecordDuplicate()
			// Record reference replacement
			m.refMap.RecordReplacement("trip", trip.ID, duplicate.ID)
		} else {
			// Check for ID collision
			if m.hasTripID(result, trip.ID) {
				// Rename to avoid collision
				oldID := trip.ID
				trip.ID = m.renameID(trip.ID, feed.Index)
				m.ctx.RecordRenaming()
				// Record reference replacement
				m.refMap.RecordReplacement("trip", oldID, trip.ID)
			}
			result.Trips = append(result.Trips, trip)
			m.ctx.MarkEntitySource(trip.ID, feed.Index)
		}
	}
	return nil
}

// mergeServices merges services from feed into result
func (m *Merger) mergeServices(result *gtfs.Static, feed *Feed, strategy Strategy) error {
	// Services are keyed by ID
	result.Services = append(result.Services, feed.Data.Services...)
	return nil
}

// mergeShapes merges shapes from feed into result, handling ID collisions
func (m *Merger) mergeShapes(result *gtfs.Static, feed *Feed) error {
	for _, shape := range feed.Data.Shapes {
		// Check for ID collision
		if m.hasShapeID(result, shape.ID) {
			// Rename to avoid collision
			oldID := shape.ID
			shape.ID = m.renameID(shape.ID, feed.Index)
			m.ctx.RecordRenaming()
			// Record reference replacement so trips get updated
			m.refMap.RecordReplacement("shape", oldID, shape.ID)
		}
		result.Shapes = append(result.Shapes, shape)
		m.ctx.MarkEntitySource(shape.ID, feed.Index)
	}
	return nil
}

// Helper functions for duplicate detection

func (m *Merger) findDuplicateAgency(result *gtfs.Static, agency *gtfs.Agency, strategy Strategy) *gtfs.Agency {
	if strategy == IDENTITY {
		for i := range result.Agencies {
			if result.Agencies[i].Id == agency.Id {
				return &result.Agencies[i]
			}
		}
		return nil
	} else if strategy == FUZZY {
		scorer, ok := m.scorers["agency"]
		if !ok {
			return nil // No scorer registered
		}

		// Convert to []interface{} for generic matching
		candidates := make([]interface{}, len(result.Agencies))
		for i := range result.Agencies {
			candidates[i] = &result.Agencies[i]
		}

		match := m.findBestMatch(agency, candidates, scorer, m.opts.Threshold)
		if match != nil {
			return &result.Agencies[match.IndexB]
		}
	}
	return nil
}

func (m *Merger) findDuplicateStop(result *gtfs.Static, stop *gtfs.Stop, strategy Strategy) *gtfs.Stop {
	if strategy == IDENTITY {
		for i := range result.Stops {
			if result.Stops[i].Id == stop.Id {
				return &result.Stops[i]
			}
		}
		return nil
	} else if strategy == FUZZY {
		scorer, ok := m.scorers["stop"]
		if !ok {
			return nil // No scorer registered
		}

		// Convert to []interface{} for generic matching
		candidates := make([]interface{}, len(result.Stops))
		for i := range result.Stops {
			candidates[i] = &result.Stops[i]
		}

		match := m.findBestMatch(stop, candidates, scorer, m.opts.Threshold)
		if match != nil {
			return &result.Stops[match.IndexB]
		}
	}
	return nil
}

func (m *Merger) findDuplicateTrip(result *gtfs.Static, trip *gtfs.ScheduledTrip, strategy Strategy) *gtfs.ScheduledTrip {
	// Trips without routes can't be meaningfully compared for duplicates
	if trip.Route == nil {
		return nil
	}

	if strategy == IDENTITY {
		for i := range result.Trips {
			if result.Trips[i].ID == trip.ID && result.Trips[i].Route != nil && result.Trips[i].Route.Id == trip.Route.Id {
				return &result.Trips[i]
			}
		}
		return nil
	} else if strategy == FUZZY {
		scorer, ok := m.scorers["trip"]
		if !ok {
			return nil // No scorer registered
		}

		// Convert to []interface{} for generic matching
		candidates := make([]interface{}, len(result.Trips))
		for i := range result.Trips {
			candidates[i] = &result.Trips[i]
		}

		match := m.findBestMatch(trip, candidates, scorer, m.opts.Threshold)
		if match != nil {
			return &result.Trips[match.IndexB]
		}
	}
	return nil
}

// Helper functions for ID collision detection

func (m *Merger) hasAgencyID(result *gtfs.Static, id string) bool {
	for _, agency := range result.Agencies {
		if agency.Id == id {
			return true
		}
	}
	return false
}

func (m *Merger) hasStopID(result *gtfs.Static, id string) bool {
	for _, stop := range result.Stops {
		if stop.Id == id {
			return true
		}
	}
	return false
}

func (m *Merger) hasRouteID(result *gtfs.Static, id string) bool {
	for _, route := range result.Routes {
		if route.Id == id {
			return true
		}
	}
	return false
}

func (m *Merger) hasTripID(result *gtfs.Static, id string) bool {
	for _, trip := range result.Trips {
		if trip.ID == id {
			return true
		}
	}
	return false
}

func (m *Merger) hasShapeID(result *gtfs.Static, id string) bool {
	for _, shape := range result.Shapes {
		if shape.ID == id {
			return true
		}
	}
	return false
}

// renameID applies the configured rename strategy to an ID
func (m *Merger) renameID(id string, feedIndex int) string {
	if m.opts.RenameMode == CONTEXT {
		// Prefix with feed index: a-, b-, c-
		prefix := string(rune('a' + feedIndex))
		return prefix + "-" + id
	}
	// AGENCY mode would use agency prefix, but we need context for that
	// For now, fall back to CONTEXT
	prefix := string(rune('a' + feedIndex))
	return prefix + "-" + id
}

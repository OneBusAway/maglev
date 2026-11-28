package gtfsdb

import (
	"context"
	"database/sql"
	"slices"
	"strings"
)

// readMemo holds rows already read during one request, keyed by query
// argument. It is not safe for concurrent use: it relies on a request running
// its lookups one after another, as snapshotCache in restapi does.
type readMemo struct {
	trips             map[string]Trip
	routes            map[string]Route
	activeServiceIDs  map[string][]string
	tripsByBlockID    map[sql.NullString][]GetTripsByBlockIDRow
	stopTimesByTripID map[string][]StopTime
	frequenciesByTrip map[string][]Frequency
	blockTripSequence map[blockTripSequenceKey]int64
}

type blockTripSequenceKey struct {
	tripID     string
	blockID    sql.NullString
	serviceIDs string
}

type readMemoKey struct{}

// WithReadMemo returns a context carrying an empty read memo, so repeated
// lookups through Queries.Memo during one request reach the database once.
// A context that already carries a memo is returned unchanged.
func WithReadMemo(ctx context.Context) context.Context {
	if readMemoFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, readMemoKey{}, &readMemo{
		trips:             make(map[string]Trip),
		routes:            make(map[string]Route),
		activeServiceIDs:  make(map[string][]string),
		tripsByBlockID:    make(map[sql.NullString][]GetTripsByBlockIDRow),
		stopTimesByTripID: make(map[string][]StopTime),
		frequenciesByTrip: make(map[string][]Frequency),
		blockTripSequence: make(map[blockTripSequenceKey]int64),
	})
}

func readMemoFrom(ctx context.Context) *readMemo {
	memo, _ := ctx.Value(readMemoKey{}).(*readMemo)
	return memo
}

// MemoReader runs a subset of the Queries lookups through the request's read
// memo. With no memo on the context every call goes to the database.
//
// Callers share the rows it returns and must not modify them. Slices come
// back without spare capacity, so an append copies rather than writing where
// another caller can see it. The memo does not track which Queries filled it,
// so do not use one across WithTx or a swapped Queries.
type MemoReader struct {
	queries *Queries
	memo    *readMemo
}

// Memo returns a MemoReader that serves repeated lookups from the read memo
// on ctx, if WithReadMemo installed one.
func (q *Queries) Memo(ctx context.Context) MemoReader {
	return MemoReader{queries: q, memo: readMemoFrom(ctx)}
}

// memoized returns the cached value for key, or runs query and caches its
// result. Errors are never cached, so every caller sees the query's own
// error. pick selects the cache for the query being memoized.
func memoized[K comparable, V any](
	memo *readMemo,
	pick func(*readMemo) map[K]V,
	key K,
	query func() (V, error),
) (V, error) {
	if memo == nil {
		return query()
	}
	cache := pick(memo)
	if value, ok := cache[key]; ok {
		return value, nil
	}
	value, err := query()
	if err == nil {
		cache[key] = value
	}
	return value, err
}

// GetTrip is Queries.GetTrip, memoized.
func (r MemoReader) GetTrip(ctx context.Context, id string) (Trip, error) {
	return memoized(r.memo, func(m *readMemo) map[string]Trip { return m.trips }, id,
		func() (Trip, error) { return r.queries.GetTrip(ctx, id) })
}

// GetRoute is Queries.GetRoute, memoized.
func (r MemoReader) GetRoute(ctx context.Context, id string) (Route, error) {
	return memoized(r.memo, func(m *readMemo) map[string]Route { return m.routes }, id,
		func() (Route, error) { return r.queries.GetRoute(ctx, id) })
}

// GetActiveServiceIDsForDate is Queries.GetActiveServiceIDsForDate, memoized.
func (r MemoReader) GetActiveServiceIDsForDate(ctx context.Context, startDate string) ([]string, error) {
	return memoized(r.memo, func(m *readMemo) map[string][]string { return m.activeServiceIDs }, startDate,
		func() ([]string, error) { return clipped(r.queries.GetActiveServiceIDsForDate(ctx, startDate)) })
}

// GetTripsByBlockID is Queries.GetTripsByBlockID, memoized.
func (r MemoReader) GetTripsByBlockID(ctx context.Context, blockID sql.NullString) ([]GetTripsByBlockIDRow, error) {
	return memoized(r.memo, func(m *readMemo) map[sql.NullString][]GetTripsByBlockIDRow { return m.tripsByBlockID }, blockID,
		func() ([]GetTripsByBlockIDRow, error) { return clipped(r.queries.GetTripsByBlockID(ctx, blockID)) })
}

// GetStopTimesForTrip is Queries.GetStopTimesForTrip, memoized.
func (r MemoReader) GetStopTimesForTrip(ctx context.Context, tripID string) ([]StopTime, error) {
	return memoized(r.memo, func(m *readMemo) map[string][]StopTime { return m.stopTimesByTripID }, tripID,
		func() ([]StopTime, error) { return clipped(r.queries.GetStopTimesForTrip(ctx, tripID)) })
}

// GetFrequenciesForTrip is Queries.GetFrequenciesForTrip, memoized.
func (r MemoReader) GetFrequenciesForTrip(ctx context.Context, tripID string) ([]Frequency, error) {
	return memoized(r.memo, func(m *readMemo) map[string][]Frequency { return m.frequenciesByTrip }, tripID,
		func() ([]Frequency, error) { return clipped(r.queries.GetFrequenciesForTrip(ctx, tripID)) })
}

// GetBlockTripSequence is Queries.GetBlockTripSequence, memoized by trip ID,
// block ID, and canonicalized active service ID set.
func (r MemoReader) GetBlockTripSequence(ctx context.Context, arg GetBlockTripSequenceParams) (int64, error) {
	key := makeBlockTripSequenceKey(arg)
	return memoized(r.memo, func(m *readMemo) map[blockTripSequenceKey]int64 { return m.blockTripSequence }, key,
		func() (int64, error) { return r.queries.GetBlockTripSequence(ctx, arg) })
}

func makeBlockTripSequenceKey(arg GetBlockTripSequenceParams) blockTripSequenceKey {
	if len(arg.ServiceIds) <= 1 {
		return blockTripSequenceKey{
			tripID:     arg.TripID,
			blockID:    arg.BlockID,
			serviceIDs: strings.Join(arg.ServiceIds, "\x00"),
		}
	}
	sorted := slices.Clone(arg.ServiceIds)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	return blockTripSequenceKey{
		tripID:     arg.TripID,
		blockID:    arg.BlockID,
		serviceIDs: strings.Join(sorted, "\x00"),
	}
}

// clipped drops a query result's spare capacity, so a caller appending to a
// shared slice copies it instead of writing into memory another caller holds.
func clipped[T any](rows []T, err error) ([]T, error) {
	return slices.Clip(rows), err
}

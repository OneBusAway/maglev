# GTFS-Flex Parsing (go-gtfs) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach `github.com/OneBusAway/go-gtfs` to parse the four GTFS-Flex files (`locations.geojson`, `location_groups.txt`, `location_group_stops.txt`, `booking_rules.txt`) and the flex columns of `stop_times.txt`/`trips.txt`, exposing them on `gtfs.Static` exactly as spec §2.1 defines, while fixing three pre-existing `stop_times` bugs.

**Architecture:** `ParseStatic` keeps its table-driven CSV dispatch loop; `locations.geojson` (not CSV) is parsed with `encoding/json` before the loop because its presence decides whether `stops.txt` is required. `parseScheduledStopTimes` gains an exactly-one-of stop/location/group resolver, pickup/drop-off window validation and booking-rule resolution, skipping bad rows with `warnings.StaticWarning`s instead of failing the parse. Time interpolation runs over each trip's timed records only; windowed records are partitioned out and merged back in `stop_sequence` order.

**Tech Stack:** Go 1.18 (CI pins `1.18.0`), stdlib only (`archive/zip`, `encoding/json`, `encoding/csv`), `github.com/google/go-cmp/cmp` for tests.

**Spec:** `/private/tmp/claude-501/-Users-aaron-repos-onebusaway-maglev/215fa362-5529-43a3-9cb6-317b7d70341f/scratchpad/spec.md` §0, §1, §2, §9 (struct names in §2.1 are the contract maglev consumes). Wire background: the same scratchpad's `wiki/GTFS-Flex-Support.md` §1.1 and §1.4.

**Repository:** worktree `/Users/aaron/repos/onebusaway/.worktrees/go-gtfs-gtfs-flex`, branch `gtfs-flex` off `origin/main` @ `50d893a`. All file paths below are relative to that root.

## Global Constraints

- Go 1.18 language/library level: no `slices` or `maps` packages, no `min`/`max` builtins, no `errors.Join`, no `strings.CutPrefix`. Generics and `any` are fine (the repo already uses `ptr[T any]`).
- `go test ./...` must pass after every task (locally with the installed toolchain, and it must also compile under Go 1.18 — hence the constraint above).
- Existing `TestParse` cases must keep passing byte-for-byte via `cmp.Diff`: new `Static` slices (`Locations`, `LocationGroups`, `BookingRules`) must stay `nil` when the file is absent, and new `ScheduledStopTime`/`ScheduledTrip` fields must be zero-valued for non-flex rows.
- Struct and method names are exactly those in spec §2.1: `Static.Locations/LocationGroups/BookingRules`, `Location{Id,Name,Description,Geometry}`, `LocationGeometry{Type,Polygons,Raw}`, `LocationGroup{Id,Name,Stops}`, `BookingType`, `BookingRule{...}`, `ScheduledStopTime.{Location,LocationGroup,StartPickupDropOffWindow,EndPickupDropOffWindow,PickupBookingRule,DropOffBookingRule,SafeDurationFactor,SafeDurationOffset}`, `ScheduledTrip.{SafeDurationFactor,SafeDurationOffset}`, `IsWindowed()`, `IsFlex()`.
- Warning kinds are exactly the five in spec §2.2: `StopTimeInvalidReference{Reason}`, `StopTimeInvalidWindow{Reason}`, `LocationGroupUnknownStop{GroupID, StopID}`, `LocationInvalidGeometry{LocationID, Reason}`, `BookingRuleInvalid{BookingRuleID, Reason}`.
- Only the exact file name `locations.geojson` is read; `location.geojson` is ignored.
- Commit messages: imperative mood, subject ≤50 characters, capitalised, no trailing period, blank line, body wrapped at 72 explaining what and why, **no `Co-Authored-By` lines**.
- Do not commit `docs/superpowers/plans/` (this file) as part of the feature commits.

## Review Focus

1. A `locations.geojson` that starts with a UTF-8 BOM (Trillium-exported feeds sometimes do) must still parse; `encoding/json` rejects a BOM, so the parser strips it. Pinned in Task 4 ("BOM is stripped").
2. A Feature whose `geometry` is `null` or absent must produce a `LocationInvalidGeometry` warning and be skipped, not a `Location` with no polygons that a bbox computation downstream would choke on. Pinned in Task 4 ("null geometry warns").
3. A Polygon with an empty `coordinates` array (zero rings) must warn and skip for the same reason. Pinned in Task 4 ("polygon with no rings warns").
4. GeoJSON positions with three coordinates (`[lon, lat, alt]`) are legal per RFC 7946; the parser must keep `[lon, lat]` and ignore altitude rather than reject the feature. Pinned in Task 4 ("altitude is dropped").
5. `location_group_stops.txt` present while `location_groups.txt` is absent (or names a group that does not exist) must not crash and must leave `LocationGroups` nil. Pinned in Task 5 ("membership rows for unknown groups are skipped").

---

## Notes for implementers (read once)

- **Test helpers.** `static_test.go` has `newZipBuilder()` (adds header-only `agency.txt`, `routes.txt`, `stops.txt`, `transfers.txt`, `trips.txt`, `stop_times.txt`), `newZipBuilderWithDefaults()` (adds one agency `a`, route `route_id`, stop `stop_id`, service `service_id`, trip `trip_id`), `.add(fileName, lines...)` (lines are joined with `\n`), `.build()` and `ptr(v)`. `interpolate_test.go` has `dur("08:00:00")` and `almostEq`. Expected values in `TestParse` are compared with `cmp.Diff(actual, expected)` on the whole `*Static`; pointer fields are compared by pointee, so `&defaultStop` in an expectation matches a pointer into `result.Stops`.
- **`csv.File` reuses row storage** (`ReuseRecord = true`), and `warnings.NewStaticWarning` stores `RowContent()` by slice header. A warning raised on a row that is *not* the last row of its file will have its `RowContent` overwritten by later rows. This is a pre-existing quirk, out of scope here. Every fixture in this plan that expects a CSV warning puts the offending row **last** in its file.
- **`ExactTimes`** is `timepointColumn.ReadOr("1") != "0"`, so it is `true` when the `timepoint` column is absent.
- **Continuous pickup/drop-off** on stop_times default to `PickupDropOffPolicy_No` (1) when blank or absent; `pickup_type`/`drop_off_type` default to `PickupDropOffPolicy_Yes` (0). Expectations below spell both out.
- **Absent vs header-only.** A header-only optional CSV parses to a `nil` slice (the `var xs []T` is never appended to), so `BookingRules`/`LocationGroups` are `nil` both when the file is absent and when it is header-only. That is what the Alexandria feed needs.
- Before every commit: `gofmt -l .` must print nothing, `go vet ./...` must pass, `go test ./...` must pass.

---

### Task 1: Fix the three pre-existing stop_times bugs

**Files:**
- Modify: `static.go:798-848` (`parseScheduledStopTimes` row loop)
- Modify: `interpolate.go:55-100` (`interpolateStopTimesByShapeDist`)
- Test: `static_test.go` (new `TestParse` case + new test function), `interpolate_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: unchanged signatures `parseScheduledStopTimes(csv *csv.File, stops []Stop, trips []ScheduledTrip)` and `interpolateStopTimesByShapeDist(times []ScheduledStopTime) []ScheduledStopTime`. New unexported helper `gapShapeDistances(gap []ScheduledStopTime) ([]float64, bool)` in `interpolate.go`. The `trip != currentTrip` pointer comparison introduced here survives into Task 7's rewrite.

#### 1a. Swapped arrival/departure fallback

- [ ] **Step 1: Write the failing test**

Add this case to the `TestParse` table in `static_test.go`, immediately after the `"trip"` case:

```go
		{
			desc: "stop time with only one of arrival and departure time",
			content: newZipBuilder().add(
				"agency.txt",
				"agency_id,agency_name,agency_url,agency_timezone\na,b,c,d",
			).add(
				"routes.txt",
				"route_id,route_type\nroute_id,3",
			).add(
				"stops.txt",
				"stop_id\nstop_id",
			).add(
				"calendar.txt",
				"service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n"+
					"service_id,0,0,0,0,0,0,0,20220504,20220507",
			).add(
				"trips.txt",
				"route_id,service_id,trip_id\nroute_id,service_id,a",
			).add(
				"stop_times.txt",
				"stop_id,trip_id,arrival_time,departure_time,stop_sequence",
				"stop_id,a,04:05:06,,1",
				"stop_id,a,,13:14:15,2",
			).build(),
			expected: &Static{
				Agencies: []Agency{defaultAgency},
				Routes:   []Route{defaultRoute},
				Services: []Service{defaultService},
				Stops:    []Stop{defaultStop},
				Trips: []ScheduledTrip{
					{
						Route:   &defaultRoute,
						Service: &defaultService,
						ID:      "a",
						StopTimes: []ScheduledStopTime{
							{
								Stop:              &defaultStop,
								StopSequence:      1,
								ArrivalTime:       4*time.Hour + 5*time.Minute + 6*time.Second,
								DepartureTime:     4*time.Hour + 5*time.Minute + 6*time.Second,
								ContinuousPickup:  PickupDropOffPolicy_No,
								ContinuousDropOff: PickupDropOffPolicy_No,
								ExactTimes:        true,
							},
							{
								Stop:              &defaultStop,
								StopSequence:      2,
								ArrivalTime:       13*time.Hour + 14*time.Minute + 15*time.Second,
								DepartureTime:     13*time.Hour + 14*time.Minute + 15*time.Second,
								ContinuousPickup:  PickupDropOffPolicy_No,
								ContinuousDropOff: PickupDropOffPolicy_No,
								ExactTimes:        true,
							},
						},
					},
				},
			},
		},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'TestParse/stop_time_with_only_one' -v`
Expected: FAIL — diff shows both `ArrivalTime` and `DepartureTime` are `0s` on both rows (the present value is overwritten by the missing one).

- [ ] **Step 3: Fix the two assignments**

In `static.go`, replace

```go
		if !departureOk {
			arrival = departure
		}
		if !arrivalOk {
			departure = arrival
		}
```

with

```go
		if !departureOk {
			departure = arrival
		}
		if !arrivalOk {
			arrival = departure
		}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add static.go static_test.go
git commit -m "Fix swapped arrival/departure fallback" -m "When exactly one of arrival_time and departure_time was present the
parser copied the missing value over the present one, zeroing both and
leaving interpolation to fabricate replacements. Copy the present value
into the missing one instead, which is what GTFS intends."
```

#### 1b. Unknown `trip_id` nil-pointer panic

- [ ] **Step 1: Write the failing test**

Add to `static_test.go` (after `TestParseStatic_ContinuousPickupDropOffDefaultToNo`):

```go
func TestParseStatic_UnknownTripIDIsSkipped(t *testing.T) {
	// The second row switches to a trip_id that trips.txt does not define.
	// Before the fix this dereferenced a nil *ScheduledTrip while presizing
	// its StopTimes slice.
	content := newZipBuilderWithDefaults().add(
		"stop_times.txt",
		"stop_id,trip_id,stop_sequence",
		"stop_id,trip_id,1",
		"stop_id,ghost,2",
	).build()

	static, err := ParseStatic(content, ParseStaticOptions{})
	if err != nil {
		t.Fatalf("ParseStatic() got error %v, want nil", err)
	}
	if len(static.Trips) != 1 {
		t.Fatalf("got %d trips, want 1", len(static.Trips))
	}
	if got := len(static.Trips[0].StopTimes); got != 1 {
		t.Errorf("got %d stop times on trip_id, want 1 (the ghost row must be dropped)", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestParseStatic_UnknownTripIDIsSkipped -v`
Expected: FAIL with `panic: runtime error: invalid memory address or nil pointer dereference` in `parseScheduledStopTimes`.

- [ ] **Step 3: Guard the trip lookup**

In `static.go`, replace the block

```go
		tripID := tripIDColumn.Read()
		if currentTrip == nil || currentTripID != tripID {
			thisTrip := idToTrip[tripID]
			if currentTrip != nil && cap(thisTrip.StopTimes) == 0 {
				thisTrip.StopTimes = make([]ScheduledStopTime, 0, len(currentTrip.StopTimes))
			}
			currentTrip = thisTrip
			currentTripID = tripID
		}
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			log.Printf("Skipping stop time because of missing keys %s", missingKeys)
			continue
		}
		if stopTime.Stop == nil {
			continue
		}
		if currentTrip == nil {
			continue
		}
		currentTrip.StopTimes = append(currentTrip.StopTimes, stopTime)
```

with

```go
		tripID := tripIDColumn.Read()
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			log.Printf("Skipping stop time because of missing keys %s", missingKeys)
			continue
		}
		if stopTime.Stop == nil {
			continue
		}
		trip := idToTrip[tripID]
		if trip == nil {
			log.Printf("Skipping stop time because trip %q is unknown", tripID)
			continue
		}
		if trip != currentTrip {
			// Presize the new trip's slice from the previous trip's length; trips
			// on the same route usually have similar stop counts.
			if currentTrip != nil && cap(trip.StopTimes) == 0 {
				trip.StopTimes = make([]ScheduledStopTime, 0, len(currentTrip.StopTimes))
			}
			currentTrip = trip
		}
		trip.StopTimes = append(trip.StopTimes, stopTime)
```

and delete the now-unused `var currentTripID string` declaration above the loop.

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS (a log line `Skipping stop time because trip "a" is unknown` from fixtures built with `newZipBuilderWithDefaults()` is expected and harmless: its default `stop_times.txt` row references trip `a`, which its `trips.txt` does not define).

- [ ] **Step 5: Commit**

```bash
git add static.go static_test.go
git commit -m "Skip stop_times rows for unknown trip ids" -m "A stop_times.txt row whose trip_id is not in trips.txt (reachable with
real feeds because trips are dropped for missing routes or services)
dereferenced a nil trip while presizing its StopTimes slice. Look the
trip up once, log and skip the row when it is unknown, and compare
trips by pointer instead of tracking the current trip id separately."
```

#### 1c. Nil `ShapeDistanceTraveled` in shape-distance interpolation

- [ ] **Step 1: Write the failing tests**

Add to `interpolate_test.go`:

```go
func TestInterpolateStopTimesByShapeDist_NilDistanceFallsBackToEven(t *testing.T) {
	st := []ScheduledStopTime{
		{StopSequence: 1, ArrivalTime: dur("08:00:00"), DepartureTime: dur("08:00:00"), ShapeDistanceTraveled: ptr(0.0), ExactTimes: true},
		{StopSequence: 2},
		{StopSequence: 3, ShapeDistanceTraveled: ptr(9.0)},
		{StopSequence: 4, ArrivalTime: dur("08:30:00"), DepartureTime: dur("08:30:00"), ShapeDistanceTraveled: ptr(10.0), ExactTimes: true},
	}
	wantArr := []time.Duration{dur("08:00:00"), dur("08:10:00"), dur("08:20:00"), dur("08:30:00")}

	got := interpolateStopTimesByShapeDist(st)
	for i := range got {
		if !almostEq(got[i].ArrivalTime, wantArr[i]) {
			t.Errorf("nil distance: arrival %d: want %v got %v", i, wantArr[i], got[i].ArrivalTime)
		}
		if !almostEq(got[i].DepartureTime, wantArr[i]) {
			t.Errorf("nil distance: depart %d: want %v got %v", i, wantArr[i], got[i].DepartureTime)
		}
	}
}

func TestInterpolateStopTimesByShapeDist_NilEndpointDistanceFallsBackToEven(t *testing.T) {
	st := []ScheduledStopTime{
		{StopSequence: 1, ArrivalTime: dur("08:00:00"), DepartureTime: dur("08:00:00")},
		{StopSequence: 2, ShapeDistanceTraveled: ptr(9.0)},
		{StopSequence: 3, ArrivalTime: dur("08:20:00"), DepartureTime: dur("08:20:00"), ShapeDistanceTraveled: ptr(10.0)},
	}
	got := interpolateStopTimesByShapeDist(st)
	if !almostEq(got[1].ArrivalTime, dur("08:10:00")) {
		t.Errorf("nil endpoint distance: want 08:10:00 got %v", got[1].ArrivalTime)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestInterpolateStopTimesByShapeDist_Nil' -v`
Expected: the first FAILs with a nil-pointer panic at `dist := *result[startIdx+j].ShapeDistanceTraveled`; the second FAILs with a wrong (distance-weighted from 0) time.

- [ ] **Step 3: Rewrite the gap handling**

Replace the whole of `interpolateStopTimesByShapeDist` in `interpolate.go` with:

```go
// interpolateStopTimesByShapeDist fills missing arrival/departure times using
// shape_dist_traveled as the weight. A gap in which any record (including the
// two bounding timed records) has no distance falls back to even
// interpolation for that gap.
func interpolateStopTimesByShapeDist(times []ScheduledStopTime) []ScheduledStopTime {
	result := make([]ScheduledStopTime, len(times))
	copy(result, times)
	n := len(result)
	if n == 0 {
		return nil
	}

	for tType := 0; tType < 2; tType++ {
		i := 0
		for i < n {
			if getTime(&result[i], tType) != 0 {
				i++
				continue
			}
			startIdx := i - 1
			endIdx := i
			for endIdx < n && getTime(&result[endIdx], tType) == 0 {
				endIdx++
			}
			intervals := endIdx - startIdx
			if startIdx < 0 || endIdx >= n || intervals <= 0 {
				i = endIdx
				continue
			}
			startTime := getTime(&result[startIdx], tType)
			endTime := getTime(&result[endIdx], tType)
			if endTime <= startTime {
				i = endIdx
				continue
			}
			dists, ok := gapShapeDistances(result[startIdx : endIdx+1])
			if ok && dists[intervals] > dists[0] {
				span := dists[intervals] - dists[0]
				for j := 1; j < intervals; j++ {
					w := (dists[j] - dists[0]) / span
					setTime(&result[startIdx+j], tType, startTime+time.Duration(float64(endTime-startTime)*w))
				}
			} else {
				delta := (endTime - startTime) / time.Duration(intervals)
				for j := 1; j < intervals; j++ {
					setTime(&result[startIdx+j], tType, startTime+time.Duration(j)*delta)
				}
			}
			i = endIdx
		}
	}
	return result
}

// gapShapeDistances returns the shape distances of every record in gap, or
// false when any record has none.
func gapShapeDistances(gap []ScheduledStopTime) ([]float64, bool) {
	dists := make([]float64, len(gap))
	for i := range gap {
		if gap[i].ShapeDistanceTraveled == nil {
			return nil, false
		}
		dists[i] = *gap[i].ShapeDistanceTraveled
	}
	return dists, true
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS, including the pre-existing `TestInterpolateStopTimesByShapeDist_*` cases.

- [ ] **Step 5: Commit**

```bash
git add interpolate.go interpolate_test.go
git commit -m "Fall back to even interpolation on nil distance" -m "interpolateStopTimesByShapeDist dereferenced ShapeDistanceTraveled on
every record inside a gap without checking for nil, which panics on
feeds where shape_dist_traveled is populated on some rows but not all.
Collect the gap's distances up front and interpolate evenly when any
are missing."
```

---

### Task 2: Flex warning kinds and a non-CSV warning constructor

**Files:**
- Modify: `warnings/warnings.go`
- Modify: `constants/constants.go`
- Test: `warnings/warnings_test.go`

**Interfaces:**
- Produces (package `warnings`):
  - `type StopTimeInvalidReference struct{ Reason string }`
  - `type StopTimeInvalidWindow struct{ Reason string }`
  - `type LocationGroupUnknownStop struct{ GroupID, StopID string }`
  - `type LocationInvalidGeometry struct{ LocationID, Reason string }`
  - `type BookingRuleInvalid struct{ BookingRuleID, Reason string }`
  - each with `func (w X) Error() string`, satisfying `StaticWarningKind`.
  - `func NewFileWarning(file constants.StaticFile, kind StaticWarningKind) StaticWarning` — for files that are not CSV (`RowNumber` 0, `RowContent`/`HeaderContent` nil).
- Produces (package `constants`): `LocationsGeoJSONFile StaticFile = "locations.geojson"`, `BookingRulesFile StaticFile = "booking_rules.txt"`, `LocationGroupsFile StaticFile = "location_groups.txt"`, `LocationGroupStopsFile StaticFile = "location_group_stops.txt"`, `StopTimesFile StaticFile = "stop_times.txt"`.

- [ ] **Step 1: Write the failing test**

Replace the contents of `warnings/warnings_test.go` with:

```go
package warnings

import (
	"testing"

	"github.com/OneBusAway/go-gtfs/constants"
	"github.com/google/go-cmp/cmp"
)

// Verify that StaticWarningKind satisfies the error interface.
var (
	w StaticWarningKind = nil
	e error             = w
)

func TestFlexWarningKindsHaveMessages(t *testing.T) {
	for _, tc := range []struct {
		kind StaticWarningKind
		want string
	}{
		{StopTimeInvalidReference{Reason: "r"}, "stop time has an invalid reference: r"},
		{StopTimeInvalidWindow{Reason: "r"}, "stop time has an invalid pickup/drop-off window: r"},
		{LocationGroupUnknownStop{GroupID: "g", StopID: "s"}, `location group "g" references unknown stop "s"`},
		{LocationInvalidGeometry{LocationID: "l", Reason: "r"}, `location "l" has invalid geometry: r`},
		{BookingRuleInvalid{BookingRuleID: "b", Reason: "r"}, `booking rule "b" is invalid: r`},
	} {
		if got := tc.kind.Error(); got != tc.want {
			t.Errorf("%T.Error() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestNewFileWarning(t *testing.T) {
	kind := LocationInvalidGeometry{LocationID: "l", Reason: "r"}
	got := NewFileWarning(constants.LocationsGeoJSONFile, kind)
	want := StaticWarning{Kind: kind, File: constants.LocationsGeoJSONFile}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("NewFileWarning() mismatch (-got +want):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./warnings/ -v`
Expected: FAIL to compile — `undefined: StopTimeInvalidReference`, `undefined: NewFileWarning`, `undefined: constants.LocationsGeoJSONFile`.

- [ ] **Step 3: Add the constants and warning kinds**

`constants/constants.go`:

```go
package constants

type StaticFile string

const (
	AgencyFile             StaticFile = "agency.txt"
	StopTimesFile          StaticFile = "stop_times.txt"
	BookingRulesFile       StaticFile = "booking_rules.txt"
	LocationGroupsFile     StaticFile = "location_groups.txt"
	LocationGroupStopsFile StaticFile = "location_group_stops.txt"
	LocationsGeoJSONFile   StaticFile = "locations.geojson"
)
```

Append to `warnings/warnings.go` (after `NewStaticWarning`):

```go
// NewFileWarning builds a warning for a file that is not CSV, such as
// locations.geojson, where there is no row to point at.
func NewFileWarning(file constants.StaticFile, kind StaticWarningKind) StaticWarning {
	return StaticWarning{
		Kind: kind,
		File: file,
	}
}
```

and at the end of the file:

```go
// StopTimeInvalidReference is raised when a stop_times.txt row does not
// reference exactly one of stop_id, location_id and location_group_id, or
// references an id (including a booking rule id) that does not resolve.
type StopTimeInvalidReference struct {
	Reason string
}

func (w StopTimeInvalidReference) Error() string {
	return fmt.Sprintf("stop time has an invalid reference: %s", w.Reason)
}

// StopTimeInvalidWindow is raised when a stop_times.txt row's
// start/end_pickup_drop_off_window pair violates the GTFS presence rules.
type StopTimeInvalidWindow struct {
	Reason string
}

func (w StopTimeInvalidWindow) Error() string {
	return fmt.Sprintf("stop time has an invalid pickup/drop-off window: %s", w.Reason)
}

// LocationGroupUnknownStop is raised when location_group_stops.txt names a
// stop that stops.txt does not define.
type LocationGroupUnknownStop struct {
	GroupID string
	StopID  string
}

func (w LocationGroupUnknownStop) Error() string {
	return fmt.Sprintf("location group %q references unknown stop %q", w.GroupID, w.StopID)
}

// LocationInvalidGeometry is raised when a locations.geojson Feature cannot be
// used: no id, no geometry, an unsupported geometry type or malformed
// coordinates.
type LocationInvalidGeometry struct {
	LocationID string
	Reason     string
}

func (w LocationInvalidGeometry) Error() string {
	return fmt.Sprintf("location %q has invalid geometry: %s", w.LocationID, w.Reason)
}

// BookingRuleInvalid is raised when a booking_rules.txt row is skipped.
type BookingRuleInvalid struct {
	BookingRuleID string
	Reason        string
}

func (w BookingRuleInvalid) Error() string {
	return fmt.Sprintf("booking rule %q is invalid: %s", w.BookingRuleID, w.Reason)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add constants/constants.go warnings/warnings.go warnings/warnings_test.go
git commit -m "Add GTFS-Flex warning kinds" -m "The flex parser skips malformed stop_times, location, location group
and booking rule rows instead of failing the parse, and callers need to
see why. Add one StaticWarningKind per failure class, file name
constants for the flex files, and NewFileWarning for locations.geojson,
which has no CSV row to attach a warning to."
```

---

### Task 3: Parse `booking_rules.txt`

**Files:**
- Modify: `enums.go` (append `BookingType`)
- Modify: `static.go` (`Static` struct, new `BookingRule` struct, dispatch table entry before `trips.txt`, new `parseBookingRules`, new `parseOptionalGtfsTime`)
- Test: `static_test.go` (`TestParse` cases)

**Interfaces:**
- Consumes: `warnings.BookingRuleInvalid`, `constants.BookingRulesFile` (Task 2); existing `parseInt32(string) *int32`, `parseGtfsTimeToDuration(string) (time.Duration, bool)`, `checkForMissingColumns(*csv.File)`.
- Produces:
  ```go
  type BookingType int32
  const (
      BookingType_RealTime  BookingType = 0
      BookingType_SameDay   BookingType = 1
      BookingType_PriorDays BookingType = 2
  )
  func (b BookingType) String() string
  func parseBookingType(s string) (BookingType, bool)

  type BookingRule struct {
      Id                     string
      Type                   BookingType
      PriorNoticeDurationMin *int32
      PriorNoticeDurationMax *int32
      PriorNoticeLastDay     *int32
      PriorNoticeLastTime    *time.Duration
      PriorNoticeStartDay    *int32
      PriorNoticeStartTime   *time.Duration
      PriorNoticeServiceId   string
      Message                string
      PickupMessage          string
      DropOffMessage         string
      PhoneNumber            string
      InfoUrl                string
      BookingUrl             string
  }
  // on Static:
  BookingRules []BookingRule // nil when booking_rules.txt absent or header-only
  func parseBookingRules(csv *csv.File) ([]BookingRule, []warnings.StaticWarning)
  func parseOptionalGtfsTime(s string) *time.Duration // nil when empty or unparsable
  ```

- [ ] **Step 1: Write the failing tests**

Add these cases to the `TestParse` table in `static_test.go` (after the `"calendar_dates.txt"` case):

```go
		{
			desc: "booking rules with all fields",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type,prior_notice_duration_min,prior_notice_duration_max,"+
					"prior_notice_last_day,prior_notice_last_time,prior_notice_start_day,prior_notice_start_time,"+
					"prior_notice_service_id,message,pickup_message,drop_off_message,phone_number,info_url,booking_url",
				"br_1,2,,,1,17:00:00,14,00:00:00,weekdays,msg,pmsg,dmsg,555-0100,https://info.example,https://book.example",
				"br_2,1,60,1440,,,,,,,,,,,",
			).build(),
			expected: &Static{
				BookingRules: []BookingRule{
					{
						Id:                   "br_1",
						Type:                 BookingType_PriorDays,
						PriorNoticeLastDay:   ptr(int32(1)),
						PriorNoticeLastTime:  ptr(17 * time.Hour),
						PriorNoticeStartDay:  ptr(int32(14)),
						PriorNoticeStartTime: ptr(time.Duration(0)),
						PriorNoticeServiceId: "weekdays",
						Message:              "msg",
						PickupMessage:        "pmsg",
						DropOffMessage:       "dmsg",
						PhoneNumber:          "555-0100",
						InfoUrl:              "https://info.example",
						BookingUrl:           "https://book.example",
					},
					{
						Id:                     "br_2",
						Type:                   BookingType_SameDay,
						PriorNoticeDurationMin: ptr(int32(60)),
						PriorNoticeDurationMax: ptr(int32(1440)),
					},
				},
			},
		},
		{
			// Real Michigan feeds omit prior_notice_duration_min on type 1 and
			// prior_notice_last_time on type 2. Import them with nils (spec §9.4).
			desc: "booking rules missing conditionally required fields",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type,prior_notice_duration_min,prior_notice_last_day,prior_notice_last_time",
				"br_same_day,1,,,",
				"br_prior_days,2,,7,",
			).build(),
			expected: &Static{
				BookingRules: []BookingRule{
					{Id: "br_same_day", Type: BookingType_SameDay},
					{Id: "br_prior_days", Type: BookingType_PriorDays, PriorNoticeLastDay: ptr(int32(7))},
				},
			},
		},
		{
			desc: "booking rule with unparsable time is kept with a nil time",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type,prior_notice_last_time",
				"br_1,2,soon",
			).build(),
			expected: &Static{
				BookingRules: []BookingRule{{Id: "br_1", Type: BookingType_PriorDays}},
			},
		},
		{
			desc: "booking rule with unparsable type is skipped",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type",
				"br_ok,0",
				"br_bad,9",
			).build(),
			expected: &Static{
				BookingRules: []BookingRule{{Id: "br_ok", Type: BookingType_RealTime}},
				Warnings: []warnings.StaticWarning{
					{
						Kind:          warnings.BookingRuleInvalid{BookingRuleID: "br_bad", Reason: `unparsable booking_type "9"`},
						File:          constants.BookingRulesFile,
						RowNumber:     2,
						RowContent:    []string{"br_bad", "9"},
						HeaderContent: []string{"booking_rule_id", "booking_type"},
					},
				},
			},
		},
		{
			desc: "booking rule with missing id is skipped",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type",
				",1",
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					{
						Kind:          warnings.BookingRuleInvalid{BookingRuleID: "", Reason: "missing values [booking_rule_id]"},
						File:          constants.BookingRulesFile,
						RowNumber:     1,
						RowContent:    []string{"", "1"},
						HeaderContent: []string{"booking_rule_id", "booking_type"},
					},
				},
			},
		},
		{
			desc: "booking rules file with missing columns",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id\nbr_1",
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					{
						Kind:          warnings.MissingColumns{Columns: []string{"booking_type"}},
						File:          constants.BookingRulesFile,
						RowNumber:     0,
						RowContent:    []string{"booking_rule_id"},
						HeaderContent: []string{"booking_rule_id"},
					},
				},
			},
		},
		{
			desc: "header-only booking rules file yields nil",
			content: newZipBuilder().add(
				"booking_rules.txt",
				"booking_rule_id,booking_type",
			).build(),
			expected: &Static{},
		},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestParse/booking' -v`
Expected: FAIL to compile — `undefined: BookingRule`, `undefined: BookingType_PriorDays`.

- [ ] **Step 3: Add the enum**

Append to `enums.go`:

```go
// BookingType describes how far in advance an on-demand trip can be booked.
//
// This is a Go representation of the enum described in the `booking_type` field of `booking_rules.txt`.
type BookingType int32

const (
	// Real-time booking.
	BookingType_RealTime BookingType = 0
	// Up to same-day booking with advance notice.
	BookingType_SameDay BookingType = 1
	// Up to prior day(s) booking.
	BookingType_PriorDays BookingType = 2
)

func parseBookingType(s string) (BookingType, bool) {
	switch s {
	case "0":
		return BookingType_RealTime, true
	case "1":
		return BookingType_SameDay, true
	case "2":
		return BookingType_PriorDays, true
	default:
		return BookingType_RealTime, false
	}
}

func (b BookingType) String() string {
	switch b {
	case BookingType_RealTime:
		return "REAL_TIME"
	case BookingType_SameDay:
		return "SAME_DAY"
	case BookingType_PriorDays:
		return "PRIOR_DAYS"
	default:
		return "UNKNOWN"
	}
}
```

- [ ] **Step 4: Add the struct, dispatch entry and parser**

In `static.go`, change `Static` to:

```go
type Static struct {
	Agencies  []Agency
	Routes    []Route
	Stops     []Stop
	Transfers []Transfer
	Services  []Service
	Trips     []ScheduledTrip
	Shapes    []Shape

	// BookingRules is nil when booking_rules.txt is absent.
	BookingRules []BookingRule

	// Warnings raised during GTFS static parsing.
	Warnings []warnings.StaticWarning
}
```

Add after the `Frequency` struct:

```go
// BookingRule corresponds to a single row in the booking_rules.txt file.
//
// Fields that GTFS marks conditionally required are pointers and stay nil when
// the feed omits them; real feeds do omit them, and consumers decide how to
// treat the gap.
type BookingRule struct {
	Id                     string
	Type                   BookingType
	PriorNoticeDurationMin *int32 // minutes
	PriorNoticeDurationMax *int32 // minutes
	PriorNoticeLastDay     *int32
	PriorNoticeLastTime    *time.Duration
	PriorNoticeStartDay    *int32
	PriorNoticeStartTime   *time.Duration
	PriorNoticeServiceId   string
	Message                string
	PickupMessage          string
	DropOffMessage         string
	PhoneNumber            string
	InfoUrl                string
	BookingUrl             string
}
```

Insert this dispatch entry into the `ParseStatic` table **between the `shapes.txt` entry and the `trips.txt` entry**:

```go
		{
			File: constants.BookingRulesFile,
			Action: func(file *csv.File) (w []warnings.StaticWarning) {
				result.BookingRules, w = parseBookingRules(file)
				return
			},
			Optional: true,
		},
```

Add after `parseGtfsTimeToDuration`:

```go
// parseOptionalGtfsTime parses an optional HH:MM:SS cell; nil when the cell is
// empty or unparsable.
func parseOptionalGtfsTime(s string) *time.Duration {
	d, ok := parseGtfsTimeToDuration(s)
	if !ok {
		return nil
	}
	return &d
}

func parseBookingRules(csv *csv.File) ([]BookingRule, []warnings.StaticWarning) {
	idColumn := csv.RequiredColumn("booking_rule_id")
	typeColumn := csv.RequiredColumn("booking_type")
	durationMinColumn := csv.OptionalColumn("prior_notice_duration_min")
	durationMaxColumn := csv.OptionalColumn("prior_notice_duration_max")
	lastDayColumn := csv.OptionalColumn("prior_notice_last_day")
	lastTimeColumn := csv.OptionalColumn("prior_notice_last_time")
	startDayColumn := csv.OptionalColumn("prior_notice_start_day")
	startTimeColumn := csv.OptionalColumn("prior_notice_start_time")
	serviceIDColumn := csv.OptionalColumn("prior_notice_service_id")
	messageColumn := csv.OptionalColumn("message")
	pickupMessageColumn := csv.OptionalColumn("pickup_message")
	dropOffMessageColumn := csv.OptionalColumn("drop_off_message")
	phoneNumberColumn := csv.OptionalColumn("phone_number")
	infoUrlColumn := csv.OptionalColumn("info_url")
	bookingUrlColumn := csv.OptionalColumn("booking_url")

	if w := checkForMissingColumns(csv); len(w) > 0 {
		return nil, w
	}

	var w []warnings.StaticWarning
	var rules []BookingRule
	for csv.NextRow() {
		id := idColumn.Read()
		rawType := typeColumn.Read()
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			w = append(w, warnings.NewStaticWarning(csv, warnings.BookingRuleInvalid{
				BookingRuleID: id,
				Reason:        fmt.Sprintf("missing values %s", missingKeys),
			}))
			continue
		}
		bookingType, ok := parseBookingType(rawType)
		if !ok {
			w = append(w, warnings.NewStaticWarning(csv, warnings.BookingRuleInvalid{
				BookingRuleID: id,
				Reason:        fmt.Sprintf("unparsable booking_type %q", rawType),
			}))
			continue
		}
		rules = append(rules, BookingRule{
			Id:                     id,
			Type:                   bookingType,
			PriorNoticeDurationMin: parseInt32(durationMinColumn.Read()),
			PriorNoticeDurationMax: parseInt32(durationMaxColumn.Read()),
			PriorNoticeLastDay:     parseInt32(lastDayColumn.Read()),
			PriorNoticeLastTime:    parseOptionalGtfsTime(lastTimeColumn.Read()),
			PriorNoticeStartDay:    parseInt32(startDayColumn.Read()),
			PriorNoticeStartTime:   parseOptionalGtfsTime(startTimeColumn.Read()),
			PriorNoticeServiceId:   serviceIDColumn.Read(),
			Message:                messageColumn.Read(),
			PickupMessage:          pickupMessageColumn.Read(),
			DropOffMessage:         dropOffMessageColumn.Read(),
			PhoneNumber:            phoneNumberColumn.Read(),
			InfoUrl:                infoUrlColumn.Read(),
			BookingUrl:             bookingUrlColumn.Read(),
		})
	}
	return rules, w
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./...`
Expected: PASS. All pre-existing `TestParse` cases still pass because `BookingRules` is nil when the file is absent.

- [ ] **Step 6: Commit**

```bash
git add enums.go static.go static_test.go
git commit -m "Parse booking_rules.txt" -m "Add BookingType and BookingRule and read booking_rules.txt into
Static.BookingRules before trips.txt so stop_times can reference the
rules. Conditionally required fields are pointers and import as nil
when absent, because real feeds omit prior_notice_duration_min on
booking_type 1 and prior_notice_last_time on booking_type 2. Only an
unparsable booking_type or a missing id skips a row, with a warning."
```

---

### Task 4: Parse `locations.geojson`, relax `stops.txt`, treat zero-byte optional files as absent

**Files:**
- Modify: `static.go` (`Static` struct, new `Location`/`LocationGeometry` structs, `ParseStatic` pre-loop GeoJSON parse and zero-byte handling, `stops.txt` conditional optionality)
- Create: `locations.go` (GeoJSON decoding)
- Test: `static_test.go` (`TestParse` cases, two new test functions, `zipBuilder.remove` helper)

**Interfaces:**
- Consumes: `warnings.NewFileWarning`, `warnings.LocationInvalidGeometry`, `constants.LocationsGeoJSONFile` (Task 2).
- Produces:
  ```go
  // Location is a GeoJSON Feature from locations.geojson (Polygon or MultiPolygon only).
  type Location struct {
      Id          string
      Name        string // properties.stop_name
      Description string // properties.stop_desc
      Geometry    LocationGeometry
  }

  // LocationGeometry normalises Polygon and MultiPolygon to one shape.
  // Polygons[p][r][i] = [lon, lat]; ring 0 is the exterior, rings 1.. are holes.
  type LocationGeometry struct {
      Type     string          // "Polygon" | "MultiPolygon"
      Polygons [][][][2]float64
      Raw      json.RawMessage // the geometry object verbatim
  }

  // on Static:
  Locations []Location // nil when locations.geojson absent

  // locations.go
  func parseLocations(content []byte) ([]Location, []warnings.StaticWarning, error)
  func parseLocationGeometry(raw json.RawMessage) (LocationGeometry, error)
  func normalisePolygons(polygons [][][][]float64) ([][][][2]float64, error)
  func locationID(feature geoJSONFeature) string
  func jsonScalarString(raw json.RawMessage) string

  // static.go
  func parseLocationsFile(zipFile *zip.File, result *Static) (present bool, err error)
  func readZipFile(zipFile *zip.File) ([]byte, error)
  func isAbsentOrEmpty(zipFile *zip.File) bool
  ```
- Test helper produced: `func (z *zipBuilder) remove(fileName string) *zipBuilder`.

#### 4a. Zero-byte optional files are treated as absent

- [ ] **Step 1: Write the failing test**

Add to the `TestParse` table in `static_test.go` (after `"empty frequencies"`):

```go
		{
			// csv.New rejects a file with no header row; an empty optional file
			// must be treated as absent rather than aborting the whole parse.
			desc: "zero-byte optional file is treated as absent",
			content: newZipBuilder().add(
				"frequencies.txt", "",
			).build(),
			expected: &Static{},
		},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'TestParse/zero-byte' -v`
Expected: FAIL with `error when parsing: failed to read "frequencies.txt": CSV file contains no rows`.

- [ ] **Step 3: Skip empty optional files in the dispatch loop**

In `ParseStatic`, replace

```go
		zipFile := fileNameToFile[table.File]
		if zipFile == nil {
			if table.Optional {
				table.PostProcess()
				continue
			}
			return nil, fmt.Errorf("no %q file in GTFS static feed", table.File)
		}
```

with

```go
		zipFile := fileNameToFile[table.File]
		if table.Optional && isAbsentOrEmpty(zipFile) {
			table.PostProcess()
			continue
		}
		if zipFile == nil {
			return nil, fmt.Errorf("no %q file in GTFS static feed", table.File)
		}
```

and add after `openCsvFile`:

```go
// isAbsentOrEmpty reports whether an optional feed file should be treated as
// not provided. A zero-byte file has no header row and is meaningless.
func isAbsentOrEmpty(zipFile *zip.File) bool {
	return zipFile == nil || zipFile.UncompressedSize64 == 0
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add static.go static_test.go
git commit -m "Treat zero-byte optional files as absent" -m "csv.New returns an error for a file with no rows, which aborted the
whole parse when a feed shipped an empty optional file. Real flex feeds
ship empty location_groups.txt files; treat any zero-byte optional
file as if it were not in the archive."
```

#### 4b. `locations.geojson`

- [ ] **Step 1: Write the failing tests**

Add `"encoding/json"` to the imports of `static_test.go`. Add this helper after `zipBuilder.add`:

```go
func (z *zipBuilder) remove(fileName string) *zipBuilder {
	delete(z.m, fileName)
	return z
}
```

Add these cases to the `TestParse` table (after the booking rule cases from Task 3):

```go
		{
			desc: "locations.geojson polygon",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"zone_a",`+
					`"properties":{"stop_name":"Zone A","stop_desc":"North side"},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id:          "zone_a",
						Name:        "Zone A",
						Description: "North side",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson multipolygon with a hole",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"zone_m","properties":{},`+
					`"geometry":{"type":"MultiPolygon","coordinates":[`+
					`[[[0,0],[4,0],[4,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]],`+
					`[[[10,10],[11,10],[11,11],[10,10]]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "zone_m",
						Geometry: LocationGeometry{
							Type: "MultiPolygon",
							Polygons: [][][][2]float64{
								{{{0, 0}, {4, 0}, {4, 4}, {0, 0}}, {{1, 1}, {2, 1}, {2, 2}, {1, 1}}},
								{{{10, 10}, {11, 10}, {11, 11}, {10, 10}}},
							},
							Raw: json.RawMessage(`{"type":"MultiPolygon","coordinates":[` +
								`[[[0,0],[4,0],[4,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]],` +
								`[[[10,10],[11,10],[11,11],[10,10]]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson numeric feature id",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":42,"properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "42",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson id falls back to properties",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[`+
					`{"type":"Feature","properties":{"id":"from_props"},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}},`+
					`{"type":"Feature","properties":{"location_id":"from_draft"},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "from_props",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
					{
						Id: "from_draft",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson altitude is dropped",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"z","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0,5],[1,0,5],[1,1,5],[0,0,5]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "z",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0,5],[1,0,5],[1,1,5],[0,0,5]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson BOM is stripped",
			content: newZipBuilder().add(
				"locations.geojson",
				"\xef\xbb\xbf"+`{"type":"FeatureCollection","features":[{"type":"Feature","id":"z","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "z",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "locations.geojson unsupported geometry warns",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"pt","properties":{},`+
					`"geometry":{"type":"Point","coordinates":[0,0]}}]}`,
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
						LocationID: "pt",
						Reason:     `unsupported geometry type "Point"`,
					}),
				},
			},
		},
		{
			desc: "locations.geojson null geometry warns",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[`+
					`{"type":"Feature","id":"null_geom","properties":{},"geometry":null},`+
					`{"type":"Feature","id":"no_geom","properties":{}}]}`,
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
						LocationID: "null_geom",
						Reason:     "feature has no geometry",
					}),
					warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
						LocationID: "no_geom",
						Reason:     "feature has no geometry",
					}),
				},
			},
		},
		{
			desc: "locations.geojson polygon with no rings warns",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"empty","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[]}}]}`,
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
						LocationID: "empty",
						Reason:     "polygon has no rings",
					}),
				},
			},
		},
		{
			desc: "locations.geojson feature without id warns",
			content: newZipBuilder().add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
						Reason: "feature has no id",
					}),
				},
			},
		},
		{
			desc: "location.geojson (singular) is ignored",
			content: newZipBuilder().add(
				"location.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"z","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{},
		},
		{
			desc: "zero-byte locations.geojson is treated as absent",
			content: newZipBuilder().add(
				"locations.geojson", "",
			).build(),
			expected: &Static{},
		},
		{
			desc: "stops.txt is optional when locations.geojson is present",
			content: newZipBuilder().remove("stops.txt").add(
				"locations.geojson",
				`{"type":"FeatureCollection","features":[{"type":"Feature","id":"z","properties":{},`+
					`"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`,
			).build(),
			expected: &Static{
				Locations: []Location{
					{
						Id: "z",
						Geometry: LocationGeometry{
							Type:     "Polygon",
							Polygons: [][][][2]float64{{{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}},
							Raw:      json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`),
						},
					},
				},
			},
		},
		{
			desc: "header-only stops.txt parses to zero stops",
			content: newZipBuilder().add(
				"stops.txt", "stop_id,stop_name,stop_lat,stop_lon",
			).build(),
			expected: &Static{},
		},
```

Also add these two test functions after `TestParseStatic_UnknownTripIDIsSkipped`:

```go
func TestParseStatic_StopsFileRequiredWithoutLocations(t *testing.T) {
	content := newZipBuilder().remove("stops.txt").build()

	_, err := ParseStatic(content, ParseStaticOptions{})
	if err == nil {
		t.Fatal("ParseStatic() got nil error, want an error because stops.txt is missing")
	}
	if want := `no "stops.txt" file in GTFS static feed`; err.Error() != want {
		t.Errorf("ParseStatic() error = %q, want %q", err.Error(), want)
	}
}

func TestParseStatic_MalformedLocationsFileIsAnError(t *testing.T) {
	for _, tc := range []struct {
		desc    string
		content string
	}{
		{desc: "not json", content: "{"},
		{desc: "not a feature collection", content: `{"type":"Feature","features":[]}`},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			content := newZipBuilder().add("locations.geojson", tc.content).build()
			_, err := ParseStatic(content, ParseStaticOptions{})
			if err == nil {
				t.Fatal("ParseStatic() got nil error, want an error")
			}
			if !strings.HasPrefix(err.Error(), `failed to read "locations.geojson"`) {
				t.Errorf("ParseStatic() error = %q, want it to start with the file name", err.Error())
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestParse|TestParseStatic_StopsFileRequired|TestParseStatic_MalformedLocations' -v`
Expected: FAIL to compile — `undefined: Location`, `undefined: LocationGeometry`, `z.remove undefined`.

- [ ] **Step 3: Create `locations.go`**

```go
package gtfs

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/OneBusAway/go-gtfs/constants"
	"github.com/OneBusAway/go-gtfs/warnings"
)

// Location is a GeoJSON Feature from locations.geojson (Polygon or MultiPolygon only).
type Location struct {
	Id          string
	Name        string // properties.stop_name
	Description string // properties.stop_desc
	Geometry    LocationGeometry
}

// LocationGeometry normalises Polygon and MultiPolygon to one shape.
// Polygons[p][r][i] = [lon, lat]; ring 0 is the exterior, rings 1.. are holes.
type LocationGeometry struct {
	Type     string // "Polygon" | "MultiPolygon"
	Polygons [][][][2]float64
	Raw      json.RawMessage // the geometry object verbatim
}

// geoJSONFeatureCollection mirrors the subset of RFC 7946 that
// locations.geojson uses. Ids and properties stay raw because feeds disagree
// on their JSON types.
type geoJSONFeatureCollection struct {
	Type     string           `json:"type"`
	Features []geoJSONFeature `json:"features"`
}

type geoJSONFeature struct {
	ID         json.RawMessage            `json:"id"`
	Properties map[string]json.RawMessage `json:"properties"`
	Geometry   json.RawMessage            `json:"geometry"`
}

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// parseLocations decodes the content of locations.geojson. Malformed JSON or a
// top-level object that is not a FeatureCollection is an error; an unusable
// individual Feature is skipped with a LocationInvalidGeometry warning.
func parseLocations(content []byte) ([]Location, []warnings.StaticWarning, error) {
	var collection geoJSONFeatureCollection
	if err := json.Unmarshal(bytes.TrimPrefix(content, utf8BOM), &collection); err != nil {
		return nil, nil, err
	}
	if collection.Type != "FeatureCollection" {
		return nil, nil, fmt.Errorf("expected a FeatureCollection, got %q", collection.Type)
	}

	var locations []Location
	var w []warnings.StaticWarning
	for _, feature := range collection.Features {
		id := locationID(feature)
		if id == "" {
			w = append(w, warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
				Reason: "feature has no id",
			}))
			continue
		}
		geometry, err := parseLocationGeometry(feature.Geometry)
		if err != nil {
			w = append(w, warnings.NewFileWarning(constants.LocationsGeoJSONFile, warnings.LocationInvalidGeometry{
				LocationID: id,
				Reason:     err.Error(),
			}))
			continue
		}
		locations = append(locations, Location{
			Id:          id,
			Name:        jsonScalarString(feature.Properties["stop_name"]),
			Description: jsonScalarString(feature.Properties["stop_desc"]),
			Geometry:    geometry,
		})
	}
	return locations, w, nil
}

// locationID returns the Feature id, falling back to the draft-era
// properties.id and properties.location_id placements. A JSON number id is
// rendered with its literal text.
func locationID(feature geoJSONFeature) string {
	candidates := []json.RawMessage{
		feature.ID,
		feature.Properties["id"],
		feature.Properties["location_id"],
	}
	for _, raw := range candidates {
		if id := jsonScalarString(raw); id != "" {
			return id
		}
	}
	return ""
}

// jsonScalarString renders a JSON string or number as a Go string. Anything
// else (absent, null, bool, object, array) renders as "".
func jsonScalarString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// parseLocationGeometry decodes a Polygon or MultiPolygon geometry object.
func parseLocationGeometry(raw json.RawMessage) (LocationGeometry, error) {
	if len(raw) == 0 {
		return LocationGeometry{}, fmt.Errorf("feature has no geometry")
	}
	var geometry geoJSONGeometry
	if err := json.Unmarshal(raw, &geometry); err != nil {
		return LocationGeometry{}, err
	}
	var polygons [][][][]float64
	switch geometry.Type {
	case "":
		// `"geometry": null` decodes to the zero value.
		return LocationGeometry{}, fmt.Errorf("feature has no geometry")
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &rings); err != nil {
			return LocationGeometry{}, err
		}
		polygons = [][][][]float64{rings}
	case "MultiPolygon":
		if err := json.Unmarshal(geometry.Coordinates, &polygons); err != nil {
			return LocationGeometry{}, err
		}
	default:
		return LocationGeometry{}, fmt.Errorf("unsupported geometry type %q", geometry.Type)
	}
	normalised, err := normalisePolygons(polygons)
	if err != nil {
		return LocationGeometry{}, err
	}
	return LocationGeometry{Type: geometry.Type, Polygons: normalised, Raw: raw}, nil
}

// normalisePolygons truncates every position to [lon, lat] (RFC 7946 allows a
// third altitude element) and rejects empty polygons.
func normalisePolygons(polygons [][][][]float64) ([][][][2]float64, error) {
	if len(polygons) == 0 {
		return nil, fmt.Errorf("geometry has no polygons")
	}
	result := make([][][][2]float64, 0, len(polygons))
	for _, rings := range polygons {
		if len(rings) == 0 {
			return nil, fmt.Errorf("polygon has no rings")
		}
		polygon := make([][][2]float64, 0, len(rings))
		for _, ring := range rings {
			points := make([][2]float64, 0, len(ring))
			for _, position := range ring {
				if len(position) < 2 {
					return nil, fmt.Errorf("position %v has fewer than 2 coordinates", position)
				}
				points = append(points, [2]float64{position[0], position[1]})
			}
			polygon = append(polygon, points)
		}
		result = append(result, polygon)
	}
	return result, nil
}
```

- [ ] **Step 4: Wire it into `ParseStatic`**

In `static.go`, add `"io"` to the imports, add `Locations []Location` to `Static` directly above `BookingRules`:

```go
	// Locations is nil when locations.geojson is absent.
	Locations []Location
	// BookingRules is nil when booking_rules.txt is absent.
	BookingRules []BookingRule
```

In `ParseStatic`, immediately after the `fileNameToFile` loop and before `serviceIdToService := ...`, add:

```go
	locationsPresent, err := parseLocationsFile(fileNameToFile[constants.LocationsGeoJSONFile], result)
	if err != nil {
		return nil, err
	}
```

Change the `stops.txt` dispatch entry to:

```go
		{
			File: "stops.txt",
			Action: func(file *csv.File) (w []warnings.StaticWarning) {
				result.Stops = parseStops(file, opts.InheritWheelchairBoarding)
				return
			},
			// GTFS makes stops.txt optional when locations.geojson defines zones.
			Optional: locationsPresent,
		},
```

Add after `isAbsentOrEmpty`:

```go
// parseLocationsFile reads locations.geojson, when present and non-empty, into
// result. It runs before the CSV files because its presence decides whether
// stops.txt is required. Only the exact name locations.geojson is read.
func parseLocationsFile(zipFile *zip.File, result *Static) (present bool, err error) {
	if isAbsentOrEmpty(zipFile) {
		return false, nil
	}
	content, err := readZipFile(zipFile)
	if err != nil {
		return false, fmt.Errorf("failed to read %q: %w", constants.LocationsGeoJSONFile, err)
	}
	locations, w, err := parseLocations(content)
	if err != nil {
		return false, fmt.Errorf("failed to read %q: %w", constants.LocationsGeoJSONFile, err)
	}
	result.Locations = locations
	result.Warnings = append(result.Warnings, w...)
	return true, nil
}

func readZipFile(zipFile *zip.File) ([]byte, error) {
	reader, err := zipFile.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./...`
Expected: PASS. Note the `"stops.txt is optional when locations.geojson is present"` case exercises `remove("stops.txt")`; the `"header-only stops.txt"` case needs no code change and documents existing behaviour.

- [ ] **Step 6: Commit**

```bash
git add locations.go static.go static_test.go
git commit -m "Parse locations.geojson" -m "Read locations.geojson into Static.Locations before the CSV dispatch
loop, because GTFS makes stops.txt optional only when it defines zones.
Polygon and MultiPolygon are normalised to one [polygon][ring][point]
shape with the raw geometry kept verbatim; other geometry types, empty
polygons and id-less features are skipped with a warning. Feature ids
may be strings or numbers, and draft-era feeds that put the id under
properties are accepted. A leading UTF-8 BOM is stripped."
```

---

### Task 5: Parse `location_groups.txt` and `location_group_stops.txt`

**Files:**
- Modify: `static.go` (`Static` struct, new `LocationGroup` struct, two dispatch entries after `stops.txt`, `parseLocationGroups`, `parseLocationGroupStops`)
- Test: `static_test.go` (`TestParse` cases)

**Interfaces:**
- Consumes: `warnings.LocationGroupUnknownStop`, `constants.LocationGroupsFile`, `constants.LocationGroupStopsFile` (Task 2).
- Produces:
  ```go
  type LocationGroup struct {
      Id    string
      Name  string
      Stops []*Stop // resolved members; unknown stop ids are skipped with a warning
  }
  // on Static:
  LocationGroups []LocationGroup // nil when location_groups.txt absent or header-only
  func parseLocationGroups(csv *csv.File) ([]LocationGroup, []warnings.StaticWarning)
  func parseLocationGroupStops(csv *csv.File, groups []LocationGroup, stops []Stop) []warnings.StaticWarning
  ```

- [ ] **Step 1: Write the failing tests**

Add to the `TestParse` table (after the locations cases):

```go
		{
			desc: "location groups with resolved members",
			content: newZipBuilder().add(
				"stops.txt",
				"stop_id\nstop_id\nstop_2",
			).add(
				"location_groups.txt",
				"location_group_id,location_group_name",
				"g1,Group One",
				"g2,",
			).add(
				"location_group_stops.txt",
				"location_group_id,stop_id",
				"g1,stop_id",
				"g1,stop_2",
				"g2,stop_2",
			).build(),
			expected: &Static{
				Stops: []Stop{defaultStop, {Id: "stop_2"}},
				LocationGroups: []LocationGroup{
					{Id: "g1", Name: "Group One", Stops: []*Stop{&defaultStop, {Id: "stop_2"}}},
					{Id: "g2", Stops: []*Stop{{Id: "stop_2"}}},
				},
			},
		},
		{
			desc: "location group with unknown stop warns",
			content: newZipBuilder().add(
				"stops.txt",
				"stop_id\nstop_id",
			).add(
				"location_groups.txt",
				"location_group_id\ng1",
			).add(
				"location_group_stops.txt",
				"location_group_id,stop_id",
				"g1,stop_id",
				"g1,nope",
			).build(),
			expected: &Static{
				Stops: []Stop{defaultStop},
				LocationGroups: []LocationGroup{
					{Id: "g1", Stops: []*Stop{&defaultStop}},
				},
				Warnings: []warnings.StaticWarning{
					{
						Kind:          warnings.LocationGroupUnknownStop{GroupID: "g1", StopID: "nope"},
						File:          constants.LocationGroupStopsFile,
						RowNumber:     2,
						RowContent:    []string{"g1", "nope"},
						HeaderContent: []string{"location_group_id", "stop_id"},
					},
				},
			},
		},
		{
			desc: "membership rows for unknown groups are skipped",
			content: newZipBuilder().add(
				"stops.txt",
				"stop_id\nstop_id",
			).add(
				"location_group_stops.txt",
				"location_group_id,stop_id",
				"missing_group,stop_id",
			).build(),
			expected: &Static{
				Stops: []Stop{defaultStop},
			},
		},
		{
			desc: "header-only location group files yield nil",
			content: newZipBuilder().add(
				"location_groups.txt",
				"location_group_id,location_group_name",
			).add(
				"location_group_stops.txt",
				"location_group_id,stop_id",
			).build(),
			expected: &Static{},
		},
		{
			desc: "location groups file with missing columns",
			content: newZipBuilder().add(
				"location_groups.txt",
				"location_group_name\nGroup",
			).build(),
			expected: &Static{
				Warnings: []warnings.StaticWarning{
					{
						Kind:          warnings.MissingColumns{Columns: []string{"location_group_id"}},
						File:          constants.LocationGroupsFile,
						RowNumber:     0,
						RowContent:    []string{"location_group_name"},
						HeaderContent: []string{"location_group_name"},
					},
				},
			},
		},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestParse/location_group|TestParse/membership|TestParse/header-only_location' -v`
Expected: FAIL to compile — `undefined: LocationGroup`.

- [ ] **Step 3: Add the struct, dispatch entries and parsers**

In `static.go`, add to `Static` directly below `Locations`:

```go
	// LocationGroups is nil when location_groups.txt is absent.
	LocationGroups []LocationGroup
```

Add to `static.go` directly after the `BookingRule` struct (Task 3):

```go
// LocationGroup corresponds to a single row in the location_groups.txt file
// with its location_group_stops.txt members resolved.
type LocationGroup struct {
	Id    string
	Name  string
	Stops []*Stop // resolved members; unknown stop ids are skipped with a warning
}
```

Insert these two dispatch entries **immediately after the `stops.txt` entry** (before `calendar.txt`):

```go
		{
			File: constants.LocationGroupsFile,
			Action: func(file *csv.File) (w []warnings.StaticWarning) {
				result.LocationGroups, w = parseLocationGroups(file)
				return
			},
			Optional: true,
		},
		{
			File: constants.LocationGroupStopsFile,
			Action: func(file *csv.File) []warnings.StaticWarning {
				return parseLocationGroupStops(file, result.LocationGroups, result.Stops)
			},
			Optional: true,
		},
```

Add after `parseStops`:

```go
func parseLocationGroups(csv *csv.File) ([]LocationGroup, []warnings.StaticWarning) {
	idColumn := csv.RequiredColumn("location_group_id")
	nameColumn := csv.OptionalColumn("location_group_name")

	if w := checkForMissingColumns(csv); len(w) > 0 {
		return nil, w
	}

	var groups []LocationGroup
	for csv.NextRow() {
		id := idColumn.Read()
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			log.Printf("Skipping location group because of missing keys %s", missingKeys)
			continue
		}
		groups = append(groups, LocationGroup{Id: id, Name: nameColumn.Read()})
	}
	return groups, nil
}

// parseLocationGroupStops appends each row's stop to its group's Stops. A row
// naming an unknown group is logged and skipped (matching how transfers.txt
// handles dangling ids); a row naming an unknown stop raises a warning.
func parseLocationGroupStops(csv *csv.File, groups []LocationGroup, stops []Stop) []warnings.StaticWarning {
	groupIDColumn := csv.RequiredColumn("location_group_id")
	stopIDColumn := csv.RequiredColumn("stop_id")

	if w := checkForMissingColumns(csv); len(w) > 0 {
		return w
	}

	idToGroup := map[string]*LocationGroup{}
	for i := range groups {
		idToGroup[groups[i].Id] = &groups[i]
	}
	idToStop := map[string]*Stop{}
	for i := range stops {
		idToStop[stops[i].Id] = &stops[i]
	}

	var w []warnings.StaticWarning
	for csv.NextRow() {
		groupID := groupIDColumn.Read()
		stopID := stopIDColumn.Read()
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			log.Printf("Skipping location group stop because of missing keys %s", missingKeys)
			continue
		}
		group := idToGroup[groupID]
		if group == nil {
			log.Printf("Skipping location group stop because location group %q is unknown", groupID)
			continue
		}
		stop := idToStop[stopID]
		if stop == nil {
			w = append(w, warnings.NewStaticWarning(csv, warnings.LocationGroupUnknownStop{
				GroupID: groupID,
				StopID:  stopID,
			}))
			continue
		}
		group.Stops = append(group.Stops, stop)
	}
	return w
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add static.go static_test.go
git commit -m "Parse location groups and their stop members" -m "Read location_groups.txt into Static.LocationGroups and resolve
location_group_stops.txt rows to *Stop members so consumers get the
group's stops without a second lookup. Both files are optional and are
parsed right after stops.txt, which they depend on. A member row naming
an unknown stop raises LocationGroupUnknownStop; one naming an unknown
group is logged and skipped."
```

---

### Task 6: Read `safe_duration_factor`/`safe_duration_offset` from `trips.txt`

**Files:**
- Modify: `static.go` (`ScheduledTrip` struct, `parseScheduledTrips`)
- Test: `static_test.go` (`TestParse` case)

**Interfaces:**
- Produces on `ScheduledTrip`: `SafeDurationFactor *float64`, `SafeDurationOffset *float64` (nil when the column is absent or the cell is blank/unparsable).

- [ ] **Step 1: Write the failing test**

Add to the `TestParse` table (after the `"trip"` case and the Task 1 case):

```go
		{
			desc: "trip with safe duration",
			content: newZipBuilder().add(
				"agency.txt",
				"agency_id,agency_name,agency_url,agency_timezone\na,b,c,d",
			).add(
				"routes.txt",
				"route_id,route_type\nroute_id,3",
			).add(
				"calendar.txt",
				"service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n"+
					"service_id,0,0,0,0,0,0,0,20220504,20220507",
			).add(
				"trips.txt",
				"route_id,service_id,trip_id,safe_duration_factor,safe_duration_offset",
				"route_id,service_id,a,2,30",
				"route_id,service_id,b,,",
			).build(),
			expected: &Static{
				Agencies: []Agency{defaultAgency},
				Routes:   []Route{defaultRoute},
				Services: []Service{defaultService},
				Trips: []ScheduledTrip{
					{
						Route:              &defaultRoute,
						Service:            &defaultService,
						ID:                 "a",
						SafeDurationFactor: ptr(2.0),
						SafeDurationOffset: ptr(30.0),
					},
					{
						Route:   &defaultRoute,
						Service: &defaultService,
						ID:      "b",
					},
				},
			},
		},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'TestParse/trip_with_safe_duration' -v`
Expected: FAIL to compile — `unknown field SafeDurationFactor in struct literal`.

- [ ] **Step 3: Add the fields and columns**

Change `ScheduledTrip` to:

```go
type ScheduledTrip struct {
	Route                *Route
	Service              *Service
	ID                   string
	Headsign             string
	ShortName            string
	DirectionId          DirectionID
	BlockID              string
	WheelchairAccessible WheelchairBoarding
	BikesAllowed         BikesAllowed
	StopTimes            []ScheduledStopTime
	Shape                *Shape
	Frequencies          []Frequency
	// SafeDurationFactor and SafeDurationOffset (GTFS-Flex) scale the
	// scheduled travel time into a rider-facing upper bound. Nil when absent.
	SafeDurationFactor *float64
	SafeDurationOffset *float64
}
```

In `parseScheduledTrips`, add two column lookups after `shapeIDColumn`:

```go
	safeDurationFactorColumn := csv.OptionalColumn("safe_duration_factor")
	safeDurationOffsetColumn := csv.OptionalColumn("safe_duration_offset")
```

and two fields to the `trip := ScheduledTrip{...}` literal after `BikesAllowed`:

```go
			SafeDurationFactor:   parseFloat64(safeDurationFactorColumn.Read()),
			SafeDurationOffset:   parseFloat64(safeDurationOffsetColumn.Read()),
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add static.go static_test.go
git commit -m "Read safe_duration_* columns from trips.txt" -m "The adopted GTFS-Flex spec places safe_duration_factor and
safe_duration_offset on trips.txt. Expose them as optional pointers on
ScheduledTrip so consumers can compute the rider-facing travel time
bound."
```

---

### Task 7: Flex columns in `stop_times.txt` and timed-only interpolation

**Files:**
- Modify: `static.go` (`ScheduledStopTime` struct + methods, dispatch entry for `stop_times.txt`, rewrite of `parseScheduledStopTimes`, new helpers)
- Modify: `interpolate.go` (new `interpolateTimedStopTimes`)
- Create: `flex_test.go` (stop_times validation table)
- Test: `interpolate_test.go`, `static_test.go`

**Interfaces:**
- Consumes: `Location`, `LocationGroup`, `BookingRule` and their `Static` slices (Tasks 3–5); `warnings.StopTimeInvalidReference`, `warnings.StopTimeInvalidWindow` (Task 2); `interpolateStopTimes`, `interpolateStopTimesByShapeDist` (Task 1).
- Produces:
  ```go
  type ScheduledStopTime struct {
      // ...existing fields unchanged; Stop may now be nil...
      Location                 *Location
      LocationGroup            *LocationGroup
      StartPickupDropOffWindow *time.Duration
      EndPickupDropOffWindow   *time.Duration
      PickupBookingRule        *BookingRule
      DropOffBookingRule       *BookingRule
      // Draft-era placement tolerated for real feeds; adopted spec puts these on trips.
      SafeDurationFactor       *float64
      SafeDurationOffset       *float64
  }
  func (st ScheduledStopTime) IsWindowed() bool // both window pointers non-nil
  func (st ScheduledStopTime) IsFlex() bool     // windowed || Location != nil || LocationGroup != nil

  func parseScheduledStopTimes(csv *csv.File, static *Static) []warnings.StaticWarning
  type stopTimeReferences struct{ trips map[string]*ScheduledTrip; stops map[string]*Stop; locations map[string]*Location; groups map[string]*LocationGroup; bookingRules map[string]*BookingRule }
  func newStopTimeReferences(static *Static) stopTimeReferences
  type stopTimePlace struct{ stop *Stop; location *Location; group *LocationGroup }
  func (refs stopTimeReferences) resolvePlace(stopID, locationID, groupID string) (stopTimePlace, string)
  func (refs stopTimeReferences) resolveBookingRule(column, id string) (*BookingRule, string)
  func parsePickupDropOffWindow(startRaw, endRaw string) (start, end *time.Duration, reason string)
  func checkWindowUsage(windowed bool, place stopTimePlace, arrivalRaw, departureRaw string) string
  func parseArrivalDeparture(arrivalRaw, departureRaw string) (arrival, departure time.Duration)

  // interpolate.go
  func interpolateTimedStopTimes(stopTimes []ScheduledStopTime, byShapeDist bool) []ScheduledStopTime
  ```

#### 7a. Struct fields, `IsWindowed`/`IsFlex`, and `interpolateTimedStopTimes`

- [ ] **Step 1: Write the failing tests**

Add to `interpolate_test.go`:

```go
func TestScheduledStopTime_IsWindowedAndIsFlex(t *testing.T) {
	window := dur("08:00:00")
	for _, tc := range []struct {
		desc         string
		st           ScheduledStopTime
		wantWindowed bool
		wantFlex     bool
	}{
		{desc: "timed stop", st: ScheduledStopTime{Stop: &Stop{Id: "s"}}, wantWindowed: false, wantFlex: false},
		{desc: "windowed stop", st: ScheduledStopTime{Stop: &Stop{Id: "s"}, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window}, wantWindowed: true, wantFlex: true},
		{desc: "only start window", st: ScheduledStopTime{StartPickupDropOffWindow: &window}, wantWindowed: false, wantFlex: false},
		{desc: "location without windows", st: ScheduledStopTime{Location: &Location{Id: "l"}}, wantWindowed: false, wantFlex: true},
		{desc: "group without windows", st: ScheduledStopTime{LocationGroup: &LocationGroup{Id: "g"}}, wantWindowed: false, wantFlex: true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := tc.st.IsWindowed(); got != tc.wantWindowed {
				t.Errorf("IsWindowed() = %v, want %v", got, tc.wantWindowed)
			}
			if got := tc.st.IsFlex(); got != tc.wantFlex {
				t.Errorf("IsFlex() = %v, want %v", got, tc.wantFlex)
			}
		})
	}
}

func TestInterpolateTimedStopTimes_SkipsWindowedRecords(t *testing.T) {
	window := dur("08:00:00")
	st := []ScheduledStopTime{
		{StopSequence: 1, ArrivalTime: dur("08:00:00"), DepartureTime: dur("08:00:00"), ExactTimes: true},
		{StopSequence: 2, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window},
		{StopSequence: 3},
		{StopSequence: 4, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window},
		{StopSequence: 5, ArrivalTime: dur("08:40:00"), DepartureTime: dur("08:40:00"), ExactTimes: true},
	}
	got := interpolateTimedStopTimes(st, false)

	if len(got) != 5 {
		t.Fatalf("got %d records, want 5", len(got))
	}
	for i, want := range []int{1, 2, 3, 4, 5} {
		if got[i].StopSequence != want {
			t.Errorf("record %d has stop_sequence %d, want %d (stop_sequence order must be preserved)", i, got[i].StopSequence, want)
		}
	}
	if !almostEq(got[2].ArrivalTime, dur("08:20:00")) || !almostEq(got[2].DepartureTime, dur("08:20:00")) {
		t.Errorf("timed record 3 = %v/%v, want 08:20:00 (even interpolation over the timed records only)", got[2].ArrivalTime, got[2].DepartureTime)
	}
	for _, i := range []int{1, 3} {
		if got[i].ArrivalTime != 0 || got[i].DepartureTime != 0 {
			t.Errorf("windowed record %d got times %v/%v, want none", i+1, got[i].ArrivalTime, got[i].DepartureTime)
		}
		if !got[i].IsWindowed() {
			t.Errorf("windowed record %d lost its window", i+1)
		}
	}
}

func TestInterpolateTimedStopTimes_ByShapeDistanceIgnoresWindowedRecordWithoutDistance(t *testing.T) {
	// A windowed record has no shape_dist_traveled. Before partitioning, the
	// shape-distance interpolation dereferenced it and panicked.
	window := dur("08:00:00")
	st := []ScheduledStopTime{
		{StopSequence: 1, ArrivalTime: dur("08:00:00"), DepartureTime: dur("08:00:00"), ShapeDistanceTraveled: ptr(0.0)},
		{StopSequence: 2, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window},
		{StopSequence: 3, ShapeDistanceTraveled: ptr(7.5)},
		{StopSequence: 4, ArrivalTime: dur("08:40:00"), DepartureTime: dur("08:40:00"), ShapeDistanceTraveled: ptr(10.0)},
	}
	got := interpolateTimedStopTimes(st, true)
	if !almostEq(got[2].ArrivalTime, dur("08:30:00")) {
		t.Errorf("timed record 3 = %v, want 08:30:00 (distance-weighted: 7.5 of 10)", got[2].ArrivalTime)
	}
	if got[1].ArrivalTime != 0 {
		t.Errorf("windowed record got time %v, want none", got[1].ArrivalTime)
	}
}

func TestInterpolateTimedStopTimes_AllWindowedIsUnchanged(t *testing.T) {
	window := dur("08:00:00")
	st := []ScheduledStopTime{
		{StopSequence: 1, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window},
		{StopSequence: 2, StartPickupDropOffWindow: &window, EndPickupDropOffWindow: &window},
	}
	got := interpolateTimedStopTimes(st, true)
	if len(got) != 2 || got[0].StopSequence != 1 || got[1].StopSequence != 2 {
		t.Errorf("all-windowed trip changed: %+v", got)
	}
}

func TestInterpolateTimedStopTimes_EmptyIsNil(t *testing.T) {
	if got := interpolateTimedStopTimes(nil, false); got != nil {
		t.Errorf("got %v, want nil so cmp.Diff on Static keeps matching", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestScheduledStopTime_IsWindowed|TestInterpolateTimedStopTimes' -v`
Expected: FAIL to compile — `unknown field StartPickupDropOffWindow`, `undefined: interpolateTimedStopTimes`.

- [ ] **Step 3: Add the fields, methods and helper**

Change `ScheduledStopTime` in `static.go` to:

```go
type ScheduledStopTime struct {
	Trip                  *ScheduledTrip
	Stop                  *Stop // nil on GTFS-Flex rows that reference a Location or LocationGroup
	ArrivalTime           time.Duration
	DepartureTime         time.Duration
	StopSequence          int
	Headsign              string
	PickupType            PickupDropOffPolicy
	DropOffType           PickupDropOffPolicy
	ContinuousPickup      PickupDropOffPolicy
	ContinuousDropOff     PickupDropOffPolicy
	ShapeDistanceTraveled *float64
	ExactTimes            bool

	// GTFS-Flex. Exactly one of Stop, Location and LocationGroup is set.
	Location                 *Location
	LocationGroup            *LocationGroup
	StartPickupDropOffWindow *time.Duration
	EndPickupDropOffWindow   *time.Duration
	PickupBookingRule        *BookingRule
	DropOffBookingRule       *BookingRule
	// Draft-era placement tolerated for real feeds; the adopted spec puts
	// these on trips.txt (see ScheduledTrip).
	SafeDurationFactor *float64
	SafeDurationOffset *float64
}

// IsWindowed reports whether the record carries a pickup/drop-off window
// instead of arrival/departure times.
func (st ScheduledStopTime) IsWindowed() bool {
	return st.StartPickupDropOffWindow != nil && st.EndPickupDropOffWindow != nil
}

// IsFlex reports whether the record is an on-demand (GTFS-Flex) record.
func (st ScheduledStopTime) IsFlex() bool {
	return st.IsWindowed() || st.Location != nil || st.LocationGroup != nil
}
```

Append to `interpolate.go`:

```go
// interpolateTimedStopTimes fills in missing arrival/departure times on the
// timed records of a trip. Windowed records legitimately carry no times, so
// they are removed from the interpolation input (not merely skipped when
// writing) and put back in stop_sequence order afterwards. stopTimes must
// already be sorted by StopSequence.
func interpolateTimedStopTimes(stopTimes []ScheduledStopTime, byShapeDist bool) []ScheduledStopTime {
	if len(stopTimes) == 0 {
		return nil
	}
	var timed, windowed []ScheduledStopTime
	for _, stopTime := range stopTimes {
		if stopTime.IsWindowed() {
			windowed = append(windowed, stopTime)
		} else {
			timed = append(timed, stopTime)
		}
	}
	if len(timed) == 0 {
		return stopTimes
	}
	if byShapeDist {
		timed = interpolateStopTimesByShapeDist(timed)
	} else {
		timed = interpolateStopTimes(timed)
	}
	if len(windowed) == 0 {
		return timed
	}
	return mergeByStopSequence(timed, windowed)
}

// mergeByStopSequence merges two StopSequence-sorted slices into one.
func mergeByStopSequence(a, b []ScheduledStopTime) []ScheduledStopTime {
	merged := make([]ScheduledStopTime, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].StopSequence <= b[j].StopSequence {
			merged = append(merged, a[i])
			i++
		} else {
			merged = append(merged, b[j])
			j++
		}
	}
	merged = append(merged, a[i:]...)
	merged = append(merged, b[j:]...)
	return merged
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS (existing `TestParse` cases still pass: the new pointer fields are nil on every existing row).

- [ ] **Step 5: Commit**

```bash
git add static.go interpolate.go interpolate_test.go
git commit -m "Add GTFS-Flex fields to ScheduledStopTime" -m "Add the location, location group, pickup/drop-off window, booking rule
and draft-era safe duration fields, plus IsWindowed and IsFlex.

Add interpolateTimedStopTimes, which partitions a trip's records into
windowed and timed, interpolates the timed slice, and merges back in
stop_sequence order. Windowed records must leave the interpolation
input entirely: skipping writes would still fabricate times across
them, and the shape-distance variant would dereference their nil
shape_dist_traveled."
```

#### 7b. `parseScheduledStopTimes` flex columns and validation

- [ ] **Step 1: Write the failing tests**

Create `flex_test.go`:

```go
package gtfs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs/constants"
	"github.com/OneBusAway/go-gtfs/warnings"
	"github.com/google/go-cmp/cmp"
)

// Shared flex fixture: two stops, two zones, one group of both stops, one
// booking rule. Tests add the stop_times.txt they exercise.

const (
	flexZoneAGeometry = `{"type":"Polygon","coordinates":[[[-85.0,45.0],[-84.9,45.0],[-84.9,45.1],[-85.0,45.0]]]}`
	flexZoneBGeometry = `{"type":"MultiPolygon","coordinates":[[[[-86.0,44.0],[-85.9,44.0],[-85.9,44.1],[-86.0,44.0]]],[[[-86.5,44.5],[-86.4,44.5],[-86.4,44.6],[-86.5,44.5]]]]}`
	flexZonesGeoJSON  = `{"type":"FeatureCollection","features":[` +
		`{"type":"Feature","id":"zone_a","properties":{"stop_name":"Zone A"},"geometry":` + flexZoneAGeometry + `},` +
		`{"type":"Feature","id":"zone_b","properties":{"stop_name":"Zone B"},"geometry":` + flexZoneBGeometry + `}]}`

	flexStopTimesHeader = "trip_id,stop_sequence,stop_id,location_id,location_group_id," +
		"arrival_time,departure_time,start_pickup_drop_off_window,end_pickup_drop_off_window," +
		"pickup_type,drop_off_type,pickup_booking_rule_id,drop_off_booking_rule_id,timepoint"
)

func newFlexZipBuilder() *zipBuilder {
	return newZipBuilder().add(
		"agency.txt", "agency_id,agency_name,agency_url,agency_timezone\na,b,c,d",
	).add(
		"routes.txt", "route_id,route_type\nroute_id,3",
	).add(
		"stops.txt", "stop_id,stop_lat,stop_lon\nstop_1,45.0,-85.0\nstop_2,45.1,-85.1",
	).add(
		"calendar.txt",
		"service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n"+
			"service_id,0,0,0,0,0,0,0,20220504,20220507",
	).add(
		"trips.txt", "route_id,service_id,trip_id\nroute_id,service_id,trip_id",
	).add(
		"booking_rules.txt", "booking_rule_id,booking_type,prior_notice_duration_min\nbr_1,1,60",
	).add(
		"locations.geojson", flexZonesGeoJSON,
	).add(
		"location_groups.txt", "location_group_id,location_group_name\ngroup_1,Group One",
	).add(
		"location_group_stops.txt", "location_group_id,stop_id\ngroup_1,stop_1\ngroup_1,stop_2",
	)
}

func flexStop1() *Stop { return &Stop{Id: "stop_1", Latitude: ptr(45.0), Longitude: ptr(-85.0)} }
func flexStop2() *Stop { return &Stop{Id: "stop_2", Latitude: ptr(45.1), Longitude: ptr(-85.1)} }

func flexZoneA() *Location {
	return &Location{
		Id:   "zone_a",
		Name: "Zone A",
		Geometry: LocationGeometry{
			Type:     "Polygon",
			Polygons: [][][][2]float64{{{{-85.0, 45.0}, {-84.9, 45.0}, {-84.9, 45.1}, {-85.0, 45.0}}}},
			Raw:      json.RawMessage(flexZoneAGeometry),
		},
	}
}

func flexZoneB() *Location {
	return &Location{
		Id:   "zone_b",
		Name: "Zone B",
		Geometry: LocationGeometry{
			Type: "MultiPolygon",
			Polygons: [][][][2]float64{
				{{{-86.0, 44.0}, {-85.9, 44.0}, {-85.9, 44.1}, {-86.0, 44.0}}},
				{{{-86.5, 44.5}, {-86.4, 44.5}, {-86.4, 44.6}, {-86.5, 44.5}}},
			},
			Raw: json.RawMessage(flexZoneBGeometry),
		},
	}
}

func flexGroup1() *LocationGroup {
	return &LocationGroup{Id: "group_1", Name: "Group One", Stops: []*Stop{flexStop1(), flexStop2()}}
}

func flexBookingRule1() *BookingRule {
	return &BookingRule{Id: "br_1", Type: BookingType_SameDay, PriorNoticeDurationMin: ptr(int32(60))}
}

func cells(row string) []string { return strings.Split(row, ",") }

func hhmm(h, m int) *time.Duration {
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
	return &d
}

func TestParseStatic_FlexStopTimeValidation(t *testing.T) {
	// Every case has a single stop_times row so that the warning's RowContent
	// (which csv.File reuses across rows) is stable.
	for _, tc := range []struct {
		desc          string
		row           string
		wantStopTimes []ScheduledStopTime
		wantWarning   warnings.StaticWarningKind // nil means no warning
	}{
		{
			desc: "zone record",
			row:  "trip_id,1,,zone_a,,,,08:00:00,17:00:00,2,1,br_1,br_1,",
			wantStopTimes: []ScheduledStopTime{{
				Location:                 flexZoneA(),
				StopSequence:             1,
				StartPickupDropOffWindow: hhmm(8, 0),
				EndPickupDropOffWindow:   hhmm(17, 0),
				PickupType:               PickupDropOffPolicy_PhoneAgency,
				DropOffType:              PickupDropOffPolicy_No,
				ContinuousPickup:         PickupDropOffPolicy_No,
				ContinuousDropOff:        PickupDropOffPolicy_No,
				PickupBookingRule:        flexBookingRule1(),
				DropOffBookingRule:       flexBookingRule1(),
				ExactTimes:               false,
			}},
		},
		{
			desc: "group record",
			row:  "trip_id,1,,,group_1,,,08:00:00,17:00:00,1,2,,br_1,",
			wantStopTimes: []ScheduledStopTime{{
				LocationGroup:            flexGroup1(),
				StopSequence:             1,
				StartPickupDropOffWindow: hhmm(8, 0),
				EndPickupDropOffWindow:   hhmm(17, 0),
				PickupType:               PickupDropOffPolicy_No,
				DropOffType:              PickupDropOffPolicy_PhoneAgency,
				ContinuousPickup:         PickupDropOffPolicy_No,
				ContinuousDropOff:        PickupDropOffPolicy_No,
				DropOffBookingRule:       flexBookingRule1(),
			}},
		},
		{
			// Spec §9.1: a stop_id record may carry windows. timepoint=1 is
			// ignored because a windowed record has no exact time.
			desc: "windowed stop record ignores timepoint",
			row:  "trip_id,1,stop_1,,,,,08:00:00,17:00:00,2,2,,,1",
			wantStopTimes: []ScheduledStopTime{{
				Stop:                     flexStop1(),
				StopSequence:             1,
				StartPickupDropOffWindow: hhmm(8, 0),
				EndPickupDropOffWindow:   hhmm(17, 0),
				PickupType:               PickupDropOffPolicy_PhoneAgency,
				DropOffType:              PickupDropOffPolicy_PhoneAgency,
				ContinuousPickup:         PickupDropOffPolicy_No,
				ContinuousDropOff:        PickupDropOffPolicy_No,
				ExactTimes:               false,
			}},
		},
		{
			desc: "timed stop record is unchanged by the flex columns",
			row:  "trip_id,1,stop_1,,,08:00:00,08:00:00,,,,,,,1",
			wantStopTimes: []ScheduledStopTime{{
				Stop:              flexStop1(),
				StopSequence:      1,
				ArrivalTime:       8 * time.Hour,
				DepartureTime:     8 * time.Hour,
				ContinuousPickup:  PickupDropOffPolicy_No,
				ContinuousDropOff: PickupDropOffPolicy_No,
				ExactTimes:        true,
			}},
		},
		{
			desc:        "no stop, location or group",
			row:         "trip_id,1,,,,,,08:00:00,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: "row references 0 of stop_id, location_id and location_group_id; exactly one is required"},
		},
		{
			desc:        "both stop and location",
			row:         "trip_id,1,stop_1,zone_a,,,,08:00:00,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: "row references 2 of stop_id, location_id and location_group_id; exactly one is required"},
		},
		{
			desc:        "unknown stop",
			row:         "trip_id,1,stop_x,,,08:00:00,08:00:00,,,,,,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: `unknown stop_id "stop_x"`},
		},
		{
			desc:        "unknown location",
			row:         "trip_id,1,,zone_x,,,,08:00:00,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: `unknown location_id "zone_x"`},
		},
		{
			desc:        "unknown location group",
			row:         "trip_id,1,,,group_x,,,08:00:00,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: `unknown location_group_id "group_x"`},
		},
		{
			desc:        "only start window",
			row:         "trip_id,1,,zone_a,,,,08:00:00,,2,1,,,",
			wantWarning: warnings.StopTimeInvalidWindow{Reason: "start_pickup_drop_off_window and end_pickup_drop_off_window must be set together"},
		},
		{
			desc:        "only end window",
			row:         "trip_id,1,,zone_a,,,,,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidWindow{Reason: "start_pickup_drop_off_window and end_pickup_drop_off_window must be set together"},
		},
		{
			desc:        "unparsable window",
			row:         "trip_id,1,,zone_a,,,,soon,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidWindow{Reason: `unparsable pickup/drop-off window "soon"-"17:00:00"`},
		},
		{
			desc:        "location without windows",
			row:         "trip_id,1,,zone_a,,,,,,2,1,,,",
			wantWarning: warnings.StopTimeInvalidWindow{Reason: "location_id and location_group_id rows require start/end_pickup_drop_off_window"},
		},
		{
			desc:        "windows with arrival time",
			row:         "trip_id,1,,zone_a,,08:00:00,,08:00:00,17:00:00,2,1,,,",
			wantWarning: warnings.StopTimeInvalidWindow{Reason: "arrival_time/departure_time are forbidden on a row with a pickup/drop-off window"},
		},
		{
			desc:        "unknown pickup booking rule",
			row:         "trip_id,1,,zone_a,,,,08:00:00,17:00:00,2,1,br_x,,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: `unknown pickup_booking_rule_id "br_x"`},
		},
		{
			desc:        "unknown drop-off booking rule",
			row:         "trip_id,1,,zone_a,,,,08:00:00,17:00:00,2,1,,br_x,",
			wantWarning: warnings.StopTimeInvalidReference{Reason: `unknown drop_off_booking_rule_id "br_x"`},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			content := newFlexZipBuilder().add("stop_times.txt", flexStopTimesHeader, tc.row).build()

			static, err := ParseStatic(content, ParseStaticOptions{})
			if err != nil {
				t.Fatalf("ParseStatic() got error %v, want nil", err)
			}
			if len(static.Trips) != 1 {
				t.Fatalf("got %d trips, want 1", len(static.Trips))
			}
			if diff := cmp.Diff(static.Trips[0].StopTimes, tc.wantStopTimes); diff != "" {
				t.Errorf("StopTimes mismatch (-got +want):\n%s", diff)
			}

			var wantWarnings []warnings.StaticWarning
			if tc.wantWarning != nil {
				wantWarnings = []warnings.StaticWarning{{
					Kind:          tc.wantWarning,
					File:          constants.StopTimesFile,
					RowNumber:     1,
					RowContent:    cells(tc.row),
					HeaderContent: cells(flexStopTimesHeader),
				}}
			}
			if diff := cmp.Diff(static.Warnings, wantWarnings); diff != "" {
				t.Errorf("Warnings mismatch (-got +want):\n%s", diff)
			}
		})
	}
}

func TestParseStatic_FlexStopIDColumnAbsent(t *testing.T) {
	// A pure zone feed may omit the stop_id column entirely.
	content := newFlexZipBuilder().add(
		"stop_times.txt",
		"trip_id,stop_sequence,location_id,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type",
		"trip_id,1,zone_a,08:00:00,17:00:00,2,1",
		"trip_id,2,zone_a,08:00:00,17:00:00,1,2",
	).build()

	static, err := ParseStatic(content, ParseStaticOptions{})
	if err != nil {
		t.Fatalf("ParseStatic() got error %v, want nil", err)
	}
	if len(static.Warnings) != 0 {
		t.Errorf("got warnings %+v, want none", static.Warnings)
	}
	if got := len(static.Trips[0].StopTimes); got != 2 {
		t.Fatalf("got %d stop times, want 2", got)
	}
	if static.Trips[0].StopTimes[0].Location != static.Trips[0].StopTimes[1].Location {
		t.Error("both records must point at the same *Location")
	}
}

func TestParseStatic_DraftSafeDurationOnStopTimes(t *testing.T) {
	content := newFlexZipBuilder().add(
		"stop_times.txt",
		"trip_id,stop_sequence,location_id,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type,mean_duration_factor,mean_duration_offset,safe_duration_factor,safe_duration_offset",
		"trip_id,1,zone_a,08:00:00,17:00:00,2,1,1,0.0,1,0.0",
	).build()

	static, err := ParseStatic(content, ParseStaticOptions{})
	if err != nil {
		t.Fatalf("ParseStatic() got error %v, want nil", err)
	}
	stopTime := static.Trips[0].StopTimes[0]
	if stopTime.SafeDurationFactor == nil || *stopTime.SafeDurationFactor != 1.0 {
		t.Errorf("SafeDurationFactor = %v, want 1.0", stopTime.SafeDurationFactor)
	}
	if stopTime.SafeDurationOffset == nil || *stopTime.SafeDurationOffset != 0.0 {
		t.Errorf("SafeDurationOffset = %v, want 0.0", stopTime.SafeDurationOffset)
	}
	if static.Trips[0].SafeDurationFactor != nil {
		t.Error("trips.txt has no safe_duration_factor column; ScheduledTrip.SafeDurationFactor must stay nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run 'TestParseStatic_Flex|TestParseStatic_DraftSafeDuration' -v`
Expected: FAIL. The `"zone record"` case gets an empty `StopTimes` (the row has no `stop_id`, which is still a required column, so it is dropped at the missing-keys check) and no warnings; the validation cases get no warnings.

- [ ] **Step 3: Rewrite `parseScheduledStopTimes`**

Change the `stop_times.txt` dispatch entry in `ParseStatic` to:

```go
		{
			File: constants.StopTimesFile,
			Action: func(file *csv.File) []warnings.StaticWarning {
				return parseScheduledStopTimes(file, result)
			},
		},
```

Replace the whole of `parseScheduledStopTimes` with:

```go
// stopTimeReferences resolves the ids a stop_times.txt row may point at.
type stopTimeReferences struct {
	trips        map[string]*ScheduledTrip
	stops        map[string]*Stop
	locations    map[string]*Location
	groups       map[string]*LocationGroup
	bookingRules map[string]*BookingRule
}

func newStopTimeReferences(static *Static) stopTimeReferences {
	refs := stopTimeReferences{
		trips:        map[string]*ScheduledTrip{},
		stops:        map[string]*Stop{},
		locations:    map[string]*Location{},
		groups:       map[string]*LocationGroup{},
		bookingRules: map[string]*BookingRule{},
	}
	for i := range static.Trips {
		refs.trips[static.Trips[i].ID] = &static.Trips[i]
	}
	for i := range static.Stops {
		refs.stops[static.Stops[i].Id] = &static.Stops[i]
	}
	for i := range static.Locations {
		refs.locations[static.Locations[i].Id] = &static.Locations[i]
	}
	for i := range static.LocationGroups {
		refs.groups[static.LocationGroups[i].Id] = &static.LocationGroups[i]
	}
	for i := range static.BookingRules {
		refs.bookingRules[static.BookingRules[i].Id] = &static.BookingRules[i]
	}
	return refs
}

// stopTimePlace is the exactly-one-of stop / location / location group that a
// stop_times.txt row serves.
type stopTimePlace struct {
	stop     *Stop
	location *Location
	group    *LocationGroup
}

// resolvePlace enforces GTFS's exactly-one-of rule for stop_id, location_id
// and location_group_id and resolves the one id given. A non-empty reason
// means the row must be skipped.
func (refs stopTimeReferences) resolvePlace(stopID, locationID, groupID string) (stopTimePlace, string) {
	referenced := 0
	for _, id := range []string{stopID, locationID, groupID} {
		if id != "" {
			referenced++
		}
	}
	if referenced != 1 {
		return stopTimePlace{}, fmt.Sprintf(
			"row references %d of stop_id, location_id and location_group_id; exactly one is required", referenced)
	}
	var place stopTimePlace
	switch {
	case stopID != "":
		place.stop = refs.stops[stopID]
		if place.stop == nil {
			return stopTimePlace{}, fmt.Sprintf("unknown stop_id %q", stopID)
		}
	case locationID != "":
		place.location = refs.locations[locationID]
		if place.location == nil {
			return stopTimePlace{}, fmt.Sprintf("unknown location_id %q", locationID)
		}
	default:
		place.group = refs.groups[groupID]
		if place.group == nil {
			return stopTimePlace{}, fmt.Sprintf("unknown location_group_id %q", groupID)
		}
	}
	return place, ""
}

// resolveBookingRule resolves an optional booking rule id. A non-empty reason
// means the id was given but does not exist.
func (refs stopTimeReferences) resolveBookingRule(column, id string) (*BookingRule, string) {
	if id == "" {
		return nil, ""
	}
	rule := refs.bookingRules[id]
	if rule == nil {
		return nil, fmt.Sprintf("unknown %s %q", column, id)
	}
	return rule, ""
}

// parsePickupDropOffWindow parses the two window cells of a row. Both empty
// means "not windowed" (nil pointers, empty reason). Any other invalid
// combination returns the reason the row must be skipped.
func parsePickupDropOffWindow(startRaw, endRaw string) (start, end *time.Duration, reason string) {
	if startRaw == "" && endRaw == "" {
		return nil, nil, ""
	}
	if startRaw == "" || endRaw == "" {
		return nil, nil, "start_pickup_drop_off_window and end_pickup_drop_off_window must be set together"
	}
	startWindow, startOk := parseGtfsTimeToDuration(startRaw)
	endWindow, endOk := parseGtfsTimeToDuration(endRaw)
	if !startOk || !endOk {
		return nil, nil, fmt.Sprintf("unparsable pickup/drop-off window %q-%q", startRaw, endRaw)
	}
	return &startWindow, &endWindow, ""
}

// checkWindowUsage applies the GTFS presence rules that tie windows to the
// kind of place a row references and to arrival/departure times.
func checkWindowUsage(windowed bool, place stopTimePlace, arrivalRaw, departureRaw string) string {
	if windowed && (arrivalRaw != "" || departureRaw != "") {
		return "arrival_time/departure_time are forbidden on a row with a pickup/drop-off window"
	}
	if !windowed && place.stop == nil {
		return "location_id and location_group_id rows require start/end_pickup_drop_off_window"
	}
	return ""
}

// parseArrivalDeparture parses the two time cells, copying whichever is
// present into the one that is missing.
func parseArrivalDeparture(arrivalRaw, departureRaw string) (arrival, departure time.Duration) {
	arrival, arrivalOk := parseGtfsTimeToDuration(arrivalRaw)
	departure, departureOk := parseGtfsTimeToDuration(departureRaw)
	if !departureOk {
		departure = arrival
	}
	if !arrivalOk {
		arrival = departure
	}
	return arrival, departure
}

func parseScheduledStopTimes(csv *csv.File, static *Static) []warnings.StaticWarning {
	tripIDColumn := csv.RequiredColumn("trip_id")
	stopSequenceColumn := csv.RequiredColumn("stop_sequence")
	stopIDColumn := csv.OptionalColumn("stop_id")
	locationIDColumn := csv.OptionalColumn("location_id")
	locationGroupIDColumn := csv.OptionalColumn("location_group_id")
	arrivalTimeColumn := csv.OptionalColumn("arrival_time")
	departureTimeColumn := csv.OptionalColumn("departure_time")
	startWindowColumn := csv.OptionalColumn("start_pickup_drop_off_window")
	endWindowColumn := csv.OptionalColumn("end_pickup_drop_off_window")
	stopHeadsignColumn := csv.OptionalColumn("stop_headsign")
	pickupTypeColumn := csv.OptionalColumn("pickup_type")
	dropOffTypeColumn := csv.OptionalColumn("drop_off_type")
	continuousPickupColumn := csv.OptionalColumn("continuous_pickup")
	continuousDropOffColumn := csv.OptionalColumn("continuous_drop_off")
	shapeDistanceTraveledColumn := csv.OptionalColumn("shape_dist_traveled")
	timepointColumn := csv.OptionalColumn("timepoint")
	pickupBookingRuleIDColumn := csv.OptionalColumn("pickup_booking_rule_id")
	dropOffBookingRuleIDColumn := csv.OptionalColumn("drop_off_booking_rule_id")
	safeDurationFactorColumn := csv.OptionalColumn("safe_duration_factor")
	safeDurationOffsetColumn := csv.OptionalColumn("safe_duration_offset")
	if w := checkForMissingColumns(csv); len(w) > 0 {
		return w
	}

	refs := newStopTimeReferences(static)
	var w []warnings.StaticWarning
	var currentTrip *ScheduledTrip
	hasNonEmptyShapeDistRow := false
	for csv.NextRow() {
		tripID := tripIDColumn.Read()
		rawStopSequence := stopSequenceColumn.Read()
		if missingKeys := csv.MissingRowKeys(); len(missingKeys) > 0 {
			log.Printf("Skipping stop time because of missing keys %s", missingKeys)
			continue
		}
		stopSequence, err := strconv.Atoi(rawStopSequence)
		if err != nil {
			log.Printf("Skipping stop time because stop_sequence %q is not an integer", rawStopSequence)
			continue
		}
		trip := refs.trips[tripID]
		if trip == nil {
			log.Printf("Skipping stop time because trip %q is unknown", tripID)
			continue
		}

		place, reason := refs.resolvePlace(stopIDColumn.Read(), locationIDColumn.Read(), locationGroupIDColumn.Read())
		if reason != "" {
			w = append(w, warnings.NewStaticWarning(csv, warnings.StopTimeInvalidReference{Reason: reason}))
			continue
		}
		startWindow, endWindow, reason := parsePickupDropOffWindow(startWindowColumn.Read(), endWindowColumn.Read())
		if reason != "" {
			w = append(w, warnings.NewStaticWarning(csv, warnings.StopTimeInvalidWindow{Reason: reason}))
			continue
		}
		windowed := startWindow != nil
		arrivalRaw := arrivalTimeColumn.Read()
		departureRaw := departureTimeColumn.Read()
		if reason := checkWindowUsage(windowed, place, arrivalRaw, departureRaw); reason != "" {
			w = append(w, warnings.NewStaticWarning(csv, warnings.StopTimeInvalidWindow{Reason: reason}))
			continue
		}
		pickupRule, reason := refs.resolveBookingRule("pickup_booking_rule_id", pickupBookingRuleIDColumn.Read())
		if reason != "" {
			w = append(w, warnings.NewStaticWarning(csv, warnings.StopTimeInvalidReference{Reason: reason}))
			continue
		}
		dropOffRule, reason := refs.resolveBookingRule("drop_off_booking_rule_id", dropOffBookingRuleIDColumn.Read())
		if reason != "" {
			w = append(w, warnings.NewStaticWarning(csv, warnings.StopTimeInvalidReference{Reason: reason}))
			continue
		}

		arrival, departure := parseArrivalDeparture(arrivalRaw, departureRaw)
		if shapeDistanceTraveledColumn.Read() != "" {
			hasNonEmptyShapeDistRow = true
		}
		stopTime := ScheduledStopTime{
			Stop:                     place.stop,
			Location:                 place.location,
			LocationGroup:            place.group,
			Headsign:                 stopHeadsignColumn.Read(),
			ArrivalTime:              arrival,
			DepartureTime:            departure,
			StopSequence:             stopSequence,
			PickupType:               parsePickupDropOffPolicyOrYes(pickupTypeColumn.Read()),
			DropOffType:              parsePickupDropOffPolicyOrYes(dropOffTypeColumn.Read()),
			ContinuousPickup:         parsePickupDropOffPolicy(continuousPickupColumn.ReadOr("")),
			ContinuousDropOff:        parsePickupDropOffPolicy(continuousDropOffColumn.ReadOr("")),
			ShapeDistanceTraveled:    parseFloat64(shapeDistanceTraveledColumn.Read()),
			// A windowed record has no exact time whatever timepoint says.
			ExactTimes:               !windowed && timepointColumn.ReadOr("1") != "0",
			StartPickupDropOffWindow: startWindow,
			EndPickupDropOffWindow:   endWindow,
			PickupBookingRule:        pickupRule,
			DropOffBookingRule:       dropOffRule,
			SafeDurationFactor:       parseFloat64(safeDurationFactorColumn.Read()),
			SafeDurationOffset:       parseFloat64(safeDurationOffsetColumn.Read()),
		}

		if trip != currentTrip {
			// Presize the new trip's slice from the previous trip's length; trips
			// on the same route usually have similar stop counts.
			if currentTrip != nil && cap(trip.StopTimes) == 0 {
				trip.StopTimes = make([]ScheduledStopTime, 0, len(currentTrip.StopTimes))
			}
			currentTrip = trip
		}
		trip.StopTimes = append(trip.StopTimes, stopTime)
	}
	for _, trip := range refs.trips {
		sort.Slice(trip.StopTimes, func(i, j int) bool {
			return trip.StopTimes[i].StopSequence < trip.StopTimes[j].StopSequence
		})
		trip.StopTimes = interpolateTimedStopTimes(trip.StopTimes, hasNonEmptyShapeDistRow)
	}
	return w
}
```

Behaviour changes relative to the pre-task code, all intentional: `stop_id` is optional; an unknown `stop_id` now raises `StopTimeInvalidReference` instead of being silently dropped; a `stop_times.txt` missing `trip_id` or `stop_sequence` now raises `MissingColumns` (the `parseAgencies` pattern) instead of printing to stdout.

- [ ] **Step 4: Run the tests**

Run: `go test ./...`
Expected: PASS, including every pre-existing `TestParse` case (none of them has an unknown `stop_id` or a `stop_times.txt` missing `trip_id`/`stop_sequence`).

- [ ] **Step 5: Commit**

```bash
git add static.go flex_test.go
git commit -m "Parse GTFS-Flex columns in stop_times.txt" -m "Make stop_id optional and read location_id, location_group_id, the
pickup/drop-off window pair, the booking rule ids and the draft-era
safe_duration_* columns. A row must reference exactly one of stop,
location and location group; a location or group row must carry both
window ends; windows and arrival/departure times are mutually
exclusive; every referenced id must resolve. Rows breaking a rule are
skipped with a StopTimeInvalidReference or StopTimeInvalidWindow
warning rather than failing the parse, matching how the rest of the
parser treats malformed rows.

Windowed rows never have exact times, so ExactTimes is false on them
regardless of timepoint. Interpolation now runs through
interpolateTimedStopTimes so windowed rows are left alone."
```

---

### Task 8: End-to-end flex feed fixtures

**Files:**
- Modify: `flex_test.go` (append `TestParseStatic_FlexFeeds` and the Alexandria excerpt test)

**Interfaces:**
- Consumes: everything from Tasks 3–7; test helpers `newFlexZipBuilder`, `flexZoneA`, `flexZoneB`, `flexGroup1`, `flexBookingRule1`, `flexStop1`, `flexStop2`, `hhmm` from Task 7.
- Produces: nothing new in non-test code.

- [ ] **Step 1: Write the failing tests**

Append to `flex_test.go`:

```go
// The four gtfs.org GTFS-Flex patterns, reduced to one trip each.
func TestParseStatic_FlexFeeds(t *testing.T) {
	t.Run("Heartland-style single zone", func(t *testing.T) {
		content := newFlexZipBuilder().add(
			"stop_times.txt",
			"trip_id,stop_sequence,location_id,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type,pickup_booking_rule_id,drop_off_booking_rule_id",
			"trip_id,1,zone_a,08:00:00,17:00:00,2,1,br_1,br_1",
			"trip_id,2,zone_a,08:00:00,17:00:00,1,2,br_1,br_1",
		).build()

		static := parseFlexFeed(t, content)
		want := []ScheduledStopTime{
			{
				Location: flexZoneA(), StopSequence: 1,
				StartPickupDropOffWindow: hhmm(8, 0), EndPickupDropOffWindow: hhmm(17, 0),
				PickupType: PickupDropOffPolicy_PhoneAgency, DropOffType: PickupDropOffPolicy_No,
				ContinuousPickup: PickupDropOffPolicy_No, ContinuousDropOff: PickupDropOffPolicy_No,
				PickupBookingRule: flexBookingRule1(), DropOffBookingRule: flexBookingRule1(),
			},
			{
				Location: flexZoneA(), StopSequence: 2,
				StartPickupDropOffWindow: hhmm(8, 0), EndPickupDropOffWindow: hhmm(17, 0),
				PickupType: PickupDropOffPolicy_No, DropOffType: PickupDropOffPolicy_PhoneAgency,
				ContinuousPickup: PickupDropOffPolicy_No, ContinuousDropOff: PickupDropOffPolicy_No,
				PickupBookingRule: flexBookingRule1(), DropOffBookingRule: flexBookingRule1(),
			},
		}
		if diff := cmp.Diff(static.Trips[0].StopTimes, want); diff != "" {
			t.Errorf("StopTimes mismatch (-got +want):\n%s", diff)
		}
		stopTimes := static.Trips[0].StopTimes
		if stopTimes[0].Location != stopTimes[1].Location || stopTimes[0].Location != &static.Locations[0] {
			t.Error("records must share the *Location stored in Static.Locations")
		}
		if stopTimes[0].PickupBookingRule != &static.BookingRules[0] {
			t.Error("records must point at the *BookingRule stored in Static.BookingRules")
		}
		for _, st := range stopTimes {
			if !st.IsWindowed() || !st.IsFlex() {
				t.Errorf("record %d: IsWindowed()=%v IsFlex()=%v, want true/true", st.StopSequence, st.IsWindowed(), st.IsFlex())
			}
		}
	})

	t.Run("zone to zone", func(t *testing.T) {
		content := newFlexZipBuilder().add(
			"stop_times.txt",
			"trip_id,stop_sequence,location_id,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type",
			"trip_id,1,zone_a,06:00:00,18:00:00,2,1",
			"trip_id,2,zone_b,06:00:00,18:00:00,1,2",
		).build()

		static := parseFlexFeed(t, content)
		stopTimes := static.Trips[0].StopTimes
		if len(stopTimes) != 2 {
			t.Fatalf("got %d stop times, want 2", len(stopTimes))
		}
		if diff := cmp.Diff(stopTimes[0].Location, flexZoneA()); diff != "" {
			t.Errorf("record 1 location (-got +want):\n%s", diff)
		}
		if diff := cmp.Diff(stopTimes[1].Location, flexZoneB()); diff != "" {
			t.Errorf("record 2 location (-got +want):\n%s", diff)
		}
		if len(stopTimes[1].Location.Geometry.Polygons) != 2 {
			t.Errorf("zone_b is a MultiPolygon with 2 polygons, got %d", len(stopTimes[1].Location.Geometry.Polygons))
		}
	})

	t.Run("RufBus-style location group without pickup_type columns", func(t *testing.T) {
		// gtfs.org's group example omits pickup_type/drop_off_type; the blank
		// default is 0 (regularly scheduled), which maglev's rule compilation
		// depends on.
		content := newFlexZipBuilder().add(
			"stops.txt", "stop_id,stop_lat,stop_lon\nstop_1,45.0,-85.0\nstop_2,45.1,-85.1\nstop_3,45.2,-85.2",
		).add(
			"location_group_stops.txt", "location_group_id,stop_id\ngroup_1,stop_1\ngroup_1,stop_2\ngroup_1,stop_3",
		).add(
			"stop_times.txt",
			"trip_id,stop_sequence,location_group_id,start_pickup_drop_off_window,end_pickup_drop_off_window",
			"trip_id,1,group_1,09:00:00,15:00:00",
			"trip_id,2,group_1,09:00:00,15:00:00",
		).build()

		static := parseFlexFeed(t, content)
		if len(static.LocationGroups) != 1 || len(static.LocationGroups[0].Stops) != 3 {
			t.Fatalf("got groups %+v, want one group of 3 stops", static.LocationGroups)
		}
		stopTimes := static.Trips[0].StopTimes
		if len(stopTimes) != 2 {
			t.Fatalf("got %d stop times, want 2", len(stopTimes))
		}
		for _, st := range stopTimes {
			if st.LocationGroup != &static.LocationGroups[0] {
				t.Errorf("record %d must point at Static.LocationGroups[0]", st.StopSequence)
			}
			if st.PickupType != PickupDropOffPolicy_Yes || st.DropOffType != PickupDropOffPolicy_Yes {
				t.Errorf("record %d pickup/drop-off = %v/%v, want ALLOWED/ALLOWED", st.StopSequence, st.PickupType, st.DropOffType)
			}
			if st.Stop != nil || st.Location != nil {
				t.Errorf("record %d must reference only the group", st.StopSequence)
			}
		}
	})

	t.Run("Hermann-style deviated route", func(t *testing.T) {
		// timed stop, zone, timed stop (untimed, to be interpolated), zone, timed stop.
		content := newFlexZipBuilder().add(
			"stop_times.txt",
			"trip_id,stop_sequence,stop_id,location_id,arrival_time,departure_time,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type,timepoint",
			"trip_id,1,stop_1,,08:00:00,08:00:00,,,0,0,1",
			"trip_id,2,,zone_a,,,08:00:00,08:40:00,1,3,",
			"trip_id,3,stop_2,,,,,,0,0,0",
			"trip_id,4,,zone_b,,,08:00:00,08:40:00,2,1,",
			"trip_id,5,stop_1,,08:40:00,08:40:00,,,0,0,1",
		).build()

		static := parseFlexFeed(t, content)
		stopTimes := static.Trips[0].StopTimes
		if len(stopTimes) != 5 {
			t.Fatalf("got %d stop times, want 5", len(stopTimes))
		}
		for i, want := range []int{1, 2, 3, 4, 5} {
			if stopTimes[i].StopSequence != want {
				t.Errorf("record %d has stop_sequence %d, want %d", i, stopTimes[i].StopSequence, want)
			}
		}
		// Windowed records untouched by interpolation.
		for _, i := range []int{1, 3} {
			st := stopTimes[i]
			if st.ArrivalTime != 0 || st.DepartureTime != 0 {
				t.Errorf("zone record %d got times %v/%v, want none", st.StopSequence, st.ArrivalTime, st.DepartureTime)
			}
			if !st.IsWindowed() || st.ExactTimes {
				t.Errorf("zone record %d: IsWindowed()=%v ExactTimes=%v, want true/false", st.StopSequence, st.IsWindowed(), st.ExactTimes)
			}
		}
		if stopTimes[1].PickupType != PickupDropOffPolicy_No || stopTimes[1].DropOffType != PickupDropOffPolicy_CoordinateWithDriver {
			t.Errorf("zone record 2 pickup/drop-off = %v/%v, want NOT_ALLOWED/COORDINATE_WITH_DRIVER", stopTimes[1].PickupType, stopTimes[1].DropOffType)
		}
		// The untimed timed stop is interpolated between its timed neighbours only.
		if !almostEq(stopTimes[2].ArrivalTime, dur("08:20:00")) || !almostEq(stopTimes[2].DepartureTime, dur("08:20:00")) {
			t.Errorf("stop record 3 = %v/%v, want 08:20:00 (midpoint of 08:00 and 08:40)", stopTimes[2].ArrivalTime, stopTimes[2].DepartureTime)
		}
		if stopTimes[2].ExactTimes {
			t.Error("stop record 3 has timepoint=0 and must not be exact")
		}
		if stopTimes[2].IsFlex() {
			t.Error("stop record 3 is a plain timed stop and must not be flex")
		}
	})
}

func parseFlexFeed(t *testing.T, content []byte) *Static {
	t.Helper()
	static, err := ParseStatic(content, ParseStaticOptions{})
	if err != nil {
		t.Fatalf("ParseStatic() got error %v, want nil", err)
	}
	if len(static.Warnings) != 0 {
		t.Fatalf("got warnings %+v, want none", static.Warnings)
	}
	if len(static.Trips) != 1 {
		t.Fatalf("got %d trips, want 1", len(static.Trips))
	}
	return static
}

// Reduced from the real Alexandria (Trillium) feed: draft-era columns on
// stop_times.txt, header-only location group files, a type 2 booking rule
// with prior_notice_start_*.
func TestParseStatic_AlexandriaExcerpt(t *testing.T) {
	content := newZipBuilder().add(
		"agency.txt", "agency_id,agency_name,agency_url,agency_timezone\n5088,DOT,https://www.alexandriava.gov,America/Los_Angeles",
	).add(
		"routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n77652,5088,,DOT Paratransit,3",
	).add(
		"stops.txt",
		"stop_id,stop_code,platform_code,stop_name,stop_desc,stop_lat,stop_lon,zone_id,stop_url,location_type,parent_station,stop_timezone,position,direction,wheelchair_boarding,tts_stop_name",
		`4258639,,,"Alexandria, VA, USA",,38.836368,-77.049221,,,0,,America/New_York,,,0,`,
	).add(
		"calendar.txt",
		"service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date",
		"c_71675_b_85952_d_63,1,1,1,1,1,1,0,20260101,20261231",
		"c_71675_b_85952_d_64,0,0,0,0,0,0,1,20260101,20261231",
	).add(
		"trips.txt",
		"route_id,service_id,trip_id,trip_short_name,trip_headsign,direction_id,block_id,shape_id,bikes_allowed,wheelchair_accessible,trip_type,continuous_pickup_message,continuous_drop_off_message,tts_trip_headsign,tts_trip_short_name",
		"77652,c_71675_b_85952_d_63,t_6124961_b_85952_tn_0,,,0,,,,,,,,,",
		"77652,c_71675_b_85952_d_64,t_6124409_b_85952_tn_0,,,0,,,,,,,,,",
	).add(
		"stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,location_id,location_group_id,stop_sequence,stop_headsign,pickup_type,drop_off_type,shape_dist_traveled,timepoint,continuous_pickup,continuous_drop_off,pickup_booking_rule_id,drop_off_booking_rule_id,start_pickup_drop_off_window,end_pickup_drop_off_window,mean_duration_factor,mean_duration_offset,safe_duration_factor,safe_duration_offset,tts_stop_headsign",
		"t_6124409_b_85952_tn_0,,,,area_1449,,1,,2,1,,0,1,1,booking_route_77652,booking_route_77652,07:00:00,24:50:00,1,0.0,1,0.0,",
		"t_6124409_b_85952_tn_0,,,,area_1449,,2,,1,2,,0,1,1,booking_route_77652,booking_route_77652,07:00:00,25:00:00,1,0.0,1,0.0,",
		"t_6124961_b_85952_tn_0,,,,area_1449,,1,,2,1,,0,1,1,booking_route_77652,booking_route_77652,05:00:00,24:50:00,1,0.0,1,0.0,",
		"t_6124961_b_85952_tn_0,,,,area_1449,,2,,1,2,,0,1,1,booking_route_77652,booking_route_77652,05:00:00,25:00:00,1,0.0,1,0.0,",
	).add(
		"booking_rules.txt",
		"booking_rule_id,booking_type,prior_notice_duration_min,prior_notice_duration_max,prior_notice_start_day,prior_notice_start_time,prior_notice_last_day,prior_notice_last_time,prior_notice_service_id,message,pickup_message,drop_off_message,phone_number,info_url,booking_url",
		`booking_route_77652,2,,,14,00:00:00,1,17:00:00,,"DOT is the City of Alexandria's paratransit program, call 703.746.5222",,,703-746-5222,https://www.alexandriava.gov/Paratransit,https://example.com/book`,
	).add(
		"location_groups.txt", "location_group_id,location_group_name",
	).add(
		"location_group_stops.txt", "location_group_id,stop_id",
	).add(
		"locations.geojson",
		`{"type":"FeatureCollection","features":[{"id":"area_1449","type":"Feature","geometry":{"type":"Polygon","coordinates":[[[-77.0464775,38.8762916],[-77.0449561,38.8765842],[-77.0403763,38.8723894],[-77.0464775,38.8762916]]]},"properties":{}}]}`,
	).build()

	static, err := ParseStatic(content, ParseStaticOptions{})
	if err != nil {
		t.Fatalf("ParseStatic() got error %v, want nil", err)
	}
	if len(static.Warnings) != 0 {
		t.Fatalf("got warnings %+v, want none", static.Warnings)
	}
	if static.LocationGroups != nil {
		t.Errorf("header-only location_groups.txt must yield nil, got %+v", static.LocationGroups)
	}
	if len(static.Locations) != 1 || static.Locations[0].Id != "area_1449" {
		t.Fatalf("got locations %+v, want area_1449 only", static.Locations)
	}
	if len(static.Stops) != 1 || static.Stops[0].Id != "4258639" {
		t.Fatalf("got stops %+v, want 4258639 only", static.Stops)
	}

	wantRule := BookingRule{
		Id:                   "booking_route_77652",
		Type:                 BookingType_PriorDays,
		PriorNoticeStartDay:  ptr(int32(14)),
		PriorNoticeStartTime: ptr(time.Duration(0)),
		PriorNoticeLastDay:   ptr(int32(1)),
		PriorNoticeLastTime:  ptr(17 * time.Hour),
		Message:              "DOT is the City of Alexandria's paratransit program, call 703.746.5222",
		PhoneNumber:          "703-746-5222",
		InfoUrl:              "https://www.alexandriava.gov/Paratransit",
		BookingUrl:           "https://example.com/book",
	}
	if diff := cmp.Diff(static.BookingRules, []BookingRule{wantRule}); diff != "" {
		t.Errorf("BookingRules mismatch (-got +want):\n%s", diff)
	}

	if len(static.Trips) != 2 {
		t.Fatalf("got %d trips, want 2", len(static.Trips))
	}
	for _, trip := range static.Trips {
		if trip.SafeDurationFactor != nil || trip.SafeDurationOffset != nil {
			t.Errorf("trip %s: trips.txt carries no safe_duration_* columns, want nil", trip.ID)
		}
		if len(trip.StopTimes) != 2 {
			t.Fatalf("trip %s: got %d stop times, want 2", trip.ID, len(trip.StopTimes))
		}
		for _, st := range trip.StopTimes {
			if st.Location != &static.Locations[0] {
				t.Errorf("trip %s seq %d: Location must be area_1449", trip.ID, st.StopSequence)
			}
			if st.PickupBookingRule != &static.BookingRules[0] || st.DropOffBookingRule != &static.BookingRules[0] {
				t.Errorf("trip %s seq %d: booking rules must resolve to booking_route_77652", trip.ID, st.StopSequence)
			}
			if st.SafeDurationFactor == nil || *st.SafeDurationFactor != 1.0 || st.SafeDurationOffset == nil || *st.SafeDurationOffset != 0.0 {
				t.Errorf("trip %s seq %d: draft-era safe duration = %v/%v, want 1.0/0.0", trip.ID, st.StopSequence, st.SafeDurationFactor, st.SafeDurationOffset)
			}
			if st.ExactTimes || !st.IsWindowed() {
				t.Errorf("trip %s seq %d: ExactTimes=%v IsWindowed()=%v, want false/true", trip.ID, st.StopSequence, st.ExactTimes, st.IsWindowed())
			}
		}
	}
	// trips.txt file order: t_6124961 (Mon–Sat, 05:00–24:50 then 05:00–25:00)
	// first, then t_6124409 (Sun, 07:00–24:50 then 07:00–25:00).
	weekdays, sunday := static.Trips[0], static.Trips[1]
	if diff := cmp.Diff(weekdays.StopTimes[0].StartPickupDropOffWindow, hhmm(5, 0)); diff != "" {
		t.Errorf("weekday start window (-got +want):\n%s", diff)
	}
	if diff := cmp.Diff(weekdays.StopTimes[0].EndPickupDropOffWindow, hhmm(24, 50)); diff != "" {
		t.Errorf("weekday end window (-got +want):\n%s", diff)
	}
	if diff := cmp.Diff(weekdays.StopTimes[1].EndPickupDropOffWindow, hhmm(25, 0)); diff != "" {
		t.Errorf("weekday drop-off end window (-got +want):\n%s", diff)
	}
	if diff := cmp.Diff(sunday.StopTimes[0].StartPickupDropOffWindow, hhmm(7, 0)); diff != "" {
		t.Errorf("sunday start window (-got +want):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run tests to verify they pass (this task adds coverage, not behaviour)**

Run: `go test . -run 'TestParseStatic_FlexFeeds|TestParseStatic_AlexandriaExcerpt' -v`
Expected: PASS. If any subtest fails, the failure is a real defect in Tasks 3–7; fix it there (with the failing subtest as the regression test) rather than adjusting the expectation. Two things to double-check on a failure: `trips.txt` row order determines `static.Trips` order (the Mon–Sat trip `t_6124961` is first, as in the real feed), and `24:50:00` parses to `24h50m` because `parseGtfsTimeToDuration` accepts hours ≥ 24.

- [ ] **Step 3: Run the whole suite and formatting checks**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l .` prints nothing; vet and tests PASS.

- [ ] **Step 4: Commit**

```bash
git add flex_test.go
git commit -m "Add end-to-end GTFS-Flex parser fixtures" -m "Cover the four gtfs.org GTFS-Flex patterns (single zone, zone to zone,
location group with pickup_type columns omitted, deviated route with
interpolation) and a reduced Alexandria feed with draft-era columns and
header-only location group files, asserting that the records share the
pointers stored on Static and that interpolation leaves windowed rows
alone."
```

---

### Task 9: README support table and final verification

**Files:**
- Modify: `README.md:55,73,79,80,81`

**Interfaces:** none.

- [ ] **Step 1: Update the five table rows**

In `README.md`, change (pad each cell with spaces to the width of the dash row beneath the header so the table still aligns):

Line 55, `stops.txt`: change the `Notes` cell from `Always required by library` to `Optional only when locations.geojson is present`.

Line 73, `location_groups.txt`: change `❌` to `✅`.

Line 79, `location_group_stops.txt`: change `❌` to `✅`.

Line 80, `locations.geojson`: change `❌` to `✅` and set the `Notes` cell to `Polygon and MultiPolygon only`.

Line 81, `booking_rules.txt`: change `❌` to `✅`.

Verify with: `grep -n 'stops.txt\|location_groups.txt\|location_group_stops.txt\|locations.geojson\|booking_rules.txt' README.md` — all five rows show `✅`.

- [ ] **Step 2: Go 1.18 compatibility sweep**

Run:

```bash
grep -n '"slices"\|"maps"\|errors\.Join\|strings\.CutPrefix\|strings\.CutSuffix\|\bmin(\|\bmax(\|\bclear(' *.go csv/*.go warnings/*.go constants/*.go
```

Expected: no output. (`io.ReadAll`, `bytes.TrimPrefix`, `json.Number`, generics and `any` are all available in Go 1.18.)

- [ ] **Step 3: Final verification**

Run: `gofmt -l . && go vet ./... && go test ./... -count=1`
Expected: `gofmt -l .` prints nothing; vet and tests PASS.

Run: `git status --short`
Expected: only `README.md` modified (and the untracked `docs/superpowers/plans/` directory, which is not committed).

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Mark GTFS-Flex files as supported in README" -m "locations.geojson, location_groups.txt, location_group_stops.txt and
booking_rules.txt are now parsed, and stops.txt is optional when
locations.geojson is present."
```

- [ ] **Step 5: Review the branch**

Run: `git log --oneline origin/main..HEAD`
Expected (newest first):

```
Mark GTFS-Flex files as supported in README
Add end-to-end GTFS-Flex parser fixtures
Parse GTFS-Flex columns in stop_times.txt
Add GTFS-Flex fields to ScheduledStopTime
Read safe_duration_* columns from trips.txt
Parse location groups and their stop members
Parse locations.geojson
Treat zero-byte optional files as absent
Parse booking_rules.txt
Add GTFS-Flex warning kinds
Fall back to even interpolation on nil distance
Skip stop_times rows for unknown trip ids
Fix swapped arrival/departure fallback
```

Do not push; the spec's §0 sequencing (push `gtfs-flex` so maglev can `go get` the pseudo-version) is a decision for the person running the plan.

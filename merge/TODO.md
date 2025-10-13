# GTFS Merge Module - Remaining Tasks

## *Important* how to work on each feature

 Your Standard Feature Implementation Request Pattern:

### 1. Comprehensive Planning Phase

- "Come up with a comprehensive plan..."
- The plan must include:
  - RED-GREEN-REFACTOR TDD cycles broken down step-by-step
  - Subagent code review after implementation
  - Quality checks (lint/fmt/vet/test) before commit
  - Time estimates for each cycle
  - Clear success criteria

### 2. Cycle-by-Cycle Execution

- "Yes, start with Cycle 1"
- Each cycle follows strict RED → GREEN pattern:
  - RED: Write failing test first (confirm it fails)
  - GREEN: Implement minimal code to make it pass
  - REFACTOR: (implicit) Clean up if needed
- You approve continuation cycle-by-cycle or let me proceed

### 3. Code Review Gate

- "Ask a subagent for a review on the latest changes"
- Subagent reviews for:
  - Correctness
  - Completeness
  - Pattern consistency
  - Missing edge cases
  - Priority-ranked issues (high/medium/low)

### 4. Quality Gate

- "Run lint fmt vet test"
- "Make sure they all pass"
- Must pass ALL checks before commit:
  - make lint
  - make fmt
  - go vet ./...
  - make test (all tests passing)
  - Restart this series of checks if any tests require fixing

### 5. Commit

- "commit it"
- Structured commit message with:
  - Feature description
  - Bullet points of changes
  - Coverage stats
  - *Important* absolutely not any Claude Code + Happy credits (Co-authored-by tags)

### Example Flow (Service Scorer):

1. ✅ "Do Service calendar merging. Make a comprehensive plan in keeping with the others"
2. ✅ I present plan with Cycles 1-7, subagent review, quality checks
3. ✅ "Please continue with plan"
4. ✅ Execute Cycles 1-7 (RED → GREEN for each)
5. ✅ Subagent review (found no blocking issues)
6. ✅ Quality checks (lint/fmt/vet/test - all passed)
7. ✅ "commit it" (with proper message format)

This pattern ensures high quality, tested, reviewed code at every step.

## Status Overview

### ✅ Completed
- Core merge orchestrator with dependency-order processing
- IDENTITY strategy (same ID = duplicate)
- FUZZY strategy with parallel matching (goroutine-based)
- Auto-detection algorithm (IDENTITY → FUZZY → NONE)
- ID collision handling (CONTEXT mode: a-, b-, c-)
- Stop scorer (name + geographic distance with Haversine)
- Route scorer (agency + route names)
- Trip scorer (route + stop sequence + direction)
- Service scorer (day pattern + date range overlap)
- **Agency scorer** (name, timezone, URL, phone, email, language, fareUrl) - 883d5c7
- **Transfer scorer** (from/to stops, type, min_transfer_time) - 5a26089
- Reference updating system (agencies, stops, routes, services, shapes, trips, stop times)
- **Stop times trip reference updating** (fixed critical timing issue) - abaac96
- **Calendar dates merging** (AddedDates/RemovedDates preservation) - a92fc39
- **Transfers merging** (IDENTITY/FUZZY duplicate detection) - 465d556
- **Frequencies merging** (time window deduplication, embedded in trips) - cbb8448
- **CLI tool** (command-line interface compatible with Java onebusaway-gtfs-merge-cli) - 4f10d10
  - Flag parsing, input handling, merge orchestration
  - Statistics reporting, error handling
  - Note: Output writer not implemented (creates placeholders only)
- Comprehensive test suite (85%+ coverage, 60+ tests)
- Parallel fuzzy matching (multi-core, race-tested)

---

## High Priority - Core Functionality

### 1. Additional Entity Scorers
**Status**: Complete (2/3 implemented, 1 blocked)
**Priority**: High
**Estimated Time**: 3-4 hours

- [x] **AgencyScorer** (2 hours) ✅ COMPLETE
  - Compare agency name similarity
  - Compare timezone
  - Compare URL/phone if present
  - Pattern: Follow StopScorer/TripScorer/ServiceScorer approach
  - Tests: 9 test functions, 28 subtests
  - Commit: 883d5c7

- [x] **TransferScorer** (1 hour) ✅ COMPLETE
  - Compare from_stop and to_stop
  - Compare transfer_type
  - Compare min_transfer_time
  - Tests: 5 test functions, 18 subtests
  - Commit: 5a26089

- [ ] **FareScorer** ❌ BLOCKED
  - **Cannot implement**: go-gtfs v1.1.0 does not include fare-related structs (FareAttribute, FareRule)
  - Requires: go-gtfs library upgrade to version with fare support
  - Status: Deferred until library dependency is upgraded

### 2. Remaining Entity Types - Merging Logic
**Status**: Complete (4/5 done, 1 blocked)
**Priority**: High
**Estimated Time**: 6-8 hours

- [x] **Stop Times Merging** ✅ COMPLETE (abaac96)
  - Leaf entity, depends on stops and trips
  - No duplicate detection needed (referenced by trips)
  - Update stop and trip references via UpdateTripReferences()
  - Tests: TestUpdateTripReferences_UpdatesStopTimeTripReferences

- [x] **Calendar Dates Merging** ✅ COMPLETE (a92fc39)
  - Exception dates for services
  - Merges AddedDates and RemovedDates from duplicate services
  - Deduplicates and sorts dates chronologically
  - Tests: 3 test functions covering merging and conflicts
  - Note: Calendar dates are embedded in Service struct, not separate entities

- [x] **Transfers Merging** ✅ COMPLETE (465d556)
  - Duplicate detection with TransferScorer
  - Update from_stop and to_stop references (already handled by UpdateStopReferences)
  - Tests: IDENTITY, FUZZY duplicate detection
  - Note: Transfers don't have IDs, identified by from_stop + to_stop

- [x] **Frequencies Merging** ✅ COMPLETE (cbb8448)
  - Duplicate detection by start_time + end_time (time window)
  - Frequencies merged when trips are duplicates
  - Deduplicates within same trip, preserves unique time windows
  - Tests: 3 test functions with 11 subtests covering duplicate detection, merging, edge cases
  - Note: Frequencies are embedded in ScheduledTrip.Frequencies[], no separate references needed
  - Bonus: Fixed transfer duplicate detection bug (existing.To.Id comparison)

- [ ] **Fare Rules Merging** ❌ BLOCKED
  - **Cannot implement**: go-gtfs v1.1.0 lacks fare support (FareAttribute, FareRule)
  - Requires: go-gtfs library upgrade
  - Status: Deferred until library dependency is upgraded

### 3. Reference Updating - Additional Entities
**Status**: Complete (Core entities)
**Priority**: High
**Estimated Time**: 2-3 hours

- [x] ~~Update agency references~~ ✅
- [x] ~~Update stop references~~ ✅
- [x] ~~Update route references~~ ✅
- [x] ~~Update service references~~ ✅
- [x] ~~Update shape references~~ ✅
- [x] ~~Update trip references~~ ✅
- [x] **Update frequency references** ✅ NOT NEEDED (cbb8448)
  - Frequencies are embedded in ScheduledTrip.Frequencies[] with no Trip references
  - Automatically updated when trips are merged (part of trip struct)
  - No separate reference updating required

- [ ] **Update fare references** (1 hour)
  - Update FareRule references (route, origin, destination, contains)
  - Tests: Complex due to multiple reference types

- [ ] **Update transfer references** (30 min)
  - Update Transfer.FromStop and Transfer.ToStop references
  - Tests: Add to references_test.go

- [ ] **Update calendar date references** (30 min)
  - Update CalendarDate.Service references
  - Tests: Add to references_test.go

---

## Medium Priority - Tooling

### 4. CLI Tool Implementation
**Status**: Partially Complete (Core functionality done, output writer needed)
**Priority**: Medium
**Estimated Time**: 6-8 hours

- [x] **Basic CLI Structure** ✅ COMPLETE (4f10d10)
  - Created `cmd/gtfs-merge/main.go`
  - Flag parsing with standard library
  - Comprehensive help text and usage documentation
  - Version information

- [x] **Input Handling** ✅ COMPLETE (4f10d10)
  - Reads multiple GTFS .zip files
  - Parses feeds with go-gtfs ParseStatic
  - Error handling and validation
  - File existence checking

- [x] **Configuration** ✅ COMPLETE (4f10d10)
  - Strategy selection (--duplicateDetection flag)
  - All scorers registered automatically
  - Compatible with Java onebusaway-gtfs-merge-cli interface

- [x] **Statistics Reporting** ✅ COMPLETE (4f10d10)
  - Reports duplicates, renamings, entity counts
  - Error on duplicates flag (--errorOnDroppedDuplicates)
  - Log dropped duplicates flag (--logDroppedDuplicates)

- [ ] **Output Writing** ❌ CRITICAL GAP
  - Currently creates placeholder files only
  - Needs CSV serialization for all GTFS entities
  - See "GTFS Writer" task below

- [ ] **Advanced Configuration** (Not implemented)
  - Per-file configuration (--file flag)
  - Threshold configuration (--threshold flag, currently hardcoded to 0.7)
  - Rename mode selection (uses default)

- [ ] **Progress Reporting** (Not implemented)
  - Progress bar for large feeds
  - Verbose mode for debugging
  - Log file output option

- [ ] **CLI Tests** (Not implemented)
  - Integration tests with sample feeds
  - Error handling tests
  - Output validation

**Implemented Flags:**
- `--duplicateDetection=identity|fuzzy|none` (default: identity)
- `--renameDuplicates`
- `--logDroppedDuplicates`
- `--errorOnDroppedDuplicates`
- `--version`

**Compatible Command Syntax:**
```bash
gtfs-merge input1.zip input2.zip output.zip
gtfs-merge --duplicateDetection=fuzzy input1.zip input2.zip output.zip
```

### 5. GTFS Writer (CRITICAL for CLI)
**Status**: Not Started
**Priority**: High (blocking CLI production use)
**Estimated Time**: 4-6 hours

- [ ] **CSV Serialization** (3 hours)
  - Serialize all GTFS entity types to CSV format
  - Required files: agency.txt, stops.txt, routes.txt, trips.txt, stop_times.txt, calendar.txt, calendar_dates.txt
  - Optional files: shapes.txt, transfers.txt, frequencies.txt, fare_attributes.txt, fare_rules.txt
  - Handle required and optional fields per GTFS spec
  - Proper CSV escaping (quotes, commas, newlines)
  - Write correct headers for each file

- [ ] **Embedded Entity Handling** (1 hour)
  - StopTimes embedded in Trips
  - Frequencies embedded in Trips
  - Calendar dates embedded in Services
  - Flatten for CSV output

- [ ] **Zip File Writing** (0.5 hours)
  - Create .zip archive
  - Write all CSV files to zip
  - Proper file compression

- [ ] **Field Mapping** (1 hour)
  - Map go-gtfs structs to GTFS CSV columns
  - Handle nil/optional fields
  - Format types correctly (durations, dates, enums)

- [ ] **Tests** (0.5 hours)
  - Roundtrip read/write validation
  - Verify GTFS spec compliance
  - Test with real-world feeds

**Note**: Once complete, update CLI writeGTFSFeed() to use this implementation.

### 6. Feed Validation Utilities
**Status**: Not Started
**Priority**: Medium
**Estimated Time**: 2-3 hours

- [ ] **GTFS Reader** (1 hour)
  - Already handled by go-gtfs ParseStatic
  - Add convenience wrappers if needed

- [ ] **Feed Validation** (1-2 hours)
  - Basic GTFS validation
  - Required fields check
  - Referential integrity check
  - Tests: Valid/invalid feed detection

---

## Lower Priority - Integration & Optimization

### 6. Maglev API Integration
**Status**: Not Started
**Priority**: Low
**Estimated Time**: 4-6 hours

- [ ] **API Endpoint** (2 hours)
  - REST endpoint for merge requests
  - Request validation
  - Response formatting
  - Error handling

- [ ] **Configuration Management** (1 hour)
  - Merge strategy configuration
  - Scorer registration
  - Threshold settings

- [ ] **Background Processing** (2 hours)
  - Queue merge jobs
  - Status tracking
  - Result storage

- [ ] **API Tests** (1 hour)
  - Integration tests
  - Error handling tests
  - Performance tests

### 7. Performance Benchmarks
**Status**: Not Started
**Priority**: Low
**Estimated Time**: 3-4 hours

- [ ] **Benchmark Suite** (2 hours)
  - Merge performance benchmarks
  - Scorer performance benchmarks
  - Memory allocation profiling
  - CPU profiling

- [ ] **Large Feed Testing** (1.5 hours)
  - Test with real metro feeds
  - Measure merge time
  - Identify bottlenecks

- [ ] **Optimization** (variable)
  - Based on profiling results
  - Optimize hot paths
  - Reduce allocations
  - Parallelize where beneficial

### 8. Documentation Updates
**Status**: Needs Update
**Priority**: Low
**Estimated Time**: 2-3 hours

- [ ] **README Updates** (1 hour)
  - Update "In Progress" section → "Completed"
  - Update coverage statistics
  - Add completed scorers to feature list
  - Update test counts

- [ ] **API Documentation** (1 hour)
  - Godoc comments for all public APIs
  - Usage examples
  - Architecture diagrams

- [ ] **Usage Examples** (1 hour)
  - More real-world examples
  - Common patterns
  - Troubleshooting guide

---

## Optional Enhancements

### 9. Additional Features
**Status**: Not Started
**Priority**: Optional
**Estimated Time**: Variable

- [ ] **AGENCY Rename Mode** (2 hours)
  - Prefix with agency_id instead of feed index
  - Tests: Verify correct prefixing
  - Documentation

- [ ] **Integration Tests with Real Feeds** (3 hours)
  - Download sample feeds from major cities
  - Test merge with real data
  - Validate output correctness
  - Performance measurements

- [ ] **Conflict Resolution Strategies** (4 hours)
  - User-defined conflict resolution
  - Custom merge rules
  - Priority-based merging
  - Tests and documentation

- [ ] **Merge Statistics Report** (2 hours)
  - Detailed merge report
  - Entity-by-entity breakdown
  - Duplicate analysis
  - Quality metrics

---

## Estimated Total Remaining Time

- **High Priority**: 11-15 hours
- **Medium Priority**: 10-13 hours
- **Low Priority**: 9-13 hours
- **Optional**: 11+ hours

**Total Core Functionality**: ~21-28 hours
**Total with Tooling**: ~31-41 hours
**Complete Package**: ~40-54 hours

---

## Recommended Implementation Order

1. **Phase 1**: Additional Scorers (3-4 hours)
   - AgencyScorer
   - TransferScorer
   - FareScorer

2. **Phase 2**: Remaining Entity Merging (6-8 hours)
   - Stop Times
   - Calendar Dates
   - Transfers
   - Frequencies
   - Fare Rules

3. **Phase 3**: Complete Reference Updates (2-3 hours)
   - Frequency references
   - Fare references
   - Transfer references
   - Calendar date references

4. **Phase 4**: CLI Tool (6-8 hours)
   - Basic CLI structure
   - I/O handling
   - Configuration and progress

5. **Phase 5**: Feed Utilities (4-5 hours)
   - GTFS reader
   - GTFS writer
   - Validation

6. **Phase 6**: Polish & Integration (optional)
   - Maglev integration
   - Benchmarks
   - Documentation
   - Additional features

---

## Notes

- All estimates assume following established TDD patterns (RED → GREEN → REFACTOR)
- Each feature should include subagent code review
- All features must pass lint/fmt/vet/test before commit
- Maintain 85%+ test coverage throughout

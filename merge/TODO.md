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
- Comprehensive test suite (85%+ coverage, 50+ tests)
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
**Status**: Mostly Complete (3/5 done, 1 blocked)
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

- [ ] **Frequencies Merging** (1.5 hours)
  - Duplicate detection: trip_id + start_time + end_time
  - Update trip references
  - Tests: Duplicate detection, reference updates

- [ ] **Fare Rules Merging** ❌ BLOCKED
  - **Cannot implement**: go-gtfs v1.1.0 lacks fare support (FareAttribute, FareRule)
  - Requires: go-gtfs library upgrade
  - Status: Deferred until library dependency is upgraded

### 3. Reference Updating - Additional Entities
**Status**: Partially Complete
**Priority**: High
**Estimated Time**: 2-3 hours

- [x] ~~Update agency references~~ ✅
- [x] ~~Update stop references~~ ✅
- [x] ~~Update route references~~ ✅
- [x] ~~Update service references~~ ✅
- [x] ~~Update shape references~~ ✅
- [x] ~~Update trip references~~ ✅
- [ ] **Update frequency references** (30 min)
  - Update Frequency.Trip references
  - Tests: Add to references_test.go

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
**Status**: Not Started
**Priority**: Medium
**Estimated Time**: 6-8 hours

- [ ] **Basic CLI Structure** (2 hours)
  - Create `cmd/gtfs-merge/main.go`
  - Argument parsing with flags package or cobra
  - Help text and usage documentation
  - Version information

- [ ] **Input/Output Handling** (3 hours)
  - Read multiple GTFS .zip files
  - Parse and load feeds with go-gtfs
  - Write merged feed to output .zip
  - Error handling and validation

- [ ] **Configuration** (1 hour)
  - Strategy selection (--strategy flag)
  - Threshold configuration (--threshold flag)
  - Rename mode selection (--rename-mode flag)
  - Scorer registration based on config

- [ ] **Progress Reporting** (1.5 hours)
  - Progress bar for large feeds
  - Statistics reporting (duplicates, renamings)
  - Verbose mode for debugging
  - Log file output option

- [ ] **CLI Tests** (0.5 hours)
  - Integration tests with sample feeds
  - Error handling tests
  - Output validation

### 5. Feed Reading/Writing Utilities
**Status**: Not Started
**Priority**: Medium
**Estimated Time**: 4-5 hours

- [ ] **GTFS Reader** (2 hours)
  - Read .zip files
  - Parse CSV files
  - Use go-gtfs library
  - Error handling for malformed feeds
  - Tests: Read various feed formats

- [ ] **GTFS Writer** (2 hours)
  - Write merged feed to .zip
  - Generate valid CSV files
  - Preserve optional fields
  - Tests: Roundtrip read/write validation

- [ ] **Feed Validation** (1 hour)
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

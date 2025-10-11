# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Getting Started

**Prerequisites**:
- Go 1.24.2 or later
- Copy `config.example.json` to `config.json` and configure required values (needed for `make run`)

**Verify installation**: `http://localhost:4000/api/where/current-time.json?key=test`

## Development Commands

All commands are managed through the Makefile:

- `make run` - Build and run the server with Unitrans real-time data (requires `.env` with API keys)
- `make build` - Build the application binary to bin/maglev
- `make test` - Run all tests
- `make lint` - Run golangci-lint (requires installation: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)
- `make coverage` - Generate test coverage report with HTML output
- `make models` - Regenerate sqlc models from SQL queries
- `make watch` - Run with Air for live reloading during development (automatically rebuilds and restarts on code changes)
- `make clean` - Clean build artifacts

## Debugging

Use Delve for debugging:

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Build the app
make build

# Start the debugger
dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./bin/maglev
```

Then connect from GoLand IDE or other Delve-compatible debugger.

## Important: Requirements to make a commit

Before committing any code, you must always run all of these steps, and have them all succeed:

1. Run `make lint` and fix any linting issues that are identified
2. Run `make test` and fix any failing tests
3. Run `go fmt ./...` and commit all of the formatting changes

## Architecture Overview

This is a Go 1.24.2+ application that provides a REST API for OneBusAway transit data. The architecture follows a layered design:

### Core Components

- **Application Layer** (`internal/app/`): Central dependency injection container holding config, logger, and GTFS manager
- **REST API Layer** (`internal/restapi/`): HTTP handlers for the OneBusAway API endpoints
- **Web UI Layer** (`internal/webui/`): HTTP handlers for the web interface and landing page
- **GTFS Manager** (`internal/gtfs/`): Manages both static GTFS data and real-time feeds (trip updates, vehicle positions)
- **Database Layer** (`gtfsdb/`): SQLite database with sqlc-generated Go code for type-safe SQL operations
- **Models** (`internal/models/`): Business logic and data structures for agencies, routes, stops, trips, vehicles
- **Utilities** (`internal/utils/`, `internal/appconf/`, `internal/logging/`): Helper functions, configuration management, and logging

### Data Flow

1. GTFS static data is loaded from URLs or local files into SQLite via the GTFS manager
2. Real-time data (GTFS-RT) is periodically fetched and merged with static data
3. REST API handlers query the GTFS manager and database to serve OneBusAway-compatible responses
4. All database access uses sqlc-generated type-safe queries from `gtfsdb/query.sql`

### Key Patterns

- Dependency injection through the `Application` struct
- All HTTP handlers embed `*app.Application` for access to shared dependencies
- Database operations use sqlc for compile-time query validation
- Real-time data is managed with read-write mutexes for concurrent access
- Configuration is handled through command-line flags with defaults

## Database Management

The project uses SQLite with sqlc for type-safe database access:

- Schema: `gtfsdb/schema.sql`
- Queries: `gtfsdb/query.sql`
- Generated code: `gtfsdb/query.sql.go` and `gtfsdb/models.go`
- Configuration: `gtfsdb/sqlc.yml`

After modifying SQL queries or schema, run `make models` to regenerate the Go code.

## Testing

- Run single test: `go test ./path/to/package -run TestName`
- Run tests with verbose output: `go test -v ./...`
- Generate coverage: `make coverage` (opens HTML report in browser)

Test files follow Go conventions with `_test.go` suffix and are co-located with the code they test.

## Data Access Patterns

### GTFS Manager vs Database Access

The GTFS Manager provides two types of data access:

**In-Memory Data** (from `manager.gtfsData`):
- `FindAgency(id)` - Direct agency lookup
- `RoutesForAgencyID(id)` - Routes for an agency
- `VehiclesForAgencyID(id)` - Real-time vehicle data
- Access via: `api.GtfsManager.FindAgency()`, etc.

**Database Queries** (via sqlc):
- `GetRoute(ctx, id)` - Single route by ID
- `GetAgency(ctx, id)` - Single agency by ID
- Access via: `api.GtfsManager.GtfsDB.Queries.GetRoute()`, etc.
- **Important**: No `FindRoute()` method exists - use database queries for route lookups

### Working with sqlc Models

Database models use `sql.NullString` for optional fields:

```go
// Always check .Valid before accessing .String
if route.ShortName.Valid {
    shortName = route.ShortName.String
}
```

Common nullable fields: `ShortName`, `LongName`, `Desc`, `Url`, `Color`, `TextColor`

## OneBusAway API Patterns

### Response Structure

All endpoints return standardized responses using `models.NewListResponse()`:

```go
response := models.NewListResponse(dataList, references)
```

### Building References

Use maps to deduplicate, then convert to slices:

```go
// Build reference maps to avoid duplicates
agencyRefs := make(map[string]models.AgencyReference)
routeRefs := make(map[string]models.Route)

// Convert to slices for final response
agencyRefList := make([]models.AgencyReference, 0, len(agencyRefs))
for _, ref := range agencyRefs {
    agencyRefList = append(agencyRefList, ref)
}
```

### GTFS-RT Status Mapping

Map GTFS-RT CurrentStatus enum to OneBusAway strings:
- `0` (INCOMING_AT) → `"INCOMING_AT"` / `"approaching"`
- `1` (STOPPED_AT) → `"STOPPED_AT"` / `"stopped"`
- `2` (IN_TRANSIT_TO) → `"IN_TRANSIT_TO"` / `"in_progress"`
- Default → `"SCHEDULED"` / `"scheduled"`

### API Route Registration

Check `internal/restapi/routes.go` first - many endpoints are already registered but may need implementation updates. Route patterns follow: `/api/where/{endpoint}/{id}` with API key validation.

## Testing Real-Time Data

### Test Data Matching Requirements

**Critical**: GTFS static data and GTFS-RT data must be from the same transit agency to achieve meaningful test coverage. Mismatched data results in:
- Real-time vehicles that don't match any agency routes
- Vehicle processing loops that never execute with actual data
- Poor test coverage of core functionality

**Example**: Using RABA static data (`raba.zip`) with Unitrans real-time data (`unitrans-*.pb`) will load vehicles but `VehiclesForAgencyID()` returns 0 vehicles.

### Real-Time Test Infrastructure

For testing GTFS-RT functionality, use HTTP test servers to serve local `.pb` files:

```go
func createTestApiWithRealTimeData(t *testing.T) (*RestAPI, func()) {
    mux := http.NewServeMux()

    mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
        data, err := os.ReadFile(filepath.Join("../../testdata", "agency-vehicle-positions.pb"))
        require.NoError(t, err)
        w.Header().Set("Content-Type", "application/x-protobuf")
        w.Write(data)
    })

    server := httptest.NewServer(mux)

    gtfsConfig := gtfs.Config{
        GtfsURL:              filepath.Join("../../testdata", "agency.zip"),
        VehiclePositionsURL:  server.URL + "/vehicle-positions",
        TripUpdatesURL:       server.URL + "/trip-updates",
    }

    // ... rest of setup
    return api, server.Close
}
```

**Benefits**:
- Simulates real-world HTTP usage without modifying production code
- Keeps test-specific infrastructure isolated
- Allows testing with matching static/real-time data pairs
- Easy to set up and maintain

### Coverage Improvements

Real-time data testing can dramatically improve coverage:
- **Before matching data**: ~30% handler coverage
- **After matching data**: ~86% handler coverage

Test the complete vehicle processing pipeline including timestamp conversion, location mapping, status translation, and reference building.

## GTFS Time Handling

### Time Storage and Conversion

GTFS stop_times data follows this conversion chain:

1. **GTFS File Format**: Times are stored as "HH:MM:SS" strings (e.g., "08:30:00")
2. **GTFS Library**: Parsed into `time.Duration` values (nanoseconds internally)
3. **Database Storage**: Stored as `int64` nanoseconds since midnight in SQLite
4. **API Response**: Converted to Unix epoch timestamps in milliseconds

### Converting GTFS Times to API Timestamps

To convert database time values to API timestamps:

```go
// Database stores time.Duration as int64 nanoseconds since midnight
// Convert to Unix timestamp in milliseconds for a specific date
startOfDay := time.Unix(date/1000, 0).Truncate(24 * time.Hour)
arrivalDuration := time.Duration(row.ArrivalTime)
arrivalTimeMs := startOfDay.Add(arrivalDuration).UnixMilli()
```

**Key Points**:
- Database `arrival_time` and `departure_time` are nanoseconds since midnight
- API responses need Unix epoch timestamps in milliseconds
- Always use the target date to calculate the proper epoch time
- GTFS times can exceed 24 hours (e.g., "25:30:00" for 1:30 AM next day)

## New Endpoint Implementation Workflow

### 1. Research and Planning
- Fetch official API documentation from https://developer.onebusaway.org/api/where/methods
- Examine production API responses to understand exact JSON structure
- Check existing similar endpoints for patterns and data access methods

### 2. Database Queries
- Add new sqlc queries to `gtfsdb/query.sql` if needed
- Run `make models` to regenerate Go code after query changes
- Test queries directly in SQLite to verify data availability

### 3. Models and Data Structures
- Create model structs in `internal/models/` matching API response format
- Include constructor functions following existing patterns (e.g., `NewScheduleStopTime`)
- Ensure JSON tags match production API field names exactly

### 4. Handler Implementation
- Follow existing handler patterns in `internal/restapi/`
- Use `utils.ExtractIDFromParams()` and `utils.ExtractAgencyIDAndCodeID()` for ID parsing
- Build reference maps to deduplicate agencies, routes, etc.
- Convert reference maps to slices for final response
- Use `models.NewEntryResponse()` or `models.NewListResponse()` for response structure

### 5. Route Registration
- Add route to `internal/restapi/routes.go` with `validateAPIKey` wrapper
- Follow pattern: `/api/where/{endpoint}/{id}` for single resource endpoints

### 6. Testing Strategy
- Use `createTestApi(t)` for test setup with RABA test data
- Use `serveApiAndRetrieveEndpoint(t, api, endpoint)` for integration testing
- Test both success and error cases (invalid IDs, missing data)
- Ensure tests pass with existing test data rather than requiring specific agency data

### 7. Data Validation
- Check that test stops/routes have actual schedule data before testing
- Use SQLite queries to verify data availability: `SELECT COUNT(*) FROM stop_times WHERE stop_id = '...'`
- Handle cases where stops exist but have no schedule data (return empty arrays, not errors)

## REST API Documentation

The official REST API documentation is available at: https://developer.onebusaway.org/api/where/methods

The Open API specification is located at https://github.com/OneBusAway/sdk-config/blob/main/openapi.yml

You should always fetch the latest version of the OpenAPI specification from the OneBusAway SDK Config repository
before implementing new endpoints or modifying existing ones.
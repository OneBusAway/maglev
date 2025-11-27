# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Getting Started

**Prerequisites** (choose one):
- **Native**: Go 1.24.2 or later
- **Docker**: Docker 20.10+ and Docker Compose v2.0+

**Setup**:
- Copy `config.example.json` to `config.json` and configure required values

**Verify installation**: `http://localhost:4000/api/where/current-time.json?key=test`

## Development Commands

All commands are managed through the Makefile:

- `make run` - Build and run the server with config from `config.json`
- `make build` - Build the application binary to `bin/maglev`
- `make test` - Run all tests
- `make lint` - Run golangci-lint (requires: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)
- `make coverage` - Generate test coverage report with HTML output
- `make models` - Regenerate sqlc models from SQL queries
- `make watch` - Run with Air for live reloading during development
- `make fmt` - Format all Go code with `go fmt`
- `make clean` - Clean build artifacts

## Docker Commands

Docker provides a consistent development environment across all platforms:

- `make docker-build` - Build the Docker image
- `make docker-run` - Build and run the container with mounted config
- `make docker-stop` - Stop and remove the running container
- `make docker-compose-up` - Start production services with Docker Compose
- `make docker-compose-down` - Stop Docker Compose services
- `make docker-compose-dev` - Start development environment with live reload
- `make docker-clean` - Remove all Docker images and volumes

**Quick Start with Docker:**
```bash
cp config.example.json config.json
docker-compose up
```

**Development with live reload:**
```bash
docker-compose -f docker-compose.dev.yml up
```

**Docker Files:**
- `Dockerfile` - Multi-stage production build (Go 1.24 + Alpine)
- `Dockerfile.dev` - Development image with Air live reload
- `docker-compose.yml` - Production configuration with volumes and health check
- `docker-compose.dev.yml` - Development setup with source mounting
- `.dockerignore` - Files excluded from Docker context
- `.air.docker.toml` - Air live reload configuration for Docker development

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

### File Structure

```
maglev/
├── cmd/api/              # Application entry point
├── internal/
│   ├── app/              # Application container (dependency injection)
│   ├── appconf/          # Configuration management
│   ├── gtfs/             # GTFS data management (static + real-time)
│   ├── logging/          # Structured logging and error handling
│   ├── models/           # Business models and API response structures
│   ├── restapi/          # HTTP handlers and middleware
│   ├── utils/            # Helper functions (geometry, ID parsing, validation)
│   └── webui/            # Web interface handlers
├── gtfsdb/               # SQLite database layer (sqlc-generated)
└── testdata/             # Test fixtures (RABA GTFS data, protobuf files)
```

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
- Configuration is handled through JSON config file or command-line flags

## Implemented API Endpoints

All endpoints are registered in `internal/restapi/routes.go`:

| Endpoint | Handler | Description |
|----------|---------|-------------|
| `/api/where/current-time.json` | `current_time_handler.go` | Server time |
| `/api/where/agencies-with-coverage.json` | `agencies_with_coverage_handler.go` | All agencies with coverage areas |
| `/api/where/agency/{id}` | `agency_handler.go` | Single agency details |
| `/api/where/routes-for-agency/{id}` | `routes_for_agency_handler.go` | Routes for an agency |
| `/api/where/route-ids-for-agency/{id}` | `route_ids_for_agency_handler.go` | Route IDs only |
| `/api/where/stops-for-agency/{id}` | `stops_for_agency_handler.go` | Stops for an agency |
| `/api/where/stop-ids-for-agency/{id}` | `stop-ids-for-agency_handler.go` | Stop IDs only |
| `/api/where/stop/{id}` | `stop_handler.go` | Single stop details |
| `/api/where/stops-for-location.json` | `stops_for_location_handler.go` | Stops near coordinates |
| `/api/where/stops-for-route/{id}` | `stops_for_route_handler.go` | Stops on a route |
| `/api/where/routes-for-location.json` | `routes_for_location_handler.go` | Routes near coordinates |
| `/api/where/trip/{id}` | `trip_handler.go` | Single trip details |
| `/api/where/trip-details/{id}` | `trip_details_handler.go` | Extended trip info with status |
| `/api/where/trips-for-route/{id}` | `trips_for_route_handler.go` | Trips on a route |
| `/api/where/trips-for-location.json` | `trips_for_location_handler.go` | Active trips near coordinates |
| `/api/where/trip-for-vehicle/{id}` | `trip_for_vehicle_handler.go` | Trip for a vehicle |
| `/api/where/vehicles-for-agency/{id}` | `vehicles_for_agency_handler.go` | Real-time vehicles |
| `/api/where/block/{id}` | `block_handler.go` | Block configuration |
| `/api/where/shape/{id}` | `shapes_handler.go` | Polyline shape data |
| `/api/where/schedule-for-stop/{id}` | `schedule_for_stop_handler.go` | Stop schedule |
| `/api/where/schedule-for-route/{id}` | `schedule_for_route_handler.go` | Route schedule |
| `/api/where/arrival-and-departure-for-stop/{id}` | `arrival_and_departure_for_stop_handler.go` | Single arrival |
| `/api/where/arrivals-and-departures-for-stop/{id}` | `arrival_and_departure_for_stop_handler.go` | All arrivals |
| `/api/where/report-problem-with-trip/{id}` | `report_problem_with_trip_handler.go` | Report trip issue |
| `/api/where/report-problem-with-stop/{id}` | `report_problem_with_stop_handler.go` | Report stop issue |

## Middleware Components

Located in `internal/restapi/`:

| Middleware | File | Description |
|------------|------|-------------|
| **Compression** | `compression_middleware.go` | Gzip compression using `klauspost/compress/gzhttp`. Default: 1KB min size, level 6 |
| **Rate Limiting** | `rate_limit_middleware.go` | Per-API-key rate limiting with `golang.org/x/time/rate`. Auto-cleanup of idle limiters |
| **Request Logging** | `request_logging_middleware.go` | HTTP request/response logging |
| **Security** | `security_middleware.go` | Security headers and protections |

Middleware chain (innermost to outermost): `handler → compression → rate limiting → API key validation`

## Helper Modules

### ID Utilities (`internal/utils/api.go`)

OneBusAway uses combined IDs in the format `{agency_id}_{code_id}`:

```go
// Extract parts from combined ID
agencyID, codeID, err := utils.ExtractAgencyIDAndCodeID("25_1234")
// agencyID = "25", codeID = "1234"

// Extract just one part
agencyID, _ := utils.ExtractAgencyID("25_1234")
codeID, _ := utils.ExtractCodeID("25_1234")

// Form combined ID
combinedID := utils.FormCombinedID("25", "1234") // "25_1234"

// Extract ID from HTTP request path (removes .json extension)
id := utils.ExtractIDFromParams(r) // "25_1234.json" → "25_1234"
```

### Geometry (`internal/utils/geometry.go`)

```go
// Calculate distance in meters between two coordinates
distance := utils.Haversine(lat1, lon1, lat2, lon2)
```

### Parameter Parsing (`internal/utils/api.go`)

```go
// Parse float parameters with validation
lat, fieldErrors := utils.ParseFloatParam(r.URL.Query(), "lat", nil)

// Parse time parameter (epoch ms or YYYY-MM-DD)
dateStr, parsedTime, fieldErrors, ok := utils.ParseTimeParameter(timeParam, location)
```

### Vehicle Status (`internal/restapi/vehicles_helper.go`)

```go
// Convert GTFS-RT status to OneBusAway format
status, phase := GetVehicleStatusAndPhase(vehicle)
// Returns: ("IN_TRANSIT_TO", "in_progress"), ("STOPPED_AT", "stopped"), etc.
```

## Database Management

The project uses SQLite with sqlc for type-safe database access:

- Schema: `gtfsdb/schema.sql`
- Queries: `gtfsdb/query.sql`
- Generated code: `gtfsdb/query.sql.go` and `gtfsdb/models.go`
- Configuration: `gtfsdb/sqlc.yml`

After modifying SQL queries or schema, run `make models` to regenerate the Go code.

### Key Database Queries

**Single Entity Lookups:**
- `GetAgency`, `GetRoute`, `GetStop`, `GetTrip` - Fetch by ID

**Agency-scoped Queries:**
- `GetRouteIDsForAgency`, `GetStopIDsForAgency` - IDs for an agency
- `GetStopForAgency` - Stop verified to belong to agency

**Location-based:**
- `GetStopsWithinBounds` - Spatial query for stops in bounding box

**Route/Trip Relations:**
- `GetStopIDsForRoute`, `GetStopIDsForTrip` - Stop IDs on route/trip
- `GetRoutesForStop`, `GetRouteIDsForStop` - Routes serving a stop
- `GetAllTripsForRoute`, `GetTripsForRouteInActiveServiceIDs` - Trips on route
- `GetStopsForRoute` - All stops on a route

**Schedule Data:**
- `GetScheduleForStop`, `GetScheduleForStopOnDate` - Stop schedules
- `GetStopTimesForTrip`, `GetStopTimesForStopInWindow` - Stop times
- `GetArrivalsAndDeparturesForStop` - Arrivals/departures

**Block Operations:**
- `GetTripsByBlockID`, `GetBlockDetails` - Block trip sequences
- `GetTripsByBlockIDOrdered` - Trips ordered by departure time

**Shape Data:**
- `GetShapeByID`, `GetShapePointsForTrip` - Route polylines
- `GetShapesGroupedByTripHeadSign` - Shapes by direction

**Service Calendar:**
- `GetActiveServiceIDsForDate` - Active services for a date
- `GetCalendarByServiceID`, `GetCalendarDateExceptionsForServiceID` - Service patterns

**Batch Queries (N+1 prevention):**
- `GetRoutesForStops`, `GetAgenciesForStops` - Batch lookups
- `GetStopsByIDs`, `GetRoutesByIDs`, `GetTripsByIDs` - Batch by IDs

## In-Memory Data Structures

The GTFS Manager (`internal/gtfs/gtfs_manager.go`) maintains:

**Static Data** (from `manager.gtfsData`):
- `Agencies` - Transit agency information
- `Routes` - All routes
- `Stops` - All stops
- `Trips` - Scheduled trips
- Accessed via: `GetAgencies()`, `GetTrips()`, `GetStops()`, `GetStaticData()`

**Real-Time Data** (protected by `realTimeMutex`):
- `realTimeTrips` - GTFS-RT trip updates
- `realTimeVehicles` - GTFS-RT vehicle positions
- `realTimeAlerts` - Service alerts
- `realTimeTripLookup` - Map of trip ID → index for O(1) lookup
- `realTimeVehicleLookupByTrip` - Map of trip ID → vehicle index
- `realTimeVehicleLookupByVehicle` - Map of vehicle ID → vehicle index

**Direction Calculator** (shape-based direction inference):
- `DirectionCalculator` - Precomputed stop directions from shape geometry

## Data Access Patterns

### GTFS Manager vs Database Access

**In-Memory Data** (from `manager.gtfsData`):
- `FindAgency(id)` - Direct agency lookup
- `RoutesForAgencyID(id)` - Routes for an agency
- `VehiclesForAgencyID(id)` - Real-time vehicle data
- `GetVehicleForTrip(tripID)` - Vehicle for a trip (checks block)
- `GetVehicleByID(vehicleID)` - Vehicle by ID
- `GetTripUpdateByID(tripID)` - Real-time trip update
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

## Testing

- Run single test: `go test ./path/to/package -run TestName`
- Run tests with verbose output: `go test -v ./...`
- Generate coverage: `make coverage` (opens HTML report in browser)

Test files follow Go conventions with `_test.go` suffix and are co-located with the code they test.

### Test Data Files

Located in `testdata/`:
- `raba.zip` - RABA transit static GTFS data
- `raba.db` - Pre-built SQLite database
- `raba-vehicle-positions.pb` - RABA real-time vehicle positions
- `raba-trip-updates.pb` - RABA real-time trip updates
- `unitrans-*.pb` - Unitrans real-time data (for mismatched data testing)
- `config_*.json` - Configuration test fixtures

### Testing Patterns

**Basic Test Setup:**
```go
func createTestApi(t *testing.T) *RestAPI {
    // Creates API with RABA test data from testdata/raba.zip
}

func serveApiAndRetrieveEndpoint(t *testing.T, api *RestAPI, endpoint string) map[string]interface{} {
    // Makes HTTP request and returns parsed JSON response
}
```

**Real-Time Test Setup:**
```go
func createTestApiWithRealTimeData(t *testing.T) (*RestAPI, func()) {
    mux := http.NewServeMux()
    mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
        data, _ := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
        w.Header().Set("Content-Type", "application/x-protobuf")
        w.Write(data)
    })
    server := httptest.NewServer(mux)
    // ... configure with server URLs
    return api, server.Close
}
```

### Test Data Matching Requirements

**Critical**: GTFS static data and GTFS-RT data must be from the same transit agency to achieve meaningful test coverage. Mismatched data results in:
- Real-time vehicles that don't match any agency routes
- Vehicle processing loops that never execute with actual data
- Poor test coverage of core functionality

## OneBusAway API Patterns

### Response Structure

All endpoints return standardized responses:

```go
// Single entry response
response := models.NewEntryResponse(entry, references)

// List response
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
- Add route to `internal/restapi/routes.go` with `rateLimitAndValidateAPIKey` wrapper
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

## Configuration

Maglev supports JSON configuration files with IDE validation via `config.schema.json`:

```json
{
  "$schema": "./config.schema.json",
  "port": 4000,
  "env": "development",
  "api-keys": ["test"],
  "rate-limit": 100,
  "gtfs-static-feed": { "url": "..." },
  "gtfs-rt-feeds": [{ "vehicle-positions-url": "..." }]
}
```

## REST API Documentation

The official REST API documentation is available at: https://developer.onebusaway.org/api/where/methods

The Open API specification is located at https://github.com/OneBusAway/sdk-config/blob/main/openapi.yml

You should always fetch the latest version of the OpenAPI specification from the OneBusAway SDK Config repository before implementing new endpoints or modifying existing ones.

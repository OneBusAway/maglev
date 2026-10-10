# GTFS-Realtime export contract

## Purpose

Define the shared external contract for agency-wide GTFS-Realtime exports so clients can consume Maglev feeds without treating JSON APIs or raw upstream feeds as substitutes.

## ADDED Requirements

### Requirement: Agency export formats
The system SHALL serve GET `/api/gtfs_realtime/{export}-for-agency/{id}.{format}` for exports `vehicle-positions`, `alerts`, and `trip-updates`, and formats `pb` and `pbtext`. Successful bodies SHALL contain a GTFS-RT FeedMessage without an OBA JSON envelope. Both formats SHALL represent the same feed-building behavior; exact Java text formatting is not required.

#### Scenario: Binary and text exports
- **WHEN** each of the three exports is requested in both formats against equivalent state and a fixed time
- **THEN** binary bodies decode as FeedMessage and text bodies parse as protobuf text with equivalent contents, disregarding nonsemantic ordering

### Requirement: Feed header time
Every successful feed SHALL declare `gtfs_realtime_version="2.0"`. Its timestamp SHALL be the effective request time in epoch seconds. `time` SHALL follow Maglev's shared parsing conventions: absent/empty uses current time; epoch milliseconds, `YYYY-MM-DD`, and `YYYY-MM-DD_HH-mm-ss` are accepted. Date strings SHALL use the agency timezone. Request time SHALL NOT imply historical realtime replay.

#### Scenario: Fixed timestamp
- **WHEN** a valid export is requested with `time=1791581968000`
- **THEN** its header timestamp is `1791581968`

#### Scenario: Current timestamp
- **WHEN** time is absent or empty
- **THEN** the header reflects current server time in epoch seconds

#### Scenario: Epoch zero
- **WHEN** `time=0` is supplied
- **THEN** the header timestamp is zero, not current time

#### Scenario: Date-string input
- **WHEN** `time=2026-10-09_12-00-00` is supplied for an agency in America/Los_Angeles
- **THEN** it resolves to noon in that timezone; `time=2026-10-09` resolves to midnight in that timezone

#### Scenario: Numeric fractional-second truncation
- **WHEN** `time=1791581968999` is supplied
- **THEN** shared parsing truncates to epoch second `1791581968`, and the effective parsed time used by other export behavior is `1791581968000` milliseconds

### Requirement: Version and invalid parameters
The system SHALL accept absent or empty `version`, and `version=2`. Unsupported versions and malformed supported parameters SHALL produce 400 rather than an internal-server error. Error bodies are not required to be protobuf feeds. The request API version SHALL remain distinct from the GTFS-RT header version.

#### Scenario: Unsupported version
- **WHEN** an export is requested with `version=1`
- **THEN** it returns 400, not a successful feed

#### Scenario: Malformed time
- **WHEN** an export is requested with `time=bad-time`
- **THEN** it returns 400, not 500

### Requirement: Pre-epoch time rejection
After shared time parsing, times before the Unix epoch SHALL produce 400 because GTFS-RT header timestamps are unsigned. This format constraint SHALL NOT replace Maglev's accepted time syntax with a separate parser.

#### Scenario: Negative epoch input
- **WHEN** `time=-1000` is supplied
- **THEN** the request returns 400 rather than wrapping the timestamp into an unsigned value

#### Scenario: Pre-epoch date input
- **WHEN** a date string resolves to an instant before the Unix epoch
- **THEN** the request returns 400

### Requirement: Normalization option syntax
`removeAgencyIds` SHALL default to true when absent or empty. Nonempty values SHALL accept case-insensitive `true` or `false`; any other value SHALL return 400.

#### Scenario: Boolean forms
- **WHEN** the parameter is absent, empty, `TRUE`, or `False`
- **THEN** normalization is enabled for the first three cases and disabled for `False`

#### Scenario: Invalid boolean
- **WHEN** `removeAgencyIds=maybe` is supplied
- **THEN** the request returns 400

### Requirement: Authorization and rate limiting
All exports in both formats SHALL use Maglev's API-key authorization and rate limiting. Invalid or missing credentials SHALL NOT receive a successful feed, including after another caller requests the same URL. Rate-limited requests SHALL receive 429. Existing Maglev error-response conventions SHALL apply.

#### Scenario: Authorization across formats
- **WHEN** each export is requested in each format with a missing or invalid key after an authorized request
- **THEN** it is rejected by API-key validation rather than returning the prior caller's feed

#### Scenario: Rate limit exceeded
- **WHEN** an authorized caller exceeds its configured API rate limit
- **THEN** the export request returns 429

### Requirement: Empty feed success
An authorized, valid request for an unknown agency, an agency without retained matching data, or a filter without matching entities SHALL return 200 with a valid header and zero entities, rather than 404 or an invalid protobuf body.

#### Scenario: Unknown agency
- **WHEN** an authorized valid request names an unknown agency in any export or format
- **THEN** it returns 200 and a valid empty FeedMessage

### Requirement: Payload ID normalization
Payload trip, route, and stop IDs SHALL default to raw IDs, removing only known agency qualification and preserving arbitrary raw underscores. `removeAgencyIds=false` SHALL emit `{owningAgencyID}_{rawID}` without double-prefixing known qualified inputs. Vehicle IDs SHALL use the separate legacy interpretation requirement. Alert selector agency IDs SHALL remain intact; entity IDs follow their separate conventions.

#### Scenario: First underscore only
- **WHEN** a payload ID is `40_trip_with_underscores` and normalization is enabled
- **THEN** the exported payload ID is `trip_with_underscores`

#### Scenario: Raw IDs containing underscores
- **WHEN** raw upstream ID `trip_with_underscores` belongs to agency 40
- **THEN** default output remains `trip_with_underscores` and `removeAgencyIds=false` produces `40_trip_with_underscores`

#### Scenario: Known qualified input
- **WHEN** input identity is known to be qualified as `40_trip_with_underscores`
- **THEN** preserving qualification does not produce `40_40_trip_with_underscores`

#### Scenario: Prefix preservation
- **WHEN** `removeAgencyIds=false` is supplied to each export
- **THEN** resolvable payload IDs use their own agency qualification, including alert route, trip, and stop IDs; the requested agency does not replace another affected entity's owner

### Requirement: Legacy vehicle ID normalization
Vehicle IDs in all exports SHALL split at the first underscore into agency and vehicle ID. Without an underscore, the matched block's agency SHALL qualify the ID; feed agency membership SHALL NOT substitute. Default output SHALL omit the resolved agency prefix; `removeAgencyIds=false` SHALL preserve it without double-prefixing. Underscores after the first SHALL remain intact. A vehicle identity unresolved by these rules SHALL NOT be invented.

#### Scenario: Prefixed vehicle ID
- **WHEN** vehicle ID `40_bus_A` is exported
- **THEN** default payload ID is `bus_A` and `removeAgencyIds=false` emits `40_bus_A`

#### Scenario: Raw-looking vehicle ID with underscore
- **WHEN** upstream vehicle ID `bus_A` is exported
- **THEN** default payload ID is `A` and `removeAgencyIds=false` emits `bus_A`, because legacy interprets `bus` as the agency

#### Scenario: Unprefixed vehicle ID with matched block
- **WHEN** vehicle ID `1234` is associated with a matched block belonging to agency 40
- **THEN** default payload ID is `1234` and `removeAgencyIds=false` emits `40_1234`

### Requirement: Raw route filtering
Absent `routeFilterId` SHALL select agency-wide output. A present filter SHALL match the raw route ID, not its agency-qualified representation. Endpoint-specific selection SHALL occur without changing the exported record's identity or trimming alert selectors.

#### Scenario: Raw versus qualified filter
- **WHEN** a matching route is `40_2LINE`
- **THEN** `routeFilterId=2LINE` selects eligible records and `routeFilterId=40_2LINE` does not match that route

### Requirement: Content types and no caching
Successful binary responses SHALL use `application/x-google-protobuf`; text responses SHALL use `text/plain; charset=utf-8`. Export responses SHALL send `Cache-Control: no-store`. Conditional validators SHALL NOT be required, and malformed legacy Last-Modified dates SHALL NOT be reproduced.

#### Scenario: Repeated conditional request
- **WHEN** an authorized export request supplies conditional cache headers
- **THEN** a successful response contains the newly built feed and `Cache-Control: no-store`, rather than relying on a cached feed or a required 304 response

### Requirement: Available standard fields
For the supported standard fields listed in endpoint specs, supplied upstream values SHALL be retained through ingestion and exported when their records are selected. DTO omissions SHALL NOT excuse losing a supplied supported field. Missing values SHALL NOT be invented. Normalization, selection, reconciliation, and prediction reconstruction SHALL still apply; these endpoints SHALL NOT be raw upstream-feed proxies.

#### Scenario: Rich upstream fields
- **WHEN** an ingested vehicle or alert supplies supported bearing, speed, occupancy, cause, effect, or severity values
- **THEN** the corresponding values survive ingestion and export, even if current DTOs require extending

#### Scenario: Missing values
- **WHEN** an optional field is unavailable
- **THEN** the exporter does not fabricate a value merely to populate it

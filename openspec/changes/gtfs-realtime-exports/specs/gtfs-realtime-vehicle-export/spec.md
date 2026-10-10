# GTFS-Realtime vehicle export

## Purpose

Provide agency-wide vehicle positions suitable for vehicle-map clients, with explicit ownership, freshness, and route-selection behavior.

## ADDED Requirements

### Requirement: Vehicle agency ownership
The vehicle export SHALL use legacy vehicle identity interpretation: split an ID at its first underscore into agency and vehicle ID; otherwise use the matched block's agency. Feed agency membership SHALL NOT replace a missing block association. Unresolved vehicles SHALL be excluded. Unfiltered output SHALL cover all eligible retained vehicles for the resolved agency without geographic bounds, pagination, or an arbitrary count cap.

#### Scenario: Cross-agency active trip
- **WHEN** vehicle ID `A_bus_1` is associated with a block or active route belonging to agency B
- **THEN** the vehicle is selected for A's vehicle export, because the ID prefix takes precedence

#### Scenario: Legacy underscore interpretation
- **WHEN** an upstream vehicle ID is `bus_A`, even if the source intended the whole string as a raw ID
- **THEN** it resolves to agency `bus` and vehicle ID `A`, matching legacy rather than preserving the whole raw ID

#### Scenario: Matched-block fallback
- **WHEN** vehicle ID `1234` has no underscore and is associated with a matched block belonging to agency B
- **THEN** ownership resolves to B regardless of the source feed's agency membership

#### Scenario: Missing matched block
- **WHEN** vehicle ID `1234` has no underscore and no matched block, even on a single-agency source
- **THEN** it is excluded because ownership cannot be resolved by the legacy rules

### Requirement: Position and freshness eligibility
A vehicle SHALL have a position and update timestamp and satisfy `floor(requestTimeMs/1000) - floor(lastUpdateMs/1000) < 600` to be exported. Requested time SHALL be the shared contract's effective time. Missing positions or timestamps SHALL cause exclusion, not timestamp fabrication.

#### Scenario: Freshness boundary
- **WHEN** vehicle age calculated from epoch seconds is 599 seconds or 600 seconds
- **THEN** the 599-second vehicle is eligible and the 600-second vehicle is excluded

#### Scenario: Positionless vehicle
- **WHEN** a retained vehicle has no position
- **THEN** it is omitted even if its timestamp is fresh

#### Scenario: Missing update timestamp
- **WHEN** a positioned vehicle has no update timestamp
- **THEN** it is omitted rather than assigned request time or ingestion time

### Requirement: Trip-optional vehicles and route selection
An eligible vehicle without a trip SHALL be included in unfiltered output. Route-filtered output SHALL include only vehicles whose active trip's route matches the raw filter; a vehicle without a trip SHALL be excluded from filtered output.

#### Scenario: Vehicle without trip
- **WHEN** a fresh positioned vehicle with an agency-prefixed ID has no assigned trip
- **THEN** it appears in the agency-wide feed without a trip descriptor and does not appear in a route-filtered feed

#### Scenario: Active-route matching
- **WHEN** positioned fresh vehicles have active trips on routes A and B and the filter is A
- **THEN** only the vehicle on A is exported

### Requirement: Vehicle payload and entity identity
Each exported vehicle SHALL carry its vehicle descriptor, position, and last-update timestamp in epoch seconds. Available trip and route identifiers SHALL be included. FeedEntity IDs SHALL be sequential numeric strings starting at `1`, distinct from normalized payload vehicle IDs. Available additional standard fields SHALL follow the shared preservation contract.

#### Scenario: Two eligible vehicles
- **WHEN** two vehicles survive selection
- **THEN** their entity IDs are `1` and `2`, and their payloads contain the corresponding coordinates, vehicle IDs, and update timestamps

### Requirement: Supported vehicle fields
The export SHALL retain and export supplied bearing, speed, odometer, stop ID, current stop sequence, current status, congestion level, occupancy status, and occupancy percentage. Descriptor fields supplied for eligible vehicles SHALL also survive export. Unsupported future fields are not implicitly required.

#### Scenario: Rich vehicle ingestion
- **WHEN** a selected upstream vehicle supplies bearing, speed, odometer, stop sequence/status, congestion, and occupancy values
- **THEN** those values survive ingestion and appear in its exported VehiclePosition

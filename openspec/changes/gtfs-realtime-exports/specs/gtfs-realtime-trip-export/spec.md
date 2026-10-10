# GTFS-Realtime trip export

## Purpose

Provide agency-wide trip updates with useful legacy-compatible prediction reconstruction, correct trip selection and cancellation handling, and OBA headsign compatibility.

## ADDED Requirements

### Requirement: Own-trip agency and route selection
Trip updates SHALL be selected by their own route's agency. A route filter SHALL be applied to each exported trip's own route, including downstream/interlining trips and cancellations, rather than the vehicle's active route. Unfiltered output SHALL cover eligible trip state without imposing the vehicle-position export's location or 600-second freshness exclusions.

#### Scenario: Interlining route filters
- **WHEN** a vehicle is on route A and has downstream predictions for route B
- **THEN** filter A returns only A's updates, and filter B can return B's updates even while the vehicle is active on A

#### Scenario: Different route agencies
- **WHEN** predicted trips on one block serve routes owned by different agencies
- **THEN** each trip qualifies according to its own route agency, not the vehicle's ownership

#### Scenario: No vehicle position
- **WHEN** eligible trip prediction state exists without a vehicle position
- **THEN** absence of that position alone does not remove the trip update

### Requirement: Available block predictions
The export SHALL include predictions for distinct trips represented in the eligible block prediction state, not just the active trip. Each trip SHALL receive its own stop predictions. An association with a vehicle SHALL NOT limit output to one update per vehicle or assign another trip's stop predictions to it.

#### Scenario: Active and downstream predictions
- **WHEN** retained realtime information represents predictions for the active trip and a later trip on the same block
- **THEN** eligible updates for both trips are exported with their own stop predictions

### Requirement: Sparse prediction reconstruction
The export SHALL match supplied stop events to static trip visits and compute predictions from absolute event times or scheduled times plus event delay. Missing predictions SHALL be filled only within the rules below using the block's available schedule deviation. Service instances and stop sequences SHALL remain distinct. Static schedules alone SHALL NOT establish realtime evidence.

#### Scenario: Supplied absolute and delay events
- **WHEN** a visit scheduled at 10:10 supplies an absolute arrival of 10:12 and a departure delay of 90 seconds against scheduled departure 10:11
- **THEN** exported arrival is 10:12 and departure is 10:12:30 on the applicable service instance

#### Scenario: Loop visits
- **WHEN** a trip visits the same stop more than once and realtime data identifies a stop sequence
- **THEN** reconstruction associates the prediction with that visit rather than an unrelated occurrence of the stop ID

#### Scenario: No realtime evidence
- **WHEN** only static scheduled trips exist with no corresponding realtime evidence
- **THEN** the exporter does not fabricate a realtime prediction solely because those trips are scheduled

### Requirement: Bounded filling before supplied predictions
When a block has supplied stop predictions and a trip-level delay, missing scheduled visits SHALL receive scheduled arrival/departure plus block deviation only if predicted departure is strictly after the associated update timestamp (or request time if absent) and strictly before the earliest existing predicted event. Earliest event uses each visit's arrival when present, otherwise departure. Filling SHALL NOT overwrite supplied visits.

#### Scenario: Fill an earlier missing visit but not a later one
- **WHEN** update time is 10:00, trip delay is +60 seconds, and visits scheduled at 10:10 and 10:30 both supply arrival/departure predictions at 10:11 and 10:31; missing visits are scheduled at 09:59, 10:05, and 10:20 with equal arrival/departure
- **THEN** the 10:05 visit is added at 10:06; the 09:59 visit is not added because its predicted departure equals 10:00; the 10:20 visit is not added because 10:21 exceeds the earliest supplied prediction; supplied predictions remain intact

### Requirement: Single-prediction filling
For a block represented by a single stop prediction from a one-stop update, missing visits SHALL also receive schedule-plus-deviation predictions after that prediction, provided predicted departure is strictly after update/request time and scheduled arrival does not exceed the last scheduled arrival among trips represented by supplied updates. This SHALL NOT extrapolate indefinitely into later scheduled trips without supporting bounds.

#### Scenario: Fill within the represented trip's end
- **WHEN** update time is 10:00 and the sole supplied visit is scheduled at 10:10 with arrival/departure at 10:11; the represented trip ends at scheduled arrival 10:30 and a missing visit is scheduled at 10:20
- **THEN** deviation is +60 seconds and the missing visit is exported at 10:21

#### Scenario: Do not extrapolate to an unsupported later trip
- **WHEN** the preceding single-prediction fixture also has a later block trip beginning at 10:40 but no supplied update for it
- **THEN** its visits beyond the represented 10:30 schedule bound are not fabricated

### Requirement: Interlining reconstruction and filtering
Predictions SHALL be assembled across eligible block trips before own-route filtering. Each generated or supplied prediction SHALL remain associated with its trip and service instance. Earlier missing visits satisfying bounded filling SHALL not disappear merely because the active vehicle is on another route.

#### Scenario: Sparse downstream route
- **WHEN** update time is 09:00; active trip A on route A supplies a +60-second delay but no stop events; downstream trip B on route B supplies two visits scheduled at 10:10 and 10:30 with predictions 10:11 and 10:31 and has a missing visit scheduled at 10:05; no other trip-level delay is supplied
- **THEN** B includes the reconstructed 10:06 visit and its supplied predictions; route B filtering returns B even while A is active, and route A filtering does not leak B

### Requirement: Delay and next-stop fallback
When eligible active-trip status has no timepoint predictions, the export SHALL provide a minimal update containing trip and route IDs and available trip-level delay. If an eligible next-stop prediction exists, it SHALL include a departure event at effective request time in seconds plus next-stop time offset. Missing next-stop information SHALL NOT require fabricating a stop prediction.

#### Scenario: Next-stop fallback
- **WHEN** active-trip status has no timepoint predictions, a delay, and a next-stop offset of 90 seconds
- **THEN** its update includes the delay and next-stop departure at request time plus 90 seconds

### Requirement: Stop events and standard field fidelity
Available arrival and departure predictions SHALL be exported as epoch-second stop events. Unavailable events SHALL be omitted, not serialized as sentinel timestamps. Service-instance descriptors, stop sequences/relationships, trip/event delays, event times, and uncertainty already retained by current models SHALL survive export, subject to normalization, cancellation reconciliation, and reconstruction. Additional standard-field ingestion expansion is deferred.

#### Scenario: Arrival-only prediction
- **WHEN** a stop has a valid arrival prediction but no departure prediction
- **THEN** arrival time is exported and no sentinel departure time is emitted

#### Scenario: Standard stop metadata
- **WHEN** a selected retained update contains a stop sequence, schedule relationship, event delay/time, and uncertainty
- **THEN** those values survive export without requiring retention of additional standard fields during ingestion

### Requirement: Cancellation reconciliation
Known canceled trip instances SHALL be exported once with schedule relationship CANCELED, their trip and route IDs, and available service date. The same trip instance SHALL NOT also receive a contradictory SCHEDULED update. Cancellation selection SHALL obey the own-trip agency and route filter rules.

#### Scenario: Conflicting ordinary and canceled state
- **WHEN** retained state contains an ordinary update and a known cancellation for the same trip instance
- **THEN** the resulting feed contains a single CANCELED update for that instance, not a SCHEDULED/CANCELED pair

#### Scenario: Unrelated cancellation
- **WHEN** a canceled trip is on route B and the request filters route A or a nonexistent route
- **THEN** that cancellation is not exported

### Requirement: Service-instance descriptors
Trip descriptors SHALL preserve available service-date, start-time, and schedule-relationship information. ADDED trips SHALL include their known service date and applicable start time. Formatting SHALL identify the actual service instance rather than depend accidentally on the server's local time zone.

#### Scenario: Added trip across midnight
- **WHEN** an added trip has a known service date and a start after midnight in its service instance
- **THEN** its descriptor identifies that service date and applicable start time consistently with the instance

### Requirement: Legacy trip entity identity and timestamps
Trip-update entity IDs SHALL use qualified trip ID plus `_` plus effective request time in milliseconds, independently of payload normalization. Only when distinct service instances would collide, IDs SHALL append a service-instance suffix to ensure uniqueness without losing instances. Ordinary updates SHALL carry available associated vehicle IDs and last-update timestamps in epoch seconds.

#### Scenario: Request-dependent identity
- **WHEN** trip `40_trip_A` is exported at time `1791581968000` with default normalization
- **THEN** its entity ID is `40_trip_A_1791581968000` while its payload trip ID is `trip_A`

#### Scenario: Refresh identity
- **WHEN** the same trip is exported with a different effective request time
- **THEN** its entity ID changes with that time rather than being replaced by a stable-ID convention

#### Scenario: Colliding service instances
- **WHEN** two eligible instances share a trip ID but differ in service date or start time
- **THEN** both are exported with distinct service-instance suffixes on their otherwise colliding entity IDs, and their predictions and cancellation states remain separate

### Requirement: OBA headsign extensions
Trip exports SHALL populate OBA-compatible `oba_trip_update.tripHeadsign` for an available active-trip headsign and `oba_stop_time_update.stopHeadsign` for available static stop headsigns. Both binary and text formats SHALL preserve these custom extensions using the legacy wire definitions, rather than substitute nonstandard fields in the core GTFS-RT schema.

#### Scenario: Trip and stop headsigns
- **WHEN** an active trip and its predicted stop have available trip and stop headsigns
- **THEN** an extension-aware decoder recovers both values from the binary response and the text response represents both extensions

#### Scenario: Standard decoder compatibility
- **WHEN** a standard GTFS-RT decoder without OBA extension definitions reads the binary feed
- **THEN** it can decode the standard trip-update contents without needing to interpret the headsign extensions

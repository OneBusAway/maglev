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

### Requirement: Legacy scheduled-trip matching reference
Ordinary scheduled-trip block matching SHALL use the trip-update timestamp when supplied, otherwise the trip-feed header timestamp (zero when absent). For updates grouped by vehicle, the first update in retained source order SHALL supply the matching trip and reference time. Export request time SHALL NOT reassign retained block associations. Matching SHALL occur before route filtering.

#### Scenario: Update timestamp takes precedence
- **WHEN** an ordinary update timestamp is Tuesday 01:10, its feed header timestamp is Tuesday 12:00, and the export request time is Wednesday 12:00
- **THEN** initial block matching uses Tuesday 01:10, not the header or export request time

#### Scenario: Feed timestamp fallback
- **WHEN** an ordinary update has no timestamp and its feed header timestamp is Tuesday 01:10
- **THEN** matching uses Tuesday 01:10; if the header timestamp is also absent, the matching reference is the Unix epoch rather than current or request time

### Requirement: Legacy candidate service dates
On a matching-cache miss, candidate service dates SHALL use the timezone of the matched trip's agency (its route's agency), not the server's local timezone. Before 04:00 they SHALL be yesterday then today; from 04:00 through 20:59:59, today only; from 21:00, today then tomorrow. This fixed window SHALL NOT expand to all possible overlapping service days. Legacy used the server's timezone, which matched only because legacy servers ran in the agency's timezone; agency-local dates keep matching correct on servers running in UTC or serving several timezones.

#### Scenario: Overnight previous-day service
- **WHEN** matching reference time is Tuesday 01:10 agency-local and Monday's active block contains the trip with a first departure at 25:00
- **THEN** Monday is checked before Tuesday, and Monday's block is selected if its adjusted block start is positive

#### Scenario: Candidate-window boundaries
- **WHEN** reference time is 03:59:59, 04:00, 20:59:59, or 21:00 agency-local
- **THEN** candidates are respectively yesterday/today, today only, today only, and today/tomorrow

#### Scenario: Agency-local date selection
- **WHEN** the server runs in UTC and the matching reference time is Tuesday 10:00 UTC, which is Tuesday 03:00 for an agency in America/Los_Angeles
- **THEN** the search checks Monday then Tuesday in the agency's timezone, rather than only the server-local Tuesday

### Requirement: Legacy first-match block resolution
For each candidate date in order, matching SHALL require an active block containing the trip and a static first departure for that trip. It SHALL accept the first candidate with a positive adjusted block start, without nearest-instance scoring or a uniqueness check. Supplied ordinary-trip `start_date` and `start_time` SHALL NOT select the block instance. If no candidate qualifies, that ordinary update SHALL NOT establish an exportable block association.

#### Scenario: Overlapping active days
- **WHEN** reference time is Tuesday 01:10 and both Monday and Tuesday have qualifying blocks for the ordinary trip
- **THEN** Monday wins even if Tuesday is closer to a supplied stop-event time

#### Scenario: Supplied descriptor does not override matching
- **WHEN** an ordinary update supplies Tuesday's `start_date` and a different `start_time`, but Monday is the first qualifying candidate
- **THEN** its block association and reconstruction use Monday's resolved instance, not the supplied hints

#### Scenario: Unmatched ordinary trip
- **WHEN** the trip is absent from static data or no candidate date has a qualifying active block
- **THEN** the update does not establish an ordinary block association, even if it contains absolute stop-event times; unrelated eligible updates remain exportable

### Requirement: Legacy static block-start derivation
Matching SHALL take the trip's first static departure as its trip start. Adjusted block start SHALL equal the block's first departure plus this trip start minus the matching block-trip's first departure. A negative result SHALL fall back to the block's first departure; a resulting zero or negative value SHALL NOT qualify. An incoming ordinary-trip start time SHALL NOT provide a frequency-instance offset.

#### Scenario: Zero block start
- **WHEN** a candidate's adjusted block start is exactly zero
- **THEN** that candidate is rejected and the next candidate, if any, is checked

#### Scenario: Frequency start hint ignored
- **WHEN** an ordinary frequency-based trip supplies a start time different from its first static departure
- **THEN** block matching uses the static departure rather than deriving a distinct departure instance from that hint

### Requirement: Legacy matching-cache lifecycle
Matching SHALL maintain a per-source cache keyed only by qualified static trip ID, not service date, start time, or vehicle ID. The first result, including an unresolved result, SHALL be reused for 30 minutes from insertion without extending expiry on reads. Static GTFS replacement SHALL clear this cache. Retained associations SHALL be published immutably; export reads SHALL NOT rematch them against request time.

#### Scenario: Cached association across date-window change
- **WHEN** an ordinary trip resolves to Monday at Tuesday 03:59 and the same source applies another update for it at Tuesday 04:01 before cache expiry
- **THEN** Monday's cached association is reused rather than rerunning the today-only search

#### Scenario: Cached failure and invalidation
- **WHEN** matching fails for a trip and a later update arrives before the cached result expires
- **THEN** the unresolved result is reused; after expiry or static GTFS replacement, a subsequent update reruns matching

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

### Requirement: Block schedule deviation selection
For each block service instance, supplied updates SHALL be considered in scheduled block-trip order. The last supplied trip-level delay SHALL determine block deviation. Without a trip-level delay, deviation SHALL be predicted event time minus its scheduled time for the supplied event closest to effective request time. A future event SHALL win a tie with a past event. Absolute event time SHALL take precedence over scheduled time plus event delay.

#### Scenario: Multiple trip-level delays
- **WHEN** block trip A supplies delay +60 seconds and later block trip B supplies delay +120 seconds
- **THEN** block deviation is +120 seconds regardless of source-feed array order or which route is requested

#### Scenario: Trip-level delay takes precedence
- **WHEN** a block supplies trip-level delay +60 seconds and a stop event implies deviation +120 seconds
- **THEN** block deviation is +60 seconds, while the supplied stop prediction remains unchanged

#### Scenario: Closest supplied event
- **WHEN** effective request time is 10:00, a past event at 09:59 implies deviation +30 seconds, and a future event at 10:05 implies deviation +90 seconds, with no trip-level delay
- **THEN** block deviation is +30 seconds; a future event does not win merely because it is future

#### Scenario: Future event wins equal-distance tie
- **WHEN** effective request time is 10:00, a past event at 09:59 implies deviation +30 seconds, and a future event at 10:01 implies deviation +90 seconds, with no trip-level delay
- **THEN** block deviation is +90 seconds

#### Scenario: Absolute time overrides event delay
- **WHEN** a supplied event has scheduled time 10:10, absolute time 10:12, and delay +60 seconds
- **THEN** its prediction is 10:12 and its deviation candidate is +120 seconds, not +60 seconds

### Requirement: Legacy prediction-filling reference time
Before filling each block trip, updates through that trip SHALL be scanned in scheduled block-trip order and retained source order within each trip. Every supplied update timestamp SHALL replace the carried timestamp, including zero. The last carried nonzero timestamp SHALL bound filling; otherwise the originating trip-feed header time (zero when absent) SHALL apply. Later trips SHALL NOT retroactively change this bound. Export request time SHALL NOT substitute.

#### Scenario: Carry an earlier update timestamp
- **WHEN** trip A supplies timestamp 09:00 and delay +60 seconds, and later trip B supplies predictions but no timestamp, with a missing visit predicted at 10:06 before B's earliest supplied event
- **THEN** B's filling uses 09:00, so that visit qualifies even if export request time is 10:20

#### Scenario: Current trip timestamp replaces carried time
- **WHEN** the same fixture gives B timestamp 10:07
- **THEN** B's missing visit predicted at 10:06 is not filled because B's timestamp replaces A's 09:00

#### Scenario: Later trip does not change an earlier bound
- **WHEN** B is filled using 09:00 and later trip C supplies timestamp 11:00
- **THEN** C's timestamp does not remove B's already-qualified reconstructed visit

#### Scenario: Zero resets to feed time
- **WHEN** A supplies timestamp 09:00, B supplies timestamp zero, and the originating trip-feed header time is 10:07
- **THEN** B's filling uses 10:07 rather than retaining A's 09:00; if the header timestamp is absent, the bound is the Unix epoch

### Requirement: Bounded filling before supplied predictions
When a block has supplied stop predictions and a trip-level delay, missing scheduled visits SHALL receive scheduled arrival/departure plus block deviation only if predicted departure is strictly after that trip's legacy filling-reference time and strictly before the earliest existing predicted event. Earliest event uses each visit's arrival when present, otherwise departure. Filling SHALL NOT overwrite supplied visits.

#### Scenario: Fill an earlier missing visit but not a later one
- **WHEN** update time is 10:00, trip delay is +60 seconds, and visits scheduled at 10:10 and 10:30 both supply arrival/departure predictions at 10:11 and 10:31; missing visits are scheduled at 09:59, 10:05, and 10:20 with equal arrival/departure
- **THEN** the 10:05 visit is added at 10:06; the 09:59 visit is not added because its predicted departure equals 10:00; the 10:20 visit is not added because 10:21 exceeds the earliest supplied prediction; supplied predictions remain intact

### Requirement: Legacy single-prediction mode
After processing each block trip's supplied updates, single-prediction mode SHALL activate if the accumulated prediction collection has exactly one record and the collection's first input update in retained source order has exactly one stop update. Once active, it SHALL remain active for that block collection's traversal. Activation SHALL NOT require a trip-level delay and SHALL use available selected deviation. Counts SHALL include prediction records already reconstructed for earlier trips.

#### Scenario: Mode without trip-level delay
- **WHEN** the first input update has one stop update yielding one prediction and no supplied trip-level delay exists
- **THEN** single-prediction mode activates using the stop-derived deviation

#### Scenario: First input update prevents activation
- **WHEN** the first input update has two stop updates, only one of which yields a prediction, and no trip-level delay exists
- **THEN** the single accumulated prediction alone does not activate single-prediction mode

#### Scenario: Mode remains active
- **WHEN** single-prediction mode activates for trip A and subsequent processing of trip B adds more prediction records
- **THEN** the mode remains active through B; it is not switched off because the collection now has multiple records

### Requirement: Single-prediction filling
While single-prediction mode is active, missing visits SHALL receive schedule-plus-deviation predictions when departure is strictly after the legacy filling-reference time and either before the earliest accumulated predicted event or scheduled arrival is at most the end bound. The end bound SHALL be the greatest scheduled arrival in trips processed so far having at least one supplied stop update. Delay-only updates SHALL NOT extend it. Supplied visits SHALL NOT be overwritten.

#### Scenario: Fill within the represented trip's end
- **WHEN** filling-reference time is 10:00, no trip-level delay exists, and the first input update's sole supplied visit is scheduled at 10:10 with arrival/departure at 10:11; the trip ends at scheduled arrival 10:30 and a missing visit is scheduled at 10:20
- **THEN** deviation is +60 seconds and the missing visit is exported at 10:21 without requiring a trip-level delay

#### Scenario: Earlier visit without trip-level delay
- **WHEN** the same single-prediction fixture has a missing visit scheduled at 10:05
- **THEN** it is filled at 10:06, before the supplied 10:11 prediction, despite the absence of trip-level delay

#### Scenario: Delay-only downstream trip does not extend the bound
- **WHEN** single-prediction mode is active, its represented stop-update trip ends at scheduled arrival 10:30, and a later trip supplies only a trip-level delay with no stop updates
- **THEN** that delay-only trip does not advance the single-prediction end bound beyond 10:30

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
Available arrival/departure predictions SHALL be exported as epoch-second events; unavailable events SHALL be omitted, not sentinel timestamps. Retained instance descriptors, stop sequences/relationships, trip/event delays, event times, and uncertainty SHALL survive export, subject to normalization, legacy scheduled-trip instance resolution, cancellation reconciliation, and reconstruction. Additional standard-field retention is deferred.

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
Ordinary scheduled-trip descriptors SHALL identify their legacy-resolved service instance, not conflicting supplied date/start-time hints. Their resolved service date SHALL use the agency-local date chosen by matching. Available schedule relationships SHALL be preserved. ADDED and DUPLICATED trips SHALL remain separate from ordinary static-trip matching and preserve their known instance date and applicable start time without reinterpretation by the matcher.

#### Scenario: Resolved ordinary descriptor
- **WHEN** an ordinary update supplies Tuesday's service date but resolves to Monday's block instance
- **THEN** any exported service date identifies Monday, and any exported start time comes from the resolved static instance rather than the conflicting hint

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
- **WHEN** two eligible resolved instances share a trip ID but differ in service date or start time, such as independently resolved sources or added/duplicated instances; differing supplied hints alone do not create distinct ordinary instances
- **THEN** both are exported with distinct service-instance suffixes on their otherwise colliding entity IDs, and their predictions and cancellation states remain separate

### Requirement: OBA headsign extensions
Trip exports SHALL populate OBA-compatible `oba_trip_update.tripHeadsign` for an available active-trip headsign and `oba_stop_time_update.stopHeadsign` for available static stop headsigns. Both binary and text formats SHALL preserve these custom extensions using the legacy wire definitions, rather than substitute nonstandard fields in the core GTFS-RT schema.

#### Scenario: Trip and stop headsigns
- **WHEN** an active trip and its predicted stop have available trip and stop headsigns
- **THEN** an extension-aware decoder recovers both values from the binary response and the text response represents both extensions

#### Scenario: Standard decoder compatibility
- **WHEN** a standard GTFS-RT decoder without OBA extension definitions reads the binary feed
- **THEN** it can decode the standard trip-update contents without needing to interpret the headsign extensions

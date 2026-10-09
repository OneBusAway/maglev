# Design

## Context

See [proposal.md](proposal.md) for motivation and the four [capability specs](specs/) for required behavior.

At inspected revision `601597759171a7b7df98799d13687870d910f4f8`, Maglev retains per-feed realtime state and publishes an immutable merged snapshot. Incoming stop updates are retained rather than replaced with a materialized block-wide prediction collection. Existing REST helpers derive block schedule deviation, stop delays, next-stop information, and arrival/departure predictions on demand.

Legacy reference: OneBusAway application modules tag `v2.7.1`, revision `095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a`. Useful paths relative to that repository:

- `onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/gtfs_realtime/`: shared action and the three exporters.
- `onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/realtime/gtfs_realtime/GtfsRealtimeTripLibrary.java`: block update collection and conditional schedule-based prediction filling, particularly lines 900–1111 at the reference revision.
- `onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/beans/TripStatusBeanServiceImpl.java`: conversion of block-wide prediction records into trip-status beans.

The relevant legacy pipeline is incoming realtime updates → block processing and conditional prediction filling → trip status → export. The serializer itself does not generate downstream predictions, but reproducing only serialization would miss output generated earlier in this same pipeline.

Existing `go-gtfs` normalized vehicles retain bearing, speed, occupancy, and other useful standard fields. Their vehicle descriptor has ID, label, and license plate but no explicit agency field. Normalized alerts retain cause/effect and translations, but the inspected dependency's alert model has no severity field. The enumerated supported fields must survive ingestion even where current DTOs omit them. Field fidelity and agency ownership therefore require deliberate handling rather than assuming every needed value exists in a convenience DTO. Source references below support the investigation; the specs contain the behavior and worked examples needed to implement it.

## Goals / Non-Goals

**Goals**

- One shared request and serialization boundary, with independently understandable vehicle, alert, and trip builders.
- Reuse existing realtime/schedule reasoning while avoiding accidental changes to JSON endpoint behavior.
- Preserve protobuf field presence and OBA extension wire compatibility.
- Make trip reconstruction and intentional legacy departures testable with small controlled fixtures.

**Non-Goals**

- A raw upstream-feed proxy, a historical realtime archive, or a new statistical forecasting engine.
- Byte-for-byte Java protobuf text formatting, legacy HTTP defects, or reproduction of unapproved prediction quirks.
- Response caching or a deployment-specific CDN configuration.

## Decisions

### Shared request boundary; endpoint-specific selection

Use a shared export request representation for the agency ID, effective time, raw route filter, normalization option, and output format. Reuse existing API-key, rate-limit, version, and error mechanisms. Apply `Cache-Control: no-store` at a boundary that also covers rejected export requests. Use `VersionValidationMiddleware` and `utils.ParseTimeParameter` rather than separate export parsers for version/time. Shared time parsing accepts epoch milliseconds and two date-string forms, defaults absent/empty values to the injected clock, treats zero as the epoch, and truncates numeric fractional seconds. Apply the additional pre-epoch 400 guard before encoding an unsigned feed timestamp. Parse `removeAgencyIds` with an absent/empty default of true, case-insensitive true/false, and 400 for other values.

Keep raw ID plus resolved owning agency as an identity pair through selection and reconstruction. Add qualification when requested or remove only qualification known from provenance; never interpret the first underscore of an arbitrary upstream ID as an agency boundary. Normalize only when building payloads.

**Rationale:** this keeps query semantics identical across all six routes and prevents stripping agency information before ownership checks.

**Alternative:** three independent handlers each parse and normalize everything. This invites drift, particularly the legacy alert normalization exception that this change intentionally corrects.

### One feed builder per endpoint; two serializers

Each endpoint builds a complete protobuf FeedMessage; binary and text serializers consume that same message. Use the existing Go protobuf runtime for standard serialization. Retain the enumerated supported upstream fields and their optional presence through ingestion, extending DTOs or narrow typed snapshot metadata where needed. Do not synthesize optional zero values just because a DTO uses a scalar default.

**Rationale:** text becomes a debugging representation of the same behavior, not a separate implementation. Assembling the message before writing also prevents a serialization error from leaving a partial successful response.

**Alternative:** format-specific builders or hand-written protobuf text. Both duplicate logic and make extensions harder to verify.

### Snapshot-based selection with ownership resolution

Build against one consistent realtime view and use batched static lookups for missing route, trip, stop, and agency associations. Do not mutate published snapshots or acquire writer locks while performing schedule queries. Avoid per-entity SQL lookups when batch access exists.

Vehicle selection must not simply reuse `VehiclesForAgencyID`: that helper selects by serving route and excludes tripless vehicles, whereas this contract uses vehicle ownership. Preserve or resolve genuine ownership from qualified vehicle identity and source provenance; do not silently reinterpret route agency as ownership. A single-agency source can establish ownership for a raw vehicle ID; a multi-agency source alone cannot. Exclude unresolved vehicles, and exclude vehicles without an update timestamp instead of assigning ingestion or request time. If current ingestion loses necessary provenance, extend the retained representation narrowly.

Similarly, alert selection cannot rely only on an agency-only alert index: route-, trip-, and stop-specific selectors can establish affected agency. Resolve selector ownership before normalization, and fill a missing `agency_id` only when that ownership is unambiguous. Preserve explicit agency IDs and retain entity-specific selector fields; adding agency context must not broaden a route/stop selector into an agency-only notice. This supports the inspected iOS decoder, which requires an agency ID. Select and deduplicate whole records while preserving complete selector scope, including other agencies' selectors.

**Alternative:** reuse convenience indexes without checking their semantics. That would violate tripless-vehicle and affected-agency behavior.

### Reconstruct predictions using shared schedule and delay reasoning

Use Maglev's existing helpers as the starting point, not as proof that all legacy filling behavior is already reproduced:

- `trip_updates_helper.go`: ordered block trip updates, block delay selection, schedule matching, and stop delays.
- `trips_helper.go`: trip status and next-stop reasoning.
- `arrival_and_departure_for_stop_handler.go`: prediction lookup, prior-stop propagation, and trip-delay fallback.

Separate prediction calculation from HTTP response construction where necessary. The export needs a per-trip/per-stop prediction collection; repeatedly issuing JSON requests or wrapping JSON payloads is not an appropriate adapter.

Implement the filling rules and worked examples in the trip spec directly; legacy code is supporting evidence, not a substitute for those requirements. Assemble supplied predictions by block-trip order, matching stops by service instance and visit. Absolute event time takes precedence over schedule plus event delay. For block deviation, reuse the established ordering: the last supplied trip-level delay wins; without one, derive deviation from the stop prediction closest to the reference time, preferring a future prediction on a tie.

With supplied predictions and a trip-level delay, fill missing visits at scheduled times plus deviation only where predicted departure is after the update/reference time and before the earliest existing predicted event. The single-prediction case additionally allows filling later visits within the schedule end bound represented by supplied updates. Match missing visits by trip and sequence rather than a bare stop ID, so loop visits are not accidentally collapsed. Static membership alone does not justify unconstrained downstream extrapolation.

Carry service-instance identity and stop sequence through reconstruction, then apply each resulting trip's own agency and route selection. Do not first discard blocks solely because the active route fails the requested filter. Resolve cancellation conflicts before ordinary updates are serialized. Scope any reuse/extraction so existing JSON behavior is protected by regression tests.

**Alternative:** serialize only incoming stop updates. Rejected because it loses schedule-derived predictions that are part of the agreed scope. A wholesale Java prediction subsystem port is also unnecessary: much of the schedule and delay reasoning already exists, and identical internal architecture is not required.

### OBA headsign compatibility through extension-aware descriptors

Keep the OBA headsign schema and generated Go bindings in a dedicated Maglev-local internal package. Do not add OBA-specific types to `go-gtfs` or require a separate-repository change for headsign support. Generate Go bindings using `protoc` and `protoc-gen-go`, importing the existing `github.com/OneBusAway/go-gtfs/proto` types rather than generating another copy of standard GTFS-RT messages.

Use the public [OBA schema](https://github.com/OneBusAway/onebusaway-gtfs-realtime-api/blob/b134b5428a2f63b511b80f2a6c534e0e8028ad54/src/main/proto/com/google/transit/realtime/gtfs-realtime-OneBusAway.proto) as the authoritative wire definition. Retain its license/attribution. The local schema needs the two headsign-bearing messages and their extensions, not registration of unrelated OBA extensions:

- `transit_realtime.OneBusAwayTripUpdate.tripHeadsign` is string field 3; `transit_realtime.oba_trip_update` extends TripUpdate at field 1000.
- `transit_realtime.OneBusAwayStopTimeUpdate.stopHeadsign` is string field 1; `transit_realtime.oba_stop_time_update` extends TripUpdate.StopTimeUpdate at field 1000.

Adjust only Go package/import placement as needed; preserve protobuf names, field numbers, types, and extendees. The pinned standard descriptors already support the required extension ranges, and the existing NYCT stop extension uses field 1001. Import the local generated package where encoding/decoding requires extension registration so named protobuf text output works.

An isolated runtime probe with these descriptors confirmed binary encoding, ordinary decoding without OBA registration, extension-aware decoding, and named protobuf-text roundtripping using Maglev's current dependency. This verifies runtime compatibility; generation of the actual local bindings remains implementation work.

Do not substitute ordinary custom fields in the standard schema or assume that appending unknown binary fields alone gives named `.pbtext` extensions. Test with an extension-aware decoder as well as an ordinary GTFS-RT decoder. Match static stop headsigns by trip and stop visit; avoid an ambiguous first-stop-ID match on loops.

**Alternatives:** shared-library OBA bindings would introduce unnecessary cross-repository coordination and broaden a reusable dependency with application-specific compatibility. Omitting extensions or preserving only opaque bytes would not meet the binary and named-text headsign contract.

### Legacy entity-ID conventions, separate from reconciliation identity

Keep entity IDs as specified, but use richer internal service-instance keys for cancellation reconciliation and prediction grouping. Request-time entity IDs do not themselves distinguish multiple service instances of the same trip. Deduplicate logical alerts before allocating fallback IDs, and reserve existing alert IDs before allocating count-based fallback values.

The ordinary trip ID convention is preserved; do not quietly replace it with stable IDs. When distinct service instances would produce the same entity ID, append deterministic service-instance suffixes to the colliding IDs only. Use available service date and start time to distinguish those instances; normal IDs remain unchanged.

Source inspection confirms this can arise in retained state: `go-gtfs` parses and groups by `TripID`, which includes start date and start time, and Maglev's merged trip list preserves those records. However, `rebuildMergedRealtimeLocked` builds `tripLookup` using only the trip ID string, with the last record winning. `GetTripUpdateByID` and even `GetTripUpdatesForTrip` therefore expose at most one record for that string. Export grouping must use the full snapshot records and service-instance keys rather than depend on these lossy lookups. This is source-supported, not an observed live-feed collision.

**Alternative:** use entity IDs as the sole internal grouping key. Legacy's observed duplicate canceled/scheduled pair demonstrates why that is unsafe.

### Code reuse map

The following source-inspected candidates distinguish direct reuse from adaptations. Paths are relative to the Maglev repository. Reuse the underlying behavior where it fits; a similarly named JSON helper is not necessarily an export builder.

| Area | Existing code | Reuse and semantic boundary |
| --- | --- | --- |
| Authorization and version | `internal/restapi/routes.go`: `rateLimitAndValidateAPIKey`; `version_middleware.go`: `VersionValidationMiddleware`; `errors.go` | Reuse request protection and normal error handling. Preserve auth-before-rate-limit ordering. Ensure the no-store boundary covers globally rejected export requests too. |
| Time and timezone | `internal/utils/api.go`: `ParseTimeParameter`; `internal/restapi/timezone_helper.go`: `loadAgencyLocation` | Direct reuse with the export's additional pre-epoch guard. Use the injected clock and resolved agency timezone; do not introduce a Java-specific date parser. |
| Boolean parsing | `internal/utils/api.go`: `ParseBoolParam` | Reuse case-insensitive true/false validation, but adapt the empty-value case locally: this helper returns false for empty input, whereas the export contract defaults empty to true. Do not change existing callers' semantics. |
| ID qualification and path extraction | `internal/utils/api.go`: `FormCombinedID`, `ExtractAgencyIDAndCodeID`; `internal/utils/http.go`: `ExtractIDFromParams`; `internal/restapi/id_helpers.go` | Construct qualified identities with existing utilities; split only IDs known to be qualified. Path extraction currently strips `.json`, not `.pb`/`.pbtext`; add format-aware extraction before shared ID validation, or register paths with agency/format separated. The endpoint path uses a plain agency ID, not a combined entity ID. |
| Cache policy | `internal/restapi/caching_middleware.go`: `CacheControlMiddleware` | A nonpositive duration already supplies `no-cache, no-store, must-revalidate`, including errors; this satisfies the no-store policy without duplicating caching logic. Do not add `etagStatic` or `ETagMiddleware` to export routes. |
| Realtime reads | `internal/gtfs/realtime.go`: immutable merged snapshot, `GetRealTimeTrips`, `GetRealTimeVehicles`; `gtfs_manager.go`: `GetAllTripUpdates` | Preserve the snapshot publication model. Separate accessor calls can observe different refreshes, so consider a narrow read-only snapshot accessor for one consistent export view, including full alert records and provenance. Avoid trip-ID-only lookups for instance grouping. |
| Agency resolution | `internal/gtfs/realtime.go`: `buildRouteAgencyMap`, `tripAgencyIDs`, `informedEntityMatchesAgency` | Reuse batch route/trip resolution patterns rather than duplicate SQL. These are package-private ingestion helpers: extraction/shared placement may be needed. Existing alert matching handles agency/route and trip route, not stop-only ownership or static lookup for a trip selector without route; complete those gaps for export selection and enrichment. |
| Batch static data | `internal/utils/batches.go`: `QueryInBatches`, `QueryInBatchesReserving`; `gtfsdb/query.sql`: `GetTripsByIDs`, `GetRoutesByIDs`, `GetStopsByIDs`, `GetStopTimesForTripIDs`, `GetTripsByBlockIDs` | Reuse generated queries and bind-limit batching. `GetTripsByBlockIDs` also binds service IDs: reserve their slots. Static trips and stop times already expose trip/stop headsigns, so retrieving those values does not require new persistence. |
| Block and stop calculations | `internal/restapi/trip_updates_helper.go`: `loadScheduledForTrips`, `matchScheduleEntryBySequence`, `stuPredictedFromEvent`, `stuDeviationPicker`, `sequenceIdentifiesVisit`, `StopDelays` | Strong reuse candidates for schedule matching, event conversion, delay selection, and loop visits. Pass explicit instance-aware snapshot inputs to extracted calculation logic rather than let it refetch via lossy trip-ID-only lookups. |
| Existing prediction wrappers | `GetScheduleDeviationForBlock`, `GetStopDelaysFromTripUpdates`; `getPredictedTimes` in `arrival_and_departure_for_stop_handler.go`; trip/next-stop logic in `trips_helper.go` | Reuse calculations and regression coverage, not wrappers unchanged. They read current manager state, some use only one trip record, block deviation applies a one-hour guard, and per-stop fallback is not identical to bounded export filling. Preserve existing JSON behavior when extracting shared logic. |
| Test setup | `internal/restapi/http_test.go`: `createTestApiWithClock`, `createTestApiWithFeed`; `internal/gtfs/gtfs_manager_mock.go`: vehicle options, trip updates, and alerts | Reuse deterministic clock/static-feed setup and mock fixtures, including missing timestamp/trip cases. `callAPIHandler` and `serveApiAndRetrieveEndpoint` decode JSON: reuse their HTTP setup pattern with a binary/text response reader, not the JSON decoder. Use full HTTP middleware for auth, version, cache, and content-type tests. |

Do not force-fit these superficially related helpers:

- `VehiclesForAgencyID` and ingestion `filterVehiclesByAgency` select by active route and omit tripless vehicles, not by the required vehicle ownership.
- `StaleDetector` uses an absolute gap greater than 15 minutes and can accept a missing timestamp when position exists. It is not the export's one-sided, epoch-second 600-second eligibility check.
- `BuildSituationReferences` in `reference_utils.go` produces a JSON model, keeps only the first translation, and derives severity from effect. It cannot preserve protobuf alert translations or supplied severity.
- The current alert index skips empty alert IDs and is not a full-record export source; fallback-ID handling needs access to retained records rather than only indexed results.

No reusable outbound FeedMessage builder or OBA headsign extension encoder was found in the inspected application code. Existing `go-gtfs/proto` messages and the protobuf runtime remain reusable foundations; endpoint mapping, bounded prediction assembly, instance cancellation reconciliation, and extension-aware serialization are the new integration work.

### Suggested delivery boundaries

Keep the shared contract in one coherent specification, but recommend independently reviewable implementation slices:

1. Shared request/format infrastructure and vehicle positions.
2. Alert export, ownership resolution, and field fidelity.
3. Trip export, prediction reconstruction, reconciliation, and headsign extensions.

The boundaries are recommendations, not a mandatory PR count. Further split tightly scoped refactors or protobuf binding work when useful. Keep related changes understandable without unrelated cleanup. Maglev's contribution guidelines prefer small PRs, so avoid making the simpler exports wait on a large combined trip implementation.

## Risks / Trade-offs

- [DTO field loss] → Audit normalized-field coverage; preserve needed source information through a narrow typed representation. Do not proxy raw feeds to solve selection or fidelity.
- [Vehicle ownership unavailable in current state] → Preserve ownership provenance through ingestion; do not infer ownership from active route alone. Tripless and cross-agency fixtures must exercise the export path independently of ingestion filtering.
- [Prediction fill differs from existing REST helpers] → Controlled sparse/block fixtures must compare expected predictions against pinned legacy behavior, with intentional departures documented. Broad helper reuse without semantic comparison is insufficient.
- [Request time changes header and trip entity IDs] → No response caching; fixed-time fixtures make comparisons reproducible. Request time does not recreate earlier retained state.
- [Multi-feed conflicts or repeated trip instances] → Reconcile by logical alert/trip-instance identity before serialization; apply the collision-only entity-ID suffix exception rather than overwrite records.
- [Optional scalar defaults masquerade as supplied data] → Retain supplied supported fields and their presence through ingestion; do not silently omit a value because the current DTO discarded it.
- [Agency-wide reconstruction cost] → Batch schedule lookups and build each block's prediction collection once per response; do not hold realtime writer locks during that work.
- [Legacy source behavior exceeds tested coverage] → Separate source-supported scenarios from controlled live evidence. Added trips, multi-zone service, and isolated downstream fixtures need targeted tests, not assumptions from the public feed.

## Migration Plan

This adds routes without replacing existing JSON APIs. No destructive database migration is proposed. Add narrow provenance/field-retention changes only if needed for the contract; verify that JSON outputs and feed ingestion continue to behave as before. Each delivered endpoint must pass its shared-contract and endpoint-specific scenarios before deployment.

Use controlled matching GTFS/GTFS-RT fixtures for freshness, sparse updates, interlining, cancellations, translations, and extensions. Parse binary and text outputs semantically; do not assert unstable ordering or Java-specific whitespace. Public CDN cache hits are not evidence of origin filtering or authorization. Production/legacy response comparisons should distinguish intentional departures from regressions.

Rollback is reverting the new route registration and implementation; preserve existing realtime configuration and data. No new persistence or cache service is required.

## Open Questions

- Where should narrowly scoped ownership/field-retention support live: within Maglev or in the shared `go-gtfs` dependency? Either placement must meet the same contract and preserve existing consumers.

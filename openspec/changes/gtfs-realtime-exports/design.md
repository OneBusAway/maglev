# Design

## Context

See [proposal.md](proposal.md) for motivation and the four [capability specs](specs/) for required behavior.

At inspected revision `601597759171a7b7df98799d13687870d910f4f8`, Maglev retains per-feed realtime state and publishes an immutable merged snapshot. Incoming stop updates are retained rather than replaced with a materialized block-wide prediction collection. Existing REST helpers derive block schedule deviation, stop delays, next-stop information, and arrival/departure predictions on demand.

Legacy reference: OneBusAway application modules tag `v2.7.1`, revision `095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a`. Useful paths relative to that repository:

- `onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/gtfs_realtime/`: shared action and the three exporters.
- `onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/realtime/gtfs_realtime/GtfsRealtimeTripLibrary.java`: block update collection and conditional schedule-based prediction filling, particularly lines 900–1111 at the reference revision.
- `onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/beans/TripStatusBeanServiceImpl.java`: conversion of block-wide prediction records into trip-status beans.

The relevant legacy pipeline is incoming realtime updates → block processing and conditional prediction filling → trip status → export. The serializer itself does not generate downstream predictions, but reproducing only serialization would miss output generated earlier in this same pipeline.

Existing `go-gtfs` normalized vehicles retain bearing, speed, occupancy, and other useful standard fields. Their vehicle descriptor has ID, label, and license plate but no explicit agency field. Normalized alerts retain cause/effect and translations, but the inspected dependency's alert model has no severity field. Export the applicable standard values these models already retain; adding retention for discarded standard fields such as alert severity is deferred. Agency ownership provenance is a separate selection requirement and remains in scope. Source references below support the investigation; the specs contain the behavior and worked examples needed to implement it.

## Goals / Non-Goals

**Goals**

- One shared request and serialization boundary, with independently understandable vehicle, alert, and trip builders.
- Reuse existing realtime/schedule reasoning while avoiding accidental changes to JSON endpoint behavior.
- Preserve standard values and optional presence already retained in the current models, plus OBA extension wire compatibility.
- Make trip reconstruction and intentional legacy departures testable with small controlled fixtures.

**Non-Goals**

- A raw upstream-feed proxy, a historical realtime archive, or a new statistical forecasting engine.
- Byte-for-byte Java protobuf text formatting, legacy HTTP defects, or reproduction of unapproved prediction quirks.
- Response caching or a deployment-specific CDN configuration.
- Expanding standard-field ingestion/model coverage, including alert severity. This does not exclude required agency provenance or locally generated OBA headsign bindings.

## Decisions

### Shared request boundary; endpoint-specific selection

Use a shared export request representation for the agency ID, effective time, raw route filter, normalization option, and output format. Reuse existing API-key, rate-limit, version, and error mechanisms. Apply `Cache-Control: no-store` at a boundary that also covers rejected export requests. Use `VersionValidationMiddleware` and `utils.ParseTimeParameter` rather than separate export parsers for version/time. Resolve date strings in the requested agency's timezone, or UTC when the agency is unknown. Shared time parsing accepts epoch milliseconds and two date-string forms, defaults absent/empty values to the injected clock, treats zero as the epoch, and truncates numeric fractional seconds. Apply the additional pre-epoch 400 guard before encoding an unsigned feed timestamp. Parse `removeAgencyIds` with an absent/empty default of true, case-insensitive true/false, and 400 for other values.

Keep raw ID plus resolved owning agency as an identity pair through selection and reconstruction. For trip, route, and stop IDs, add qualification when requested or remove only known qualification; preserve arbitrary raw underscores. Vehicle IDs deliberately follow the legacy exception in all exports: split at the first underscore, or use the matched block's agency when there is no underscore. Normalize only when building payloads. Do not introduce a feed setting for vehicle-ID qualification.

**Rationale:** this keeps query semantics identical across all six routes and prevents stripping agency information before ownership checks.

**Alternative:** three independent handlers each parse and normalize everything. This invites drift, particularly the legacy alert normalization exception that this change intentionally corrects.

### One feed builder per endpoint; two serializers

Each endpoint builds a complete protobuf FeedMessage; binary and text serializers consume that same message. Use the existing Go protobuf runtime for standard serialization. Map applicable standard values and optional presence already retained by the current models. Do not expand ingestion DTOs or add raw-field storage merely to retain additional standard fields in this feature. Do not synthesize optional zero values just because a DTO uses a scalar default.

**Rationale:** text becomes a debugging representation of the same behavior, not a separate implementation. Assembling the message before writing also prevents a serialization error from leaving a partial successful response.

**Alternative:** format-specific builders or hand-written protobuf text. Both duplicate logic and make extensions harder to verify.

### Snapshot-based selection with ownership resolution

Build against one consistent realtime view and use batched static lookups for missing route, trip, stop, and agency associations. Do not mutate published snapshots or acquire writer locks while performing schedule queries. Avoid per-entity SQL lookups when batch access exists.

Vehicle selection must not simply reuse `VehiclesForAgencyID`: that helper selects by serving route and excludes tripless vehicles. Use legacy-resolved identity instead: the prefix before the first underscore determines the vehicle agency, even when the upstream source intended the full string as a raw ID. Only when the ID has no underscore, use the matched block's agency. The ID prefix takes precedence over the active route or block agency; source-feed agency membership does not substitute for an absent block association. An eligible tripless vehicle with a resolvable agency-prefixed ID can appear unfiltered, but never under a route filter. Exclude unresolved vehicles and vehicles without update timestamps rather than assigning source agency or ingestion/request time. Retain the original identity and required block associations in Maglev-local immutable state, including vehicles otherwise discarded by route-based ingestion filtering. Do not require a `go-gtfs` model change or alter existing JSON selection behavior.

Similarly, alert selection cannot rely only on an agency-only alert index: route-, trip-, and stop-specific selectors can establish affected agency. Resolve selector ownership before normalization, and fill a missing `agency_id` only when that ownership is unambiguous. Preserve explicit agency IDs and retain entity-specific selector fields; adding agency context must not broaden a route/stop selector into an agency-only notice. This supports the inspected iOS decoder, which requires an agency ID. Select and deduplicate whole records while preserving complete selector scope, including other agencies' selectors.

Keep alert route-filter candidates separate from ownership inference. Follow `GtfsRealtimeAlertLibrary.java:102–127` and `AlertsForAgencyAction.java:40–57` at the pinned revision: use top-level selector `route_id`, except a trip selector with no `trip_id` and a supplied `route_id` replaces that candidate with its trip route. A trip selector naming a trip does not contribute its route hint; absent top-level route, it has no route candidate. Do not infer routes from trip IDs, stop IDs, or agency-wide scope, even when static ownership lookups are used for agency eligibility/enrichment. Select a matching whole alert once, preserving every original selector; retain the existing intentional deduplication rather than reproduce legacy's repeated-selector duplicates. Test trip-only, stop-only, agency-only, trip-route-only, and conflicting explicit route candidates.

This evidence defines route-filter candidates only. Legacy's alert endpoint queries record-agency namespaces, not the agreed affected-agency selection contract; it does not resolve contradictory explicit selector agencies versus entity ownership under that contract. Do not change those selection/enrichment requirements in this revision.

Resolve alert record agency separately from affected-selector ownership. Use the first entry in a nonempty source `agency-ids` list; otherwise use the new optional per-source `fallback-alert-record-agency-id` when nonempty. Add this setting to Maglev configuration and its schema/documentation. The fallback supplies alert identity only: do not turn it into an ingestion filter, selector agency, or affected-agency selection criterion. Do not infer record agency from selectors, the request, source-feed ID, or static-feed membership. If neither setting supplies a record agency, exclude that source's identified and anonymous alerts from these exports and issue a configuration warning identifying the source. Preserve existing ingestion and JSON behavior. Test list precedence, both omitted and empty lists, cross-agency affected scope, and unresolved record agencies.

Resolve identified alert conflicts before affected-agency or route selection. The legacy logical key is the resolved record agency plus raw upstream alert ID, not an affected selector's agency and not the source-feed ID. Retain that identity and the order of successful application in Maglev-local state. The last successfully applied update replaces the entire record for its key; do not rank sources lexicographically, choose by upstream timestamp, merge content, or resurrect an older record when the replacement fails a filter. Preserve separate records when the same raw ID occurs in different agency namespaces. Anonymous retained alerts remain separate even when their text is identical.

Maintain identified-alert export state as a logical key-to-current-winner map, not as an on-demand maximum over remaining per-feed copies. Retain the source that last applied each winner. Accept lifecycle changes only from successfully parsed/applied alert feeds with a supplied header timestamp; a timestamp of zero is supplied, while an absent timestamp is not. Failed fetches/parses and missing-header-timestamp feeds preserve state. After applying records in a refresh, remove omitted keys only when the current winner's applying source is that source. A successful timestamped empty refresh performs this cleanup too. Explicit identified-key deletion removes the global winner regardless of its source. Never revive older per-source copies automatically; a later successfully applied record can recreate a removed key.

Keep lifecycle/header-presence/deletion bookkeeping Maglev-local and export-specific, alongside record agency and application order. It is not new outbound standard-field coverage or a change to `go-gtfs` DTOs. Existing JSON ingestion and selection must remain unchanged. Test winning-source omission, losing-source omission, timestamped empty feeds, failures, missing header timestamps, cross-source deletion, and subsequent reapplication. Anonymous alerts remain governed by the existing distinct-record contract; this identified-key lifecycle does not invent identity for them.

Source evidence at the pinned revision: `GtfsRealtimeSource.java:1038–1115` guards header timestamp presence, applies identified deletions, then prunes only records whose source matches the refreshing source. `ServiceAlertsServiceImpl.removeServiceAlerts` removes the global key and references rather than selecting an earlier source record. The lifecycle is source-supported, not a live-feed timing guarantee.

Source evidence at the pinned legacy revision: `GtfsRealtimeSource.createId` assigns the first configured agency, `ServiceAlertsServiceImpl.updateReferences` replaces the record by agency-and-ID, and `ServiceAlertsCacheInMemoryImpl.putServiceAlert` overwrites the complete cached value. This is processing-order replacement, not a guarantee that the greatest feed timestamp wins.

**Alternative:** reuse convenience indexes without checking their semantics. That would violate tripless-vehicle and affected-agency behavior; per-feed concatenation alone would also fail legacy cross-feed alert replacement.

### Match ordinary scheduled-trip instances using legacy rules

Match the pinned legacy `BlockFinder` behavior rather than introduce conservative or nearest-instance inference. This decision explicitly accepts its server-local date windows, first-match selection, ignored ordinary descriptor hints, and trip-ID-only caching. It applies to ordinary scheduled-trip block association, including ordinary cancellations; added/duplicated trips retain their separate instance handling. Do not silently use the agency timezone or supplied `start_date`/`start_time` to improve ordinary matching.

Resolve associations when applying source updates and publish them as Maglev-local immutable export metadata. The matching reference is the trip-update timestamp when present, otherwise the trip-feed header timestamp, with absent header timestamp interpreted as zero. For vehicle-grouped updates, the first retained update in source order supplies the matching trip and time. Keep this reference separate from the export request time and from prediction deviation/filling reference rules. Retaining feed-header reference metadata for this purpose does not broaden standard-field ingestion. Preserve existing JSON ingestion, lookup, and descriptor behavior.

On a cache miss, use server-local calendar dates at the matching reference time: before 04:00 check yesterday then today; 04:00 through 20:59:59 check today only; from 21:00 check today then tomorrow. For each date, query the active block configuration and require membership of the static trip. Derive trip start from its first static departure. Adjusted block start is the block's first departure plus that trip start minus the matching block-trip's first departure; negative results fall back to the block's first departure. Take the first candidate whose resulting adjusted start is strictly positive. No uniqueness check, absolute-stop-event scoring, or incoming frequency-start offset participates. A missing static trip or exhausted candidate search leaves the ordinary association unresolved; do not pass that record through as an ordinary export merely because it has absolute predictions.

Keep a per-source cache keyed only by qualified static trip ID. Cache successes and unresolved results for 30 minutes from insertion using an injected processing clock, without sliding expiration. Clear on static GTFS replacement; normal source refreshes do not clear it. Subsequent source applications after expiry rematch; export reads never change the retained association. Keep cache mutation in the source-application path, not in HTTP builders, and do not hold realtime writer locks during static schedule queries.

Use the resolved date/start identity for reconstruction, cancellation reconciliation, grouping, and collision detection. Differing supplied ordinary date/time hints do not by themselves make distinct resolved instances. Ordinary exported descriptor dates/start times use the resolved identity, overriding conflicting hints; preserve available schedule relationships. The existing standard-field fidelity rule is subject to this explicit identity-resolution exception. Added/duplicated descriptors retain their known instance identity and are not fed through this ordinary matcher.

Source evidence at revision `095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a`: `GtfsRealtimeTripLibrary.java:209,324,838–887` selects the update reference and resolves the block; `BlockFinder.java:46–118,140–184` implements the trip-ID cache, date windows, first candidate, and static start adjustment. `GtfsRealtimeSource.java:643–645` initializes the library's fallback time from the trip-feed header, and `:306–315` clears the matching cache on bundle replacement. The fallback is therefore not necessarily wall-clock ingestion time. These are source-supported requirements, not claims of controlled live-feed validation.

Tests must cover overnight `25:xx` service; 04:00 and 21:00 boundaries; multiple qualifying service dates; differing server/agency timezones; ignored supplied ordinary descriptors and frequency start hints; zero adjusted block start; update/header/absent timestamp references; cache reuse and expiry, cached failures, and static replacement. Confirm that changing only export request time changes header/entity IDs as required without rematching retained instance identity.

**Alternative:** prefer explicit descriptor hints and infer only unique calendar matches. Rejected in favor of the maintainer's explicit legacy-matching choice. Do not broaden this acceptance to unrelated exporter omissions, cancellation duplication, route-filter defects, or dynamic-trip formatting bugs.

### Reconstruct predictions using shared schedule and delay reasoning

Use Maglev's existing helpers as the starting point, not as proof that all legacy filling behavior is already reproduced:

- `trip_updates_helper.go`: ordered block trip updates, block delay selection, schedule matching, and stop delays.
- `trips_helper.go`: trip status and next-stop reasoning.
- `arrival_and_departure_for_stop_handler.go`: prediction lookup, prior-stop propagation, and trip-delay fallback.

Separate prediction calculation from HTTP response construction where necessary. The export needs a per-trip/per-stop prediction collection; repeatedly issuing JSON requests or wrapping JSON payloads is not an appropriate adapter.

Implement the filling rules and worked examples in the trip spec directly; legacy code is supporting evidence, not a substitute for those requirements. Assemble supplied predictions by block-trip order, matching stops by service instance and visit. Absolute event time takes precedence over schedule plus event delay. For each block service instance, consider supplied updates in scheduled block-trip order: the last supplied trip-level delay determines deviation, regardless of source-array order or requested route. Without a trip-level delay, use predicted event time minus its scheduled time for the supplied event closest to effective request time. Prefer a future event over a past event only when their distances tie. Absolute time takes precedence over event delay, and the chosen block deviation does not overwrite supplied stop predictions. These rules and worked acceptance scenarios now live in the trip spec, not only in this design.

Choose the filling-reference timestamp independently of the update that supplies the winning delay or earliest event. Traverse each source's resolved block collection in scheduled block-trip order, retaining source order for multiple updates within a trip. Before filling the current trip, every supplied timestamp seen through that trip replaces a carried timestamp, including zero. Use its last nonzero value; if zero or never supplied, use that collection's trip-feed header time (zero when absent). A later trip's timestamp does not retroactively change an earlier trip's bound. Export request time does not supply this fallback. Keep collection/source provenance in the export metadata; this timestamp rule does not settle the still-unresolved cross-source ordinary-trip winner policy.

This follows `GtfsRealtimeTripLibrary.java:923,931–949,1070–1077` at the pinned revision: `best.timestamp` is updated during ordered traversal and the filling bound uses it when nonzero, otherwise `_currentTime`, initialized from the trip-feed header. Tests must distinguish an earlier carried timestamp, a current-trip replacement, a later-trip timestamp, and an explicit zero reset to header time. This change does not alter the existing export contract's request-time-based block-deviation candidate selection.

With supplied predictions and a trip-level delay, fill missing visits at scheduled times plus deviation only where predicted departure is after the current trip's legacy filling-reference time and before the earliest existing predicted event. The single-prediction case additionally allows filling later visits within the schedule end bound represented by supplied updates. Match missing visits by trip and sequence rather than a bare stop ID, so loop visits are not accidentally collapsed. Static membership alone does not justify unconstrained downstream extrapolation.

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

Keep entity IDs as specified, but use richer internal service-instance keys for cancellation reconciliation and prediction grouping. Request-time entity IDs do not themselves distinguish multiple service instances of the same trip. Reconcile identified alerts by the legacy record-agency/raw-upstream-ID key before selection. Use that key's qualified identity as the FeedEntity ID, preserving an existing retained representation without double-prefixing. The same raw ID in different record agencies remains distinct even when both alerts affect the requested agency. Keep anonymous retained records separate, and reserve identified entity IDs before allocating count-based fallback values.

The ordinary trip ID convention is preserved; do not quietly replace it with stable IDs. When distinct service instances would produce the same entity ID, append deterministic service-instance suffixes to the colliding IDs only. Use resolved service date and start time to distinguish those instances; normal IDs remain unchanged. For ordinary trips, supplied date/time hints alone cannot establish separate instances under the accepted legacy matcher; independently resolved sources and added/duplicated state can still yield distinct instances.

Source inspection confirms this can arise in retained state: `go-gtfs` parses and groups by `TripID`, which includes start date and start time, and Maglev's merged trip list preserves those records. However, `rebuildMergedRealtimeLocked` builds `tripLookup` using only the trip ID string, with the last record winning. `GetTripUpdateByID` and even `GetTripUpdatesForTrip` therefore expose at most one record for that string. Export grouping must use the full snapshot records and resolved service-instance metadata rather than depend on these lossy lookups. Preserve raw records for mapping, but apply the accepted ordinary matcher before treating their supplied descriptor differences as distinct export instances. This is source-supported, not an observed live-feed collision.

**Alternative:** use entity IDs as the sole internal grouping key. Legacy's observed duplicate canceled/scheduled pair demonstrates why that is unsafe.

### Code reuse map

The following source-inspected candidates distinguish direct reuse from adaptations. Paths are relative to the Maglev repository. Reuse the underlying behavior where it fits; a similarly named JSON helper is not necessarily an export builder.

| Area | Existing code | Reuse and semantic boundary |
| --- | --- | --- |
| Authorization and version | `internal/restapi/routes.go`: `rateLimitAndValidateAPIKey`; `version_middleware.go`: `VersionValidationMiddleware`; `errors.go` | Reuse request protection and normal error handling. Preserve auth-before-rate-limit ordering. Ensure the no-store boundary covers globally rejected export requests too. |
| Time and timezone | `internal/utils/api.go`: `ParseTimeParameter`; `internal/restapi/timezone_helper.go`: `loadAgencyLocation` | Direct reuse with the export's additional pre-epoch guard. Use the injected clock and resolved agency timezone; do not introduce a Java-specific date parser. |
| Boolean parsing | `internal/utils/api.go`: `ParseBoolParam` | Reuse case-insensitive true/false validation, but adapt the empty-value case locally: this helper returns false for empty input, whereas the export contract defaults empty to true. Do not change existing callers' semantics. |
| ID qualification and path extraction | `internal/utils/api.go`: `FormCombinedID`, `ExtractAgencyIDAndCodeID`; `internal/utils/http.go`: `ExtractIDFromParams`; `internal/restapi/id_helpers.go` | Construct qualified identities with existing utilities. Split trip, route, and stop IDs only when known to be qualified; vehicle IDs intentionally use legacy first-underscore parsing with matched-block fallback. Path extraction currently strips `.json`, not `.pb`/`.pbtext`; add format-aware extraction before shared ID validation, or register paths with agency/format separated. The endpoint path uses a plain agency ID, not a combined entity ID. |
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
- `BuildSituationReferences` in `reference_utils.go` produces a JSON model, keeps only the first translation, and derives severity from effect. It cannot preserve all retained protobuf alert translations, and its effect-derived severity must not be used to fabricate the unavailable standard severity field.
- The current alert index skips empty alert IDs and is not a full-record export source; fallback-ID handling needs access to retained records rather than only indexed results.

No reusable outbound FeedMessage builder or OBA headsign extension encoder was found in the inspected application code. Existing `go-gtfs/proto` messages and the protobuf runtime remain reusable foundations; endpoint mapping, bounded prediction assembly, instance cancellation reconciliation, and extension-aware serialization are the new integration work.

### Suggested delivery boundaries

Keep the shared contract in one coherent specification, but recommend independently reviewable implementation slices:

1. Shared request/format infrastructure and vehicle positions.
2. Alert export, ownership resolution, and field fidelity.
3. Trip export, prediction reconstruction, reconciliation, and headsign extensions.

The boundaries are recommendations, not a mandatory PR count. Further split tightly scoped refactors or protobuf binding work when useful. Keep related changes understandable without unrelated cleanup. Maglev's contribution guidelines prefer small PRs, so avoid making the simpler exports wait on a large combined trip implementation.

## Risks / Trade-offs

- [DTO field loss] → Export applicable values already retained and explicitly defer other standard fields, including severity, to a later retention feature. Do not proxy raw feeds or fabricate lost values.
- [Legacy vehicle parsing misinterprets raw underscores] → This is an accepted compatibility choice: `bus_A` resolves to agency `bus`, ID `A`. Test prefix precedence, matched-block fallback for underscore-free IDs, and exclusion when neither resolves ownership. Do not introduce source-feed fallback or a qualification setting; keep these semantics confined to exports.
- [Legacy scheduled-trip matching can choose an unexpected service day] → This is explicitly accepted: use server-local fixed date windows, first qualifying block, ignored ordinary descriptor hints, and per-source trip-ID-only caching. Test these quirks, including unresolved matches and cached failures; do not replace them with conservative inference or agency-local matching. Keep ordinary descriptor resolution export-local and preserve JSON behavior.
- [Prediction fill differs from existing REST helpers] → Controlled sparse/block fixtures must compare expected predictions against pinned legacy behavior, with intentional departures documented. Broad helper reuse without semantic comparison is insufficient.
- [Request time changes header and trip entity IDs] → No response caching; fixed-time fixtures make comparisons reproducible. Request time does not recreate earlier retained state.
- [Multi-feed conflicts or repeated trip instances] → For alerts, apply whole-record last-successfully-applied replacement by record agency and raw alert ID before filters; retain application order and winning source, remove omitted winners only for that source, and never resurrect earlier copies. Preserve state on failures or missing header timestamps and avoid cross-agency collapse. For trips, reconcile by service-instance identity and use collision-only entity-ID suffixes without losing distinct instances.
- [Optional scalar defaults masquerade as supplied data] → Preserve existing optional presence where available; do not invent values or expand ingestion solely to recover presence currently lost.
- [Agency-wide reconstruction cost] → Batch schedule lookups and build each block's prediction collection once per response; do not hold realtime writer locks during that work.
- [Legacy source behavior exceeds tested coverage] → Separate source-supported scenarios from controlled live evidence. Added trips, multi-zone service, and isolated downstream fixtures need targeted tests, not assumptions from the public feed.

## Migration Plan

This adds routes without replacing existing JSON APIs. No destructive database migration is proposed. Add narrow agency-provenance changes if needed for ownership selection; do not broaden standard-field retention in this feature. Verify that JSON outputs and existing feed ingestion behavior remain unchanged apart from required provenance support. Each delivered endpoint must pass its shared-contract and endpoint-specific scenarios before deployment.

Use controlled matching GTFS/GTFS-RT fixtures for freshness, sparse updates, interlining, cancellations, translations, and extensions. Parse binary and text outputs semantically; do not assert unstable ordering or Java-specific whitespace. Public CDN cache hits are not evidence of origin filtering or authorization. Production/legacy response comparisons should distinguish intentional departures from regressions.

Operators with unscoped alert sources can set `fallback-alert-record-agency-id` to enable alert export without opting into ingestion filtering. Sources with a nonempty `agency-ids` list need no new setting; its first entry takes precedence. Sources with neither identity setting continue existing ingestion and JSON behavior but produce a configuration warning and no exported alerts. The fallback is optional and absent/empty means unavailable.

Rollback is reverting the new route registration and implementation; preserve existing realtime configuration and data. No new persistence or cache service is required.

## Open Questions

None. Vehicle identity/block associations, ordinary scheduled-trip matching references/resolved instances, and alert identity/application-order metadata will live in Maglev-local retained state. Ordinary trip instance matching explicitly follows legacy's source-time reference, server-local date search, ignored descriptor hints, first-match selection, and trip-ID-only cache. Vehicle IDs follow legacy first-underscore parsing and matched-block fallback without a new feed setting. Alert record agency uses the first `agency-ids` entry, then `fallback-alert-record-agency-id` when the list is absent or empty; unresolved sources are excluded from alert exports with a configuration warning. The fallback does not enable ingestion filtering. Alert conflicts use whole-record last-applied replacement before filtering, with source-owned omission cleanup, global explicit deletion, and no automatic resurrection; failures or missing header timestamps preserve state. Additional standard-field retention remains deferred; unknown-agency date strings use UTC.

# Proposal

## Why

Maglev does not serve the legacy agency-wide GTFS-Realtime exports; its JSON APIs are not protocol-compatible substitutes. Adding these exports closes the confirmed iOS vehicle-map and REST agency-alert gaps and provides the corresponding trip-update API, while preserving useful legacy behavior and avoiding verified defects except for the explicitly accepted legacy vehicle-ID interpretation.

## What Changes

- Add agency-wide exports under `/api/gtfs_realtime/` for `vehicle-positions-for-agency/{id}`, `alerts-for-agency/{id}`, and `trip-updates-for-agency/{id}`, in binary `.pb` and readable `.pbtext` formats. Generate GTFS-RT 2.0 from Maglev state rather than proxying upstream feeds; exact Java text formatting is not required.
- Share API-key authorization, rate limiting, Maglev's v2 and time-parameter conventions, raw route-ID filtering, and ID normalization. Default to raw trip, route, and stop payload IDs by removing only known agency qualification, preserving underscores in those raw upstream IDs. Vehicle IDs deliberately follow legacy interpretation: split at the first underscore, or use the matched block's agency when no underscore exists. `removeAgencyIds=false` emits the resulting qualified identities without double-prefixing. Reject pre-epoch times because feed timestamps are unsigned. Date strings use the requested agency's timezone, or UTC for unknown agencies. No new vehicle-ID qualification setting is required.
- Return valid empty feeds for unknown agencies and valid requests without matching data. Return 400 for invalid parameters rather than legacy 500s. Set appropriate content types and `Cache-Control: no-store`; conditional caching is not required.
- Export positioned vehicles with update timestamps younger than 600 seconds relative to the requested time, selected by legacy-resolved vehicle agency: the prefix before the first underscore, or the matched block's agency for an ID without underscores. Feed agency membership does not substitute for a matched block. Exclude missing timestamps or unresolved ownership. Permit tripless vehicles with resolvable prefixed IDs in unfiltered output; exclude them from route-filtered output.
- Select trip updates by their own route's agency and route filter, including downstream trips and cancellations. Export known cancellations once as CANCELED, without a contradictory SCHEDULED update for the same trip instance.
- Include schedule-based prediction reconstruction for sparse and downstream/interlining trip updates, reusing Maglev's existing block, schedule, and delay logic rather than limiting output to stored upstream stop updates. Define filling bounds, block-deviation selection, and worked acceptance examples directly in the specs. The last supplied trip-level delay in scheduled block-trip order wins; without one, use the supplied event closest to effective request time, preferring a future event only on a tie.
- Export alerts affecting the requested agency, retaining complete selectors, active windows, and translations. Route filtering selects whole alerts once, without duplicates or selector trimming. Include retained current, future, and expired alerts; request time does not filter alert activity. Populate missing selector agency IDs when ownership is unambiguous, preserve existing IDs, and do not guess. Identify alerts by record agency (the source's first configured agency) plus raw upstream alert ID. Conflicts use whole-record last-successfully-applied replacement before filtering, not source ordering, upstream timestamps, or content merging. Keep agency namespaces and anonymous retained records distinct.
- Export applicable standard GTFS-RT field values already retained by the current realtime models rather than deliberately reproduce legacy omissions. Defer additional standard-field retention and ingestion-model expansion, including alert severity, to a follow-up feature. Maglev-local vehicle identity/block associations, alert identity/application-order metadata, and OBA trip/stop headsign extensions remain in scope.
- Preserve legacy entity-ID conventions: sequential vehicle entity IDs, qualified trip ID plus request-time milliseconds for trip-update entity IDs, and qualified record-agency/raw-upstream alert identities with a count-based absent-ID fallback. Preserve existing retained representations of those qualified alert IDs without double-prefixing. Avoid duplicate entity IDs through alert deduplication and cancellation reconciliation. Only when distinct trip service instances would collide, append a service-instance suffix; otherwise retain the legacy trip ID convention rather than substitute stable IDs.

## Capabilities

### New Capabilities

- `gtfs-realtime-export-contract`: Shared export formats, parameters, authorization, error handling, ID normalization, feed headers, and HTTP behavior.
- `gtfs-realtime-vehicle-export`: Agency vehicle selection, position and freshness eligibility, route filtering, payload fidelity, and vehicle entity IDs.
- `gtfs-realtime-alert-export`: Affected-agency selection, whole-alert filtering and deduplication, active windows, translations, standard fields, and alert entity IDs.
- `gtfs-realtime-trip-export`: Trip-agency selection, own-route filtering, sparse and downstream prediction reconstruction, cancellation reconciliation, headsign extensions, and trip entity IDs.

### Modified Capabilities

None; the project has no existing capability specs.

## Impact

- New HTTP routes and serializers in `internal/restapi/`, consuming the immutable realtime snapshot and static GTFS schedule data.
- Narrow Maglev-local retained metadata for original vehicle identities, matched-block associations, and alert record-agency identities/application order. Preserve required export records without changing existing JSON selection behavior or expanding standard-field ingestion.
- Reuse or extraction of existing prediction helpers, notably `trip_updates_helper.go`, `trips_helper.go`, and the arrival/departure prediction paths. Existing JSON API behavior should not change incidentally.
- Maglev-local schema and generated Go bindings for the two OBA headsign extensions, importing existing standard GTFS-RT types without adding OBA-specific bindings to `go-gtfs`.
- Tests for binary/text semantic equivalence, authorization and parameters, freshness boundaries, multi-agency and route selection, legacy vehicle-ID parsing/block fallback, alert conflict replacement before filtering, sparse predictions and deviation selection, interlining, cancellations, and extension decoding.
- Confirmed direct client benefit for inspected iOS vehicle and alert paths. No direct dependency on these exports was established for inspected Android, Wayfinder, or JS SDK revisions; no inspected client dependency was established for trip exports.
- Legacy 2.7.1 source and controlled/public captures inform compatibility, but observed bugs and CDN behavior are not requirements unless explicitly accepted here, as with legacy vehicle-ID interpretation. Specs describe the required behavior and acceptance examples directly; reference-source knowledge is not needed to understand the contract.

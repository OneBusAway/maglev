# Proposal

## Why

Maglev does not serve the legacy agency-wide GTFS-Realtime exports; its JSON APIs are not protocol-compatible substitutes. Adding these exports closes the confirmed iOS vehicle-map and REST agency-alert gaps and provides the corresponding trip-update API, while preserving useful legacy behavior without reproducing verified defects.

## What Changes

- Add agency-wide exports under `/api/gtfs_realtime/` for `vehicle-positions-for-agency/{id}`, `alerts-for-agency/{id}`, and `trip-updates-for-agency/{id}`, in binary `.pb` and readable `.pbtext` formats. Generate GTFS-RT 2.0 from Maglev state rather than proxying upstream feeds; exact Java text formatting is not required.
- Share API-key authorization, rate limiting, Maglev's v2 and time-parameter conventions, raw route-ID filtering, and ID normalization. Default to raw payload IDs by removing only known agency qualification, preserving underscores in raw upstream IDs; `removeAgencyIds=false` emits qualified IDs consistently without double-prefixing. Reject pre-epoch times because feed timestamps are unsigned.
- Return valid empty feeds for unknown agencies and valid requests without matching data. Return 400 for invalid parameters rather than legacy 500s. Set appropriate content types and `Cache-Control: no-store`; conditional caching is not required.
- Export positioned vehicles with update timestamps younger than 600 seconds relative to the requested time, selected by unambiguous vehicle agency ownership. Exclude missing timestamps or unresolved ownership rather than inventing values. Permit tripless vehicles in unfiltered output; exclude them from route-filtered output.
- Select trip updates by their own route's agency and route filter, including downstream trips and cancellations. Export known cancellations once as CANCELED, without a contradictory SCHEDULED update for the same trip instance.
- Include schedule-based prediction reconstruction for sparse and downstream/interlining trip updates, reusing Maglev's existing block, schedule, and delay logic rather than limiting output to stored upstream stop updates. Define filling bounds and worked acceptance examples directly in the specs.
- Export alerts affecting the requested agency, retaining complete selectors, active windows, and translations. Route filtering selects whole alerts once, without duplicates or selector trimming. Include retained current, future, and expired alerts; request time does not filter alert activity. Populate missing selector agency IDs when ownership is unambiguous, preserve existing IDs, and do not guess.
- Retain through ingestion and export the supported supplied standard GTFS-RT fields enumerated in the endpoint specs, rather than reproducing legacy omissions or treating current DTO gaps as an excuse to lose values. Include OBA trip and stop headsign extensions when available.
- Preserve legacy entity-ID conventions: sequential vehicle entity IDs, qualified trip ID plus request-time milliseconds for trip-update entity IDs, and existing alert IDs with an absent-ID fallback. Avoid duplicate entity IDs through alert deduplication and cancellation reconciliation. Only when distinct trip service instances would collide, append a service-instance suffix; otherwise retain the legacy trip ID convention rather than substitute stable IDs.

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
- Reuse or extraction of existing prediction helpers, notably `trip_updates_helper.go`, `trips_helper.go`, and the arrival/departure prediction paths. Existing JSON API behavior should not change incidentally.
- Maglev-local schema and generated Go bindings for the two OBA headsign extensions, importing existing standard GTFS-RT types without adding OBA-specific bindings to `go-gtfs`.
- Tests for binary/text semantic equivalence, authorization and parameters, freshness boundaries, multi-agency and route selection, sparse predictions, interlining, cancellations, and extension decoding.
- Confirmed direct client benefit for inspected iOS vehicle and alert paths. No direct dependency on these exports was established for inspected Android, Wayfinder, or JS SDK revisions; no inspected client dependency was established for trip exports.
- Legacy 2.7.1 source and controlled/public captures inform compatibility, but observed bugs and CDN behavior are not requirements. Specs describe the required behavior and acceptance examples directly; reference-source knowledge is not needed to understand the contract.

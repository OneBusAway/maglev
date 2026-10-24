# Stop consolidation: implementation plan

Goal: Maglev reads Java OneBusAway stop consolidation files unchanged,
and serves the same API responses production Puget Sound does.

## Behavior

Settled with the maintainer on 2026-10-23, and checked against
production Puget Sound (`api.pugetsound.onebusaway.org`) where the API
shows it.

| Case | Java | Maglev |
|---|---|---|
| File parsing | `EntityReplacementStrategyFactory` | Same, quirk for quirk; tested against Java's own output |
| Token without `_` | Whole file fails | Import fails; the previous database keeps serving |
| Stop absorbed twice | Later line wins, silently | Later line wins, with a warning |
| Absorbed stop not loaded | No effect | No effect, with a warning |
| Canonical stop not stored (missing, or itself absorbed) | Absorbed stop removed; its stop times point at nothing | Absorbed stop removed, its stop times dropped, with a warning |
| Absorbed station's children | `parent_station` not rewritten | Children point at the canonical stop, or at no parent |
| `stop/{absorbed}` | Returns the canonical stop | Same (not built yet) |
| Other endpoints with `{absorbed}` | Not found (`arrivals-and-departures-for-stop` is 404; `schedule-for-stop` is 200 with body `null`) | Treated like any missing stop |
| Realtime trip updates | Translated only for sources with `ConsolidatedStopsModificationStrategy` | Translated whenever a file is configured |
| Realtime alerts | Always translated | Always translated |
| `stop-ids-for-agency` | Lists a stop under its own prefix agency only | Same (not built yet; maglev today lists a stop under every agency serving it) |

Production evidence, from the probes:

- `stop/29_40` returns `1_16140`, whose routes include `29_130`.
- `1_16140` appears in `stop-ids-for-agency/1` but not in `/29`.
- `29_759` is gone, even though Community Transit's feed has it and its
  canonical `1_5419` no longer exists.
- Community Transit trips at `1_16140` show `predicted: true`.

The stop-times row is the one place maglev can't copy Java literally:
it can't store a stop time with no stop.

## Which agency a token uses

Java names every stop in a feed after the feed's default agency: the
configured `defaultAgencyId`, or else the first row of `agency.txt`. It
is not the agency of the routes serving the stop. Until multi-feed
loading lands, maglev uses the first row of `agency.txt`.

Consequence: with one feed loaded, a line naming another feed's stops
does nothing, unless that feed's stop is absorbed into a stop from an
unloaded feed. Then the absorbed stop is dropped (`29_556 1_2208` drops
`1_2208` from a Metro-only server). Java does the same, and the warnings
name every such line. Don't pair a region-wide file with only some of
its feeds.

## Done

Each item is one PR.

1. **Parser** (`internal/consolidation/parse.go`), against main. Golden
   tests: the Puget Sound file plus an edge-case corpus, with expected
   output from Java itself (`testdata/stop_consolidation`).
2. **Validator** (`validate.go`), stacked on 1. Warnings only, each
   with a stable kind for metric labels.
3. **Import, draft**, stacked on the streaming import (#1677):
   - `Apply`, which works out Java's outcome for each absorbed stop
   - the gtfsdb side: skip absorbed stops; move or drop their stop
     times; fix children and trip ends; record each stop in
     `stop_consolidations`
   - loading, hashing and warning in `internal/gtfs`
   - the `gtfs-static-feed.stop-consolidation-url` option

## Still to build

In order. Each is about 200 lines unless noted.

1. **Stop prefix agency.** Every response prefixes a stop with its
   feed's default agency, and `stop-ids-for-agency` lists it only there.
   This is the stops part of multi-feed-identity.md's PR 5. It doesn't
   depend on the streaming stack and can start now.
2. **`stop/{absorbed}` alias.** When `GetStop` finds nothing, look the
   ID up in `stop_consolidations`. If its stop times moved, serve the
   canonical stop under the canonical ID. Dropped rows stay not found,
   which matches production's `null` for `29_759`.
3. **Realtime translation.**
   - After each import, load moved `stop_consolidations` rows into a
     map and swap it in atomically, like the merged realtime snapshot.
   - In `updateFeedRealtime`, after parsing and before the agency
     filter, rewrite absorbed stop IDs in trip updates, vehicle
     positions and alert informed entities.
   - The map is read from the table, so it survives restarts that skip
     the import.
4. **Warning metric.** `stop_consolidation_warnings{kind}`, a gauge set
   on each import.
5. **Multi-feed**, multi-feed-identity.md PRs 1, 2, 4, 6 and 7. That
   design stores Java-style public IDs, so a `StopID` *is* the stored
   ID, and the per-feed default agency replaces the first-agency rule.
   The consolidation code doesn't otherwise change.
6. **obacloud input.** Read obacloud's existing `bundle-inputs.json` v1
   manifest (`feeds[].{id,defaultAgencyId,url,sha256}`,
   `stopConsolidationUrl`), the same file Java's Docker builder reads,
   in place of a new `id_map.txt` contract.
7. **Puget Sound parity fixture.**
   - Trimmed Metro and Community Transit feeds around `1_16140`/`29_40`
     and `1_5419`/`29_759`.
   - A captured Community Transit realtime snapshot.
   - Golden responses for the calls listed above.
   - Needs item 5.

Record in maglev.wiki Implementation Decisions:

- stop times dropped when the canonical stop isn't stored
- children re-pointed to the canonical stop
- realtime translation always on
- conflicts logged as warnings

## Open items

- **Agencies without `agency_id`.** go-gtfs names them `<name>_id`. I
  haven't checked what Java names them, which matters for the token's
  agency.
- **Character set.** Java reads the file in the JVM's default charset
  and keeps a UTF-8 byte order mark as part of the first token. Maglev
  reads UTF-8 and also keeps the mark, matching Java on a UTF-8 JVM.
- **Gzip.** Java gunzips local `.gz` files; maglev doesn't.
- **Trips through a dropped stop.** Unverified whether production keeps
  those trips without the stop or drops the whole trip. Its Community
  Transit schedule didn't match the feed I downloaded.
- **Production's trip-update setting.** Unknown whether production
  configures `ConsolidatedStopsModificationStrategy`. The predictions
  at `1_16140` could also come from delay carried over from the trip's
  other stops.

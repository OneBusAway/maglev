# GTFS-Realtime alert export

## Purpose

Export retained service alerts for the agencies they affect, preserving enough scope and timing information for clients to interpret each notice correctly.

## ADDED Requirements

### Requirement: Affected-agency selection
An alert SHALL qualify for an agency export when an affected selector belongs to the requested agency, including agency ownership resolved from affected routes, trips, or stops. Selection SHALL NOT depend merely on the originating feed or alert record's owning agency. Available explicit selector agency IDs SHALL be preserved.

#### Scenario: Cross-agency alert ownership
- **WHEN** a record owned by agency A contains selectors affecting agency B
- **THEN** it qualifies for B's export regardless of its owning record or source feed

#### Scenario: Selector without explicit agency
- **WHEN** an alert selector names a route belonging to agency B without an explicit agency ID
- **THEN** agency selection resolves that route's ownership and includes the alert for B

### Requirement: Selector agency enrichment
An informed-entity selector SHALL identify one affected scope; fields within it apply together. A missing `agency_id` SHALL be populated when route, trip, or stop ownership unambiguously resolves it. Existing agency IDs SHALL be preserved. Ambiguous or unresolved ownership SHALL NOT be guessed, and enrichment SHALL NOT broaden an entity-specific selector into an agency-only selector.

#### Scenario: Route selector recognized by iOS
- **WHEN** a selector contains only route `2LINE`, whose ownership resolves unambiguously to agency 40
- **THEN** it is exported with both `agency_id="40"` and `route_id="2LINE"`, allowing clients requiring agency IDs to recognize it

#### Scenario: Existing selector agency
- **WHEN** a selector already supplies an agency ID
- **THEN** that value is retained rather than overwritten with the requested agency

#### Scenario: Ambiguous ownership
- **WHEN** a selector has no agency ID and its affected entity's ownership cannot be resolved unambiguously
- **THEN** no agency ID is invented for that selector

### Requirement: Whole-alert route selection and deduplication
A route-filtered export SHALL include an agency-eligible alert if at least one affected route matches the raw filter. Each selected logical alert SHALL appear once, retaining all its informed-entity selectors, including nonmatching routes or other agencies. Multiple matching selectors SHALL NOT produce duplicate entities.

#### Scenario: Multiple route matches
- **WHEN** one alert affects route A at two stops and route B at another stop, and the filter is A
- **THEN** the alert appears once with all three selectors retained

### Requirement: Legacy alert identity and replacement
An identified alert's logical identity SHALL be its record agency plus raw upstream alert ID. The record agency SHALL be the source's first configured agency, independently of affected-selector agencies. For conflicting records with the same identity, the last successfully applied update SHALL replace the whole record before agency or route selection. Source-feed ordering and upstream timestamps SHALL NOT choose the winner; contents SHALL NOT be merged.

#### Scenario: Conflicting updates from two sources
- **WHEN** two sources use record agency 40 and raw alert ID `notice`, and the second successfully applied update changes the text, windows, and selectors
- **THEN** the export uses only the second record's complete contents, even if its source sorts earlier or its upstream timestamp is older

#### Scenario: Replacement changes affected scope
- **WHEN** an earlier record affects route A and the last applied replacement affects only route B
- **THEN** route A filtering does not resurrect the earlier record; route B filtering returns the replacement

#### Scenario: Same raw ID in different agency namespaces
- **WHEN** records under agencies 40 and 20 share raw upstream alert ID `notice` and both affect the requested agency
- **THEN** both remain distinct logical alerts with entity IDs `40_notice` and `20_notice`

#### Scenario: Alerts without upstream IDs
- **WHEN** two distinct retained alerts have no upstream IDs, even if their text is identical
- **THEN** they remain separate records with unique fallback IDs; multiple matching selectors do not duplicate either record

### Requirement: Retained alerts independent of activity time
The export SHALL include eligible retained alerts regardless of whether their active windows are current, future, or expired. It SHALL preserve active periods in epoch seconds and all available header, description, and URL translations. The request `time` SHALL affect the header, not activity-window selection.

#### Scenario: Current future and expired windows
- **WHEN** three retained eligible alerts have current, future, and expired active windows
- **THEN** all three are exported with their respective windows

#### Scenario: Multilingual notice
- **WHEN** an eligible alert contains English and Spanish header and description translations
- **THEN** both translations survive export without requiring their array order to remain fixed

### Requirement: Alert entity identities
An identified alert's FeedEntity ID SHALL be its qualified legacy identity `{recordAgencyID}_{rawUpstreamAlertID}`, independently of payload normalization. An existing retained ID representing that identity SHALL be preserved, not qualified twice. Alerts without IDs SHALL receive count-based fallback IDs. Entity IDs SHALL be unique within the resulting feed, including when fallback IDs coexist with identified alerts.

#### Scenario: Existing qualified alert ID
- **WHEN** an alert ID is `40_16623` and payload normalization is enabled
- **THEN** its entity ID remains `40_16623`

#### Scenario: Missing alert IDs
- **WHEN** selected alerts include missing IDs and supplied IDs
- **THEN** every entity receives an ID and fallback allocation avoids collisions with supplied IDs

### Requirement: Alert field fidelity
The export SHALL preserve cause, effect, header/description/URL translations, active periods, and informed-entity selector values retained by the current alert model. Selector route, trip, and stop IDs SHALL obey normalization, while explicit or resolved agency IDs remain intact. Alert severity and other standard fields not retained by current ingestion are deferred; they SHALL NOT be fabricated or require model expansion for this feature.

#### Scenario: Rich alert export
- **WHEN** a selected retained alert has cause CONSTRUCTION, effect DETOUR, translations, active periods, and selectors
- **THEN** those values are preserved in export, while unavailable severity remains absent

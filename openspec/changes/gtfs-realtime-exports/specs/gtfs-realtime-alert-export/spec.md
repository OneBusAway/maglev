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

### Requirement: Retained alerts independent of activity time
The export SHALL include eligible retained alerts regardless of whether their active windows are current, future, or expired. It SHALL preserve active periods in epoch seconds and all available header, description, and URL translations. The request `time` SHALL affect the header, not activity-window selection.

#### Scenario: Current future and expired windows
- **WHEN** three retained eligible alerts have current, future, and expired active windows
- **THEN** all three are exported with their respective windows

#### Scenario: Multilingual notice
- **WHEN** an eligible alert contains English and Spanish header and description translations
- **THEN** both translations survive export without requiring their array order to remain fixed

### Requirement: Alert entity identities
An alert's existing ID SHALL be retained as its FeedEntity ID independently of payload normalization. Alerts without IDs SHALL receive count-based fallback IDs. Entity IDs SHALL be unique within the resulting feed, including when fallback IDs coexist with supplied IDs.

#### Scenario: Existing qualified alert ID
- **WHEN** an alert ID is `40_16623` and payload normalization is enabled
- **THEN** its entity ID remains `40_16623`

#### Scenario: Missing alert IDs
- **WHEN** selected alerts include missing IDs and supplied IDs
- **THEN** every entity receives an ID and fallback allocation avoids collisions with supplied IDs

### Requirement: Alert field fidelity
The export SHALL retain and export supplied cause, effect, severity, header/description/URL translations, active periods, and informed-entity selector fields. Selector route, trip, and stop IDs SHALL obey the shared normalization switch, while explicit or resolved agency IDs remain intact. Current ingestion or DTO omissions SHALL NOT justify losing these supplied values.

#### Scenario: Rich alert export
- **WHEN** an upstream alert supplies cause CONSTRUCTION, effect DETOUR, and severity SEVERE
- **THEN** all three survive ingestion and export rather than disappear through DTO omissions

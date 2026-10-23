# Use Case: Report Problem with Trip

## Goal in Context

A rider's client application submits a user-generated problem report regarding a specific transit trip (for example, if the vehicle never arrived, arrived early or late, displayed the wrong destination headsign, or bypassed a stop). The server records the report for review by transit operations.

## Scope

OneBusAway REST API — `GET /api/where/report-problem-with-trip/{id}.json`

*(Note: The legacy Java Struts implementation maps `show`, `create`, and `index` to the same handler, accepting both GET and POST requests. Maglev currently routes GET requests to `/api/where/report-problem-with-trip/{id}`.)*

## Level

User goal

## Primary Actor

Rider (via a client application)

## Stakeholders and Interests

- **Rider** — wants to report a service anomaly with a scheduled trip and receive immediate confirmation.
- **Transit Agency / Operations** — wants timely, detailed rider incident reports (including vehicle ID, service date, stop context, and user location) to detect service disruptions and schedule adherence issues.
- **Client Application** — expects a consistent response envelope and predictable parameter binding across client libraries.

## Preconditions

- The caller supplies a valid API key (`key=<key>`).
- The trip ID parameter `{id}` is provided in the request path in the combined `agencyId_tripId` format (e.g., `1_12345`).

## Minimal Guarantees

- The response envelope always includes `code`, `currentTime`, `text`, and `version`.
- Non-context database storage failures return HTTP 500 with a standard JSON error envelope (`"internal server error"`, `version: 2`); context deadline expirations return HTTP 504, and client-canceled requests return client-canceled responses.
- Malformed coordinates, accuracy, or boolean values return HTTP 400 with a field validation error envelope.

## Success Guarantees

- The problem report is stored in the database (`problem_reports_trip`) with timestamps (`created_at` and `submitted_at`) set to the current server time in milliseconds.
- Omitted optional parameters are stored as database `NULL`.
- HTTP status 200 is returned with a standard JSON response envelope containing `code: 200`, `text: "OK"`, `version: 2`, and an empty `data` object (`{}`).

## Trigger

A client issues an HTTP request to `GET /api/where/report-problem-with-trip/{id}.json?key=<key>&...`.

## Main Success Scenario

1. The client supplies the trip ID as path segment `{id}` along with an API key and optional query parameters (`serviceDate`, `vehicleId`, `stopId`, `code`, `userComment`, `userOnVehicle`, `userVehicleNumber`, `userLat`, `userLon`, `userLocationAccuracy`).
2. The server extracts the agency ID and trip ID by splitting `{id}` on the first underscore character ([`report_problem_with_trip_handler.go#L19`](../internal/restapi/report_problem_with_trip_handler.go#L19), [`id_helpers.go#L18–L40`](../internal/restapi/id_helpers.go#L18-L40)).
3. The server parses and validates floating-point parameters (`userLat`, `userLon`, `userLocationAccuracy`) and boolean parameter `userOnVehicle`.
4. If `userComment` is present, the server truncates the text to a maximum of 500 characters.
5. The server records the report into the database with `trip_id`, `service_date`, `vehicle_id`, `stop_id`, `code`, `user_comment`, `user_on_vehicle`, `user_vehicle_number`, coordinates, accuracy, and server timestamps ([`ReportProblemWithTripAction.java#L138–L140`](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/where/ReportProblemWithTripAction.java#L138-L140), [`report_problem_with_trip_handler.go#L65–L89`](../internal/restapi/report_problem_with_trip_handler.go#L65-L89)).
6. The server returns HTTP 200 with an empty OK response envelope (`data: {}`) ([`ReportProblemWithTripAction.java#L142`](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/where/ReportProblemWithTripAction.java#L142), [`report_problem_with_trip_handler.go#L91`](../internal/restapi/report_problem_with_trip_handler.go#L91)).

## Extensions

**1a. `id` is missing or empty:**
- The server returns HTTP 400 with `code: 400` and `text: "id cannot be empty"`.

**1b. `id` contains no underscore (malformed combined ID):**
- The server returns HTTP 400 with `code: 400` and `text: "invalid combined id format, expected agencyId_code"`.
- The response also contains `data.fieldErrors.id` with the same message.

**2a. Malformed floating-point query parameters (`userLat`, `userLon`, `userLocationAccuracy`):**
- The server rejects non-numeric strings, NaN, or infinite values with HTTP 400 and a JSON validation error response containing `data.fieldErrors`.

**2b. Malformed boolean query parameter (`userOnVehicle`):**
- Non-empty values other than case-insensitive `true` or `false` (e.g. `1`, `0`, `yes`, `bad`) are rejected with HTTP 400 and `fieldErrors["userOnVehicle"]`. An empty value (`?userOnVehicle=`) evaluates to `false` without an error.

**3a. Database failure or database uninitialized:**
- For non-context database errors, the server logs the error and returns HTTP 500 with a standard JSON error envelope (`"internal server error"`, `version: 2`). If a context timeout occurs (`context.DeadlineExceeded`), the server returns HTTP 504; client cancellations use `clientCanceledResponse`.

**4a. Missing or invalid API key:**
- The server returns HTTP 401 with `code: 401` and `text: "permission denied"`.

## Suspected Defects

### Defects that affect the use case

**1. Error response version hardcoded to 1 (defect DC-10).**

In legacy Java Struts, `ExceptionInterceptor.getExceptionAsResponseBean` ([line 72](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/ExceptionInterceptor.java#L72)) constructs error response envelopes with hardcoded `version: 1` regardless of the version requested by the client (version 2), and serializes the raw unhandled exception message in the `text` field. Successful responses use `version: 2`.

*Maglev intentionally corrects this — see [Implementation Decisions](#implementation-decisions).*

**2. Parameter casing mismatch between Java Struts and OpenAPI (#1715).**

The OpenAPI specification (`testdata/openapi.yml`) specifies `vehicleID` and `stopID` (with uppercase `ID`) as query parameters for `/api/where/report-problem-with-trip/{tripID}.json`. Official generated SDKs (such as the TypeScript / JavaScript SDK) send `vehicleID` and `stopID`. However, in Java OneBusAway (`ReportProblemWithTripAction.java#L64-L68`), Struts binds parameters to bean setters (`setVehicleId` and `setStopId`). Because Struts property binding is case-sensitive, query parameters sent with uppercase `ID` are silently ignored and stored as `null`.

## Implementation Decisions

**Standardized 500 error envelope with version 2 (PR #1641, defect DC-10).**

When a non-context database persistence error occurs or the database is uninitialized, Maglev returns a standard OneBusAway JSON error envelope with HTTP status 500, `Content-Type: application/json`, `"version": 2`, `"code": 500`, and `"text": "internal server error"`. (Context deadline expirations return HTTP 504, and canceled requests return client-canceled responses.) This addresses defect DC-10 and avoids leaking internal database exception strings or returning plain text errors.

**Parameter casing for vehicleId and stopId (#1715).**

Maglev currently reads `vehicleId` and `stopId` (with lowercase `d`) to maintain exact parity with Java Struts parameter binding. Requests sending uppercase `vehicleID` or `stopID` (such as calls from OpenAPI-generated SDKs) are currently ignored and stored as `NULL`. Supporting both parameter casings is tracked in [#1715](https://github.com/OneBusAway/maglev/issues/1715).

**Omitted optional parameters stored as database NULL (PR #1641).**

When optional string query parameters (`serviceDate`, `vehicleId`, `stopId`, `code`, `userComment`, `userVehicleNumber`) are omitted or empty, Maglev persists them as SQL `NULL` values using `nulls.NonEmptyString`, matching the legacy Java Struts behavior where unpopulated bean properties remain `null`. Omitted boolean (`userOnVehicle`) and float parameters (`userLat`, `userLon`, `userLocationAccuracy`) are likewise stored as SQL `NULL`.

**Strict float and boolean parameter validation (PR #1643, [[Boolean-Query-Parameters]]).**

Maglev strictly validates `userLat`, `userLon`, and `userLocationAccuracy` using `utils.ParseFloatParam`. Malformed strings (including `garbage`, `NaN`, `Inf`, and `1e999`) are rejected with HTTP 400 and an explicit `fieldErrors` structure. Explicit `0` coordinate values are preserved. For `userOnVehicle`, malformed non-empty values (such as `1`, `0`, or `bad`) return HTTP 400, while omitted or empty values default to `false` per [[Boolean-Query-Parameters]].

**User comment length limit.**

User comments are truncated to 500 characters (`utils.MaxCommentLength`) before storage to guard against unbounded payloads while preserving rider feedback.

**Deprecated API status.**

Problem reporting endpoints are in scope for Maglev 1.0 to preserve backward compatibility with legacy mobile clients, but the problem-reporting feature is deprecated per [[Maglev-1.0-Endpoint-Scope]] and is a candidate for redesign in a future API version.

## Request Parameters

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "Combined agencyId_tripId identifier in the URL path (e.g. 1_12345)"
    },
    "key": {
      "type": "string",
      "description": "API authentication key"
    },
    "serviceDate": {
      "type": "integer",
      "format": "int64",
      "description": "The service date of the trip in Unix milliseconds per canonical OpenAPI contract (accepted and stored by Maglev as an opaque string without numerical or date validation)"
    },
    "vehicleId": {
      "type": "string",
      "description": "The vehicle actively serving the trip (note lowercase 'd' casing, see #1715)"
    },
    "stopId": {
      "type": "string",
      "description": "Stop ID where the user is experiencing the problem (note lowercase 'd' casing, see #1715)"
    },
    "code": {
      "type": "string",
      "enum": [
        "vehicle_never_came",
        "vehicle_came_early",
        "vehicle_came_late",
        "wrong_headsign",
        "vehicle_does_not_stop_here",
        "other"
      ],
      "description": "A string code identifying the nature of the problem"
    },
    "userComment": {
      "type": "string",
      "description": "Additional comment text supplied by the user describing the problem (truncated to 500 characters)"
    },
    "userOnVehicle": {
      "type": "boolean",
      "default": false,
      "description": "Indicates whether the reporting user is on the transit vehicle"
    },
    "userVehicleNumber": {
      "type": "string",
      "description": "The vehicle number as reported by the user"
    },
    "userLat": {
      "type": "number",
      "format": "float",
      "description": "The reporting user's current latitude as a finite float; geographic range is not validated"
    },
    "userLon": {
      "type": "number",
      "format": "float",
      "description": "The reporting user's current longitude as a finite float; geographic range is not validated"
    },
    "userLocationAccuracy": {
      "type": "number",
      "format": "float",
      "description": "The reporting user's location accuracy, in meters"
    },
    "version": {
      "type": "integer",
      "default": 2,
      "description": "API version selector (must be 2)"
    }
  },
  "required": ["id", "key"]
}
```

## Response Structure

### Envelope

```json
{
  "type": "object",
  "properties": {
    "version":     { "type": "integer" },
    "code":        { "type": "integer" },
    "text":        { "type": "string" },
    "currentTime": { "type": "integer", "description": "Unix milliseconds" },
    "data":        { "type": "object" }
  },
  "required": ["version", "code", "text", "currentTime"]
}
```

**`version`** — `2` for Maglev responses and legacy success/validation responses; legacy Java unhandled server error envelopes use `version: 1` (see Defect DC-10).

**`code`** — HTTP status code (`200` on success, `400` for validation error, `401` for unauthorized key, `500` for server error, `504` for deadline exceeded).

**`text`** — Human-readable status description (`"OK"` on success, `"internal server error"`, `"permission denied"`, etc.).

**`currentTime`** — Server wall-clock time in Unix milliseconds when the response was constructed.

**`data`** — On success, an empty JSON object (`{}`). On validation errors, an object containing `fieldErrors`. Omitted on Maglev HTTP 500 server error responses.

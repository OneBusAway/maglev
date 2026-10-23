# Use Case: Report Problem with Stop

## Goal in Context

A rider's client application submits a user-generated problem report regarding a specific transit stop (for example, if the stop name, stop number, or physical location is incorrect, or if an expected route or trip is missing from the stop). The server records the report for review by transit agency operations or data maintainers.

## Scope

OneBusAway REST API — `GET /api/where/report-problem-with-stop/{id}.json`

*(Note: The legacy Java Struts implementation maps `show`, `create`, and `index` to the same handler, accepting both GET and POST requests. Maglev currently routes GET requests to `/api/where/report-problem-with-stop/{id}`.)*

## Level

User goal

## Primary Actor

Rider (via a client application)

## Stakeholders and Interests

- **Rider** — wants an easy, reliable way to report errors or discrepancies with a bus stop, receiving confirmation that their report was recorded.
- **Transit Agency / Data Maintainer** — wants rider feedback to be structured, including coordinates, error codes, and descriptions to identify and fix data or physical signage inaccuracies.
- **Client Application** — expects a standard API envelope response confirming submission or returning validation errors for malformed parameters.

## Preconditions

- The caller supplies a valid API key (`key=<key>`).
- The stop ID parameter `{id}` is provided in the request path in the combined `agencyId_stopId` format (e.g., `1_75403`).

## Minimal Guarantees

- The response envelope always includes `code`, `currentTime`, `text`, and `version`.
- Non-context database storage failures return HTTP 500 with a standard JSON error envelope (`"internal server error"`, `version: 2`); context deadline expirations return HTTP 504, and client-canceled requests return client-canceled responses.
- Malformed coordinates or accuracy values return HTTP 400 with a field validation error envelope.

## Success Guarantees

- The problem report is stored in the database (`problem_reports_stop`) with timestamps (`created_at` and `submitted_at`) set to the current server time in milliseconds.
- Omitted optional parameters are stored as database `NULL`.
- HTTP status 200 is returned with a standard JSON response envelope containing `code: 200`, `text: "OK"`, `version: 2`, and an empty `data` object (`{}`).

## Trigger

A client issues an HTTP request to `GET /api/where/report-problem-with-stop/{id}.json?key=<key>&...`.

## Main Success Scenario

1. The client supplies the stop ID as the path segment `{id}` along with an API key and optional query parameters (`code`, `userComment`, `userLat`, `userLon`, `userLocationAccuracy`).
2. The server extracts the agency ID and stop code by splitting `{id}` on the first underscore character ([`ReportProblemWithStopAction.java#L48–L58`](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/where/ReportProblemWithStopAction.java#L48-L58)).
3. The server parses and validates optional floating-point parameters (`userLat`, `userLon`, `userLocationAccuracy`).
4. If `userComment` is present, the server truncates the text to a maximum length of 500 characters.
5. The server records the report into the database with the raw stop code, problem code, comment, location coordinates, accuracy, and server timestamp ([`ReportProblemWithStopAction.java#L104–L106`](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/where/ReportProblemWithStopAction.java#L104-L106)).
6. The server returns HTTP 200 with an empty OK response envelope (`data: {}`) ([`ReportProblemWithStopAction.java#L108`](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/where/ReportProblemWithStopAction.java#L108)).

## Extensions

**1a. `id` is missing or empty:**
- The server returns HTTP 400 with `code: 400` and `text: "id cannot be empty"`.

**1b. `id` contains no underscore (malformed combined ID):**
- The server returns HTTP 400 with `code: 400` and `text: "invalid combined id format, expected agencyId_code"`.

**2a. Malformed floating-point query parameters (`userLat`, `userLon`, `userLocationAccuracy`):**
- The server rejects non-numeric strings, NaN, or infinite values with HTTP 400 and a JSON validation error response containing `data.fieldErrors`.

**3a. Database failure or database uninitialized:**
- For non-context database errors, the server logs the error and returns HTTP 500 with a standard JSON error envelope (`"internal server error"`, `version: 2`). If a context timeout occurs (`context.DeadlineExceeded`), the server returns HTTP 504; client cancellations use `clientCanceledResponse`.

**4a. Missing or invalid API key:**
- The server returns HTTP 401 with `code: 401` and `text: "permission denied"`.

## Suspected Defects

### Defects that affect the use case

**1. Error response version hardcoded to 1 (defect DC-10).**

In legacy Java Struts, `ExceptionInterceptor.getExceptionAsResponseBean` ([line 72](https://github.com/OneBusAway/onebusaway-application-modules/blob/095bf1ac3aeb7009b9f78e7f1cb4c65bd38b988a/onebusaway-api-webapp/src/main/java/org/onebusaway/api/actions/api/ExceptionInterceptor.java#L72)) constructs error response envelopes with hardcoded `version: 1` regardless of the version requested by the client (version 2), and serializes the raw unhandled exception message in the `text` field. Successful responses use `version: 2`.

*Maglev intentionally corrects this — see [Implementation Decisions](#implementation-decisions).*

## Implementation Decisions

**Standardized 500 error envelope with version 2 (PR #1641, defect DC-10).**

When a non-context database persistence error occurs or the database is uninitialized, Maglev returns a standard OneBusAway JSON error envelope with HTTP status 500, `Content-Type: application/json`, `"version": 2`, `"code": 500`, and `"text": "internal server error"`. (Context deadline expirations return HTTP 504, and canceled requests return client-canceled responses.) This addresses defect DC-10 and avoids leaking internal database exception strings or returning plain text errors.

**Omitted optional parameters stored as database NULL (PR #1641).**

When optional string query parameters (`code`, `userComment`) are omitted or empty, Maglev persists them as SQL `NULL` values using `nulls.NonEmptyString`, matching the legacy Java Struts behavior where unpopulated bean properties remain `null`. Similarly, omitted coordinate parameters (`userLat`, `userLon`, `userLocationAccuracy`) are stored as SQL `NULL`.

**Strict float parameter validation (PR #1643).**

Maglev strictly validates `userLat`, `userLon`, and `userLocationAccuracy` using `utils.ParseFloatParam`. Malformed strings (including `garbage`, `NaN`, `Inf`, and exponents like `1e999`) are rejected with HTTP 400 and an explicit `fieldErrors` structure. Explicit `0` values are accepted and preserved.

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
      "description": "Combined agencyId_stopId identifier in the URL path (e.g. 1_75403)"
    },
    "key": {
      "type": "string",
      "description": "API authentication key"
    },
    "code": {
      "type": "string",
      "enum": [
        "stop_name_wrong",
        "stop_number_wrong",
        "stop_location_wrong",
        "route_or_trip_missing",
        "other"
      ],
      "description": "A string code identifying the nature of the problem"
    },
    "userComment": {
      "type": "string",
      "description": "Additional comment text supplied by the user (truncated to 500 characters)"
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

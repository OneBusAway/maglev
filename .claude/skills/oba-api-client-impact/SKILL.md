# OBA API Client Impact

Assess the impact of a production change (or proposed change) on the three OBA clients: Wayfinder + JS SDK, iOS, and Android.

Typically invoked by `oba-api-review`. Can be called directly when you want to isolate client analysis from other review concerns. Run with the current working directory set to the root of your `maglev` checkout.

## Arguments

1. The endpoint name.
2. The diff source — PR number, branch name, working tree (empty), or a plain-English description of the proposed change.

## Workspace layout

Each client repo is resolved on demand with `.claude/skills/lib/resolve-oba-repo.sh <repo-name>`, which prints the repo's local path — cloning it to a local cache on first use if it isn't already checked out as a sibling of `maglev`. Paths below are relative to that resolved root:

- **JS SDK resource** (repo: `js-sdk`): `src/resources/<endpoint>.ts`, `src/resources/shared.ts`
- **Wayfinder API route** (repo: `wayfinder`): `src/routes/api/oba/<endpoint>/+server.js` (if it exists) — note `.js`, not `.ts`. If the endpoint takes a path parameter, the file lives one level deeper under a bracketed folder named for that parameter, e.g. `src/routes/api/oba/stops-for-route/[routeId]/+server.js` — the bracket name varies per endpoint (`[id]`, `[stopId]`, `[tripId]`, …), so list the endpoint's directory rather than guessing the exact folder name.
- **iOS endpoint method** (repo: `onebusaway-ios`): `OBAKitCore/Network/RESTAPIService/RESTAPIService+Get.swift`
- **iOS response models** (repo: `onebusaway-ios`): `OBAKitCore/Models/REST/` — shared reference models (stop, route, agency, …) live one level deeper, under `OBAKitCore/Models/REST/References/`.
- **Android endpoint definition** (repo: `onebusaway-android`): `onebusaway-android/src/main/java/org/onebusaway/android/api/contract/ObaWebService.kt` — a single Retrofit interface with one `@GET`-annotated method per endpoint, named in camelCase to match (e.g. `stopsForRoute`, `tripDetails`).
- **Android response models** (repo: `onebusaway-android`): `onebusaway-android/src/main/java/org/onebusaway/android/api/contract/ObaApiModels.kt` — a single file with one `kotlinx.serialization` `data class` per response shape, named to match (e.g. `StopsForRoute`).
- **Android data/adapter layer** (repo: `onebusaway-android`, secondary): `onebusaway-android/src/main/java/org/onebusaway/android/api/data/` (per-feature `*DataSource.kt`, e.g. `RouteStopsDataSource.kt`) and `.../api/adapters/` (per-domain `*Adapters.kt`, e.g. `RouteAdapters.kt`) hold business logic and DTO-to-app-model mapping. These aren't named 1:1 per endpoint — grep for the endpoint's Retrofit method name or response model class name to find the consuming files.

This skill always needs all four client repos, so resolve `js-sdk`, `wayfinder`, `onebusaway-ios`, and `onebusaway-android` up front, before starting step 2. First use may take a minute to clone; later runs just refresh the cache.

If a repo was found as a sibling checkout or via `OBA_WORKSPACE` rather than cloned into the cache, the resolver does not update it automatically and instead prints a `Warning: ... commit(s) behind ...` to stderr if it's stale. Watch for these — if one appears, note it as a caveat in the final report (the analysis for that client may be based on out-of-date source) rather than silently proceeding as if the checkout were current.

## Steps

### 1. Identify the changed behaviours

From the diff or description, list every production behaviour that is changing: fields added, removed, or renamed; response structure changes; parameter handling changes; error response changes; default value changes.

For description mode, derive the likely behaviour changes from the stated intent.

### 2. Wayfinder + JS SDK

For each changed behaviour:

- Does `src/resources/<endpoint>.ts` or `src/resources/shared.ts` in the resolved `js-sdk` repo define a TypeScript type that maps the affected field or structure? If a field is removed, will the type need updating? If a field is added, is it missing from the type?
- Does any Wayfinder component or API proxy route in the resolved `wayfinder` repo's `src/` read, display, or depend on the affected field or behaviour?

State: *impact found* (describe it) or *no impact found*.

### 3. iOS

For each changed behaviour:

- Does `OBAKitCore/Network/RESTAPIService/RESTAPIService+Get.swift` in the resolved `onebusaway-ios` repo call this endpoint?
- Does a Swift model in `OBAKitCore/Models/REST/` decode the affected field? Note whether decoding uses `try` (required — will throw if field absent) or optional chaining (will silently produce nil).
- Does any UI code downstream of the model handle a nil value defensively?

State: *impact found* (describe it) or *no impact found*.

### 4. Android

For each changed behaviour:

- Does the endpoint's `@GET` method in `ObaWebService.kt` (in the resolved `onebusaway-android` repo) send any parameter that is now handled differently?
- Does the matching `data class` in `ObaApiModels.kt` map the affected field? These are `kotlinx.serialization` models where most fields carry a default (e.g. `= emptyList()`, `= 0`) rather than being nullable — a missing/renamed field silently deserializes to that default instead of throwing or producing null. Check whether the `*DataSource.kt`/`*Adapters.kt` code downstream treats the default as "absent" or as a legitimate value.

State: *impact found* (describe it) or *no impact found*.

### 5. Report

**Client impact: `<endpoint>`**

For each changed behaviour, one row:

| Behaviour | Wayfinder/SDK | iOS | Android |
|-----------|--------------|-----|---------|
| [description] | impact / none | impact / none | impact / none |

Expand on any *impact found* entries with specifics: what changes for the client and how — field absent, value different, type mismatch, silent null, user-visible difference, etc. Do not assess whether the client should or could be updated; that is the reader's decision.

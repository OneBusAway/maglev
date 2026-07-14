# OBA API Spec Check

Assess whether a change is consistent with the relevant `maglev.wiki` spec, and verify that any deviation from legacy behaviour is recorded in the spec's Implementation Decisions section.

Typically invoked by `oba-api-review`. Can be called directly when you want to isolate spec alignment from other review concerns. Run with the current working directory set to the root of your `maglev` checkout.

## Arguments

1. The endpoint name.
2. The diff source — PR number, branch name, working tree (empty), or a plain-English description of the proposed change.

## Workspace layout

- **Spec**: `<endpoint>.md` in the `maglev.wiki` repo — resolve the repo's local path with `.claude/skills/lib/resolve-oba-repo.sh maglev.wiki` (clones it to a local cache on first use if it isn't already checked out as a sibling of `maglev`).
- **Maglev handler**: `internal/restapi/<endpoint>_handler.go` (relative to the `maglev` repo root) — handler filenames use underscores throughout, e.g. `stops-for-route` → `stops_for_route_handler.go`, so convert every hyphen in the endpoint name to an underscore before looking for the file.

## Steps

### 1. Read the spec in full

Run `.claude/skills/lib/resolve-oba-repo.sh maglev.wiki` to get the wiki repo's local path. If it was found as a sibling checkout or via `OBA_WORKSPACE` rather than cloned into the cache, the resolver doesn't update it automatically and will print a `Warning: ... commit(s) behind ...` to stderr if it's stale — if that happens, note it as a caveat in the final report, since the spec being read may be out of date. Then read `<path>/<endpoint>.md` entirely. Note:
- The specified behaviour for each request parameter, response field, and error case.
- The **Suspected Defects** section — known divergences from ideal behaviour in the legacy implementation.
- The **Implementation Decisions** section — deliberate deviations Maglev has chosen to make, with rationale.

### 2. Get the diff

- **PR**: `gh pr diff <PR> --repo OneBusAway/maglev`
- **Branch**: `git diff main...<branch>`
- **Working tree**: `git diff HEAD`

For description mode, work from the stated intent rather than an actual diff.

### 3. Check spec consistency

For each production change (or proposed change):
- Does the spec permit or require this change?
- Does the change contradict the spec?
- Does the change address a **Suspected Defect** — intentionally or as a side effect?
- Does the change affect behaviour the spec does not yet cover? If so, the spec may need to be updated.

### 4. Check deviation recording

For any change that deviates from legacy behaviour (i.e. touches a Suspected Defect or introduces a deliberate difference):
- Does an **Implementation Decisions** entry already exist for this deviation?
- If not, flag it: the spec must be updated before the change is merged to maintain the audit trail.

For description mode this is prospective: "if this change were implemented, would a new Implementation Decisions entry be required?"

### 5. Report

**Spec check: `<endpoint>`**

For each changed behaviour:
- **Consistent** — change matches what the spec requires or permits.
- **Inconsistent** — change contradicts the spec; describe the conflict.
- **Spec gap** — spec does not cover this behaviour; spec should be updated.
- **Deviation** — change deviates from legacy; Implementation Decisions entry exists ✓ / missing ✗.

Overall: **spec-consistent** / **conflicts noted** / **spec update required**

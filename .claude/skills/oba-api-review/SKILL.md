# OBA API Review

Entry point for reviewing a change to the OBA API. Determines which analyses are relevant and runs them.

Lives inside the `maglev` repo and is meant to be usable with only `maglev` checked out. `oba-api-client-impact` needs source from `wayfinder`, `js-sdk`, `onebusaway-ios`, and `onebusaway-android`; `oba-api-spec-check` and `oba-api-verify` need `maglev.wiki` — they resolve those automatically via the `oba-workspace` skill, which needs `git` and (on first use) network access. See `oba-workspace` for how repos are located and how it handles stale or locally-modified checkouts.

Run this skill with the current working directory set to the root of your `maglev` checkout.

## Argument

The source is one of four forms, optionally followed by a `--output [path]` flag:

| Form | Detection | Example |
|------|-----------|---------|
| PR number | Numeric / `#NNN` | `123` |
| Branch name | Single token, no spaces, not numeric | `fix/route-ids-paging` |
| Description | Contains spaces | `remove the situationIds field from stops-for-route` |
| *(empty)* | No argument | *(none)* → current working tree |

Strip a trailing `--output` or `--output <path>` token from the argument before applying the detection rules above — it's a modifier, not part of the source. Its presence means: also save the final report to a file (step 7). Its absence means the report is only printed inline, as before. If `--output` is given without a path, use the default path described in step 7.

## Steps

### 1. Resolve the diff source

**PR (`123` or `#NNN`):**
```bash
gh pr view <PR> --repo OneBusAway/maglev
```
Note the PR title, body, and any linked issues. Fetch linked spec-gap issue if present:
```bash
gh issue view <issue> --repo OneBusAway/maglev
```

**Branch (single token, not numeric):**
```bash
git diff main...<branch> --name-only
```
Check whether the branch is linked to a GitHub issue. If so, fetch it.

**Description (contains spaces):** No diff to fetch. The argument is the stated intent. Skip to step 3.

**Working tree (empty argument):**
```bash
git diff main...HEAD --name-only
git status --short
```
`git diff main...HEAD` captures the branch's full committed divergence from `main`, not just uncommitted edits — a branch can be entirely committed (and pushed as a PR) with nothing left in the working tree. `git status --short` additionally surfaces any uncommitted changes on top of that. Check whether the current branch is linked to a GitHub issue, or has an open PR (`gh pr list --head <branch>`). If so, fetch it.

### 2. Partition the changes

For PR, branch, and working-tree modes, categorise every changed file:

- **Production changes** — handler code, models, DB queries, middleware, or any non-test file that affects what Maglev returns at runtime.
- **Test changes** — `*_test.go` files, test helpers, fixtures, test-only DB helpers.

Record whether the diff contains production changes, test changes, or both.

### 3. Write the overview

Before running any sub-skills, write an **Overview** section for the report. This orients the reader — someone who may not have read the PR, the issue, or the spec — so they can follow the analysis that comes after.

The overview has two parts:

**What this change does** — a plain-English explanation of the problem and the fix (or feature). Walk through the before/after behaviour concretely: what the endpoint returned before, what it returns after, and under what conditions the difference matters. Use a specific scenario if one helps (e.g. "Route X ends in December but the feed runs until June — querying January used to return Y, now returns Z"). Reference the linked issue and spec section where relevant.

**Domain background** — explain any GTFS, transit-operations, or OBA API concepts that a reader needs to follow the change. Examples: how `calendar` vs `calendar_dates` determine active service; what a "feed end date" is vs a route's service window; what 510/ServiceDateOutOfRange means in the OBA API. Only cover concepts that are actually relevant to *this* change — don't recite the full GTFS spec. Aim for a reader who knows Go and SQL but not transit data.

Sources to draw on:
- The PR description and linked issue
- The `maglev.wiki` spec for the affected endpoint (see `oba-api-spec-check` for how it's resolved)
- GTFS specification concepts (calendar, calendar_dates, routes, trips, service_id)
- The diff itself (for understanding the mechanics of the fix)

### 4. Determine which sub-skills to run

| Condition | Run |
|-----------|-----|
| A gap description is available — linked spec-gap issue on a PR, or description-mode argument | `oba-api-verify` |
| Production changes exist (PR / branch / working tree) | `oba-api-client-impact`, `oba-api-spec-check` |
| Description mode | `oba-api-client-impact`, `oba-api-spec-check` (prospective) |
| Test changes only, no gap description | None — report this and stop |

### 5. Run the relevant sub-skills

Invoke each applicable sub-skill in order, passing the relevant context (gap description, endpoint name, diff source):

1. `oba-api-verify` — if a gap description is available
2. `oba-api-client-impact` — if production changes exist or description mode
3. `oba-api-spec-check` — if production changes exist or description mode

### 6. Synthesise the report

Combine the sub-skill outputs into a single report:

---

## OBA API Review: `<source>` — `<endpoint>`

**Input**: [PR #NNN / branch `<name>` / working tree / proposed change]
**Stated goal**: [from linked issue, description argument, or "none"]
**Changes**: [production / test-only / both / description only]

### Overview
[Plain-English explanation of the change and relevant domain concepts, from step 3.]

### Goal check
[Output of `oba-api-verify`, or "No stated goal — not assessed."]

### Client impact
[Output of `oba-api-client-impact`, or "Test-only changes — not applicable."]

### Spec check
[Output of `oba-api-spec-check`, or "Test-only changes — not applicable."]

### Summary

A short plain-English summary of what the analysis found. Note anything that is incomplete, incorrect, or that the change touches beyond its stated scope. Do not prescribe what should happen — that is the reader's decision.

---

### 7. Save the report to a file (only if `--output` was given)

Skip this step entirely if the `--output` flag wasn't present in the argument — the report printed in step 6 is the only output.

If `--output <path>` was given, write the exact report content from step 6 to `<path>` verbatim (no truncation or reformatting), creating parent directories if needed.

If `--output` was given with no path, write it to `tmp/oba-api-review/<slug>-<timestamp>.md` instead (this directory is already covered by the repo's `.gitignore`, so nothing here gets committed by accident):

```bash
mkdir -p tmp/oba-api-review
```

- `<timestamp>` is `date +%Y%m%d-%H%M%S`
- `<slug>` depends on the resolved source:

| Source | Slug |
|--------|------|
| PR | `pr-<NNN>` |
| Branch | the branch name with `/` replaced by `-` |
| Working tree | `working-tree-<current-branch-name-with-/-replaced-by-->` |
| Description | a short kebab-case slug derived from the description (e.g. `remove-situationids-from-stops-for-route`) |

After writing, tell the user the file path so they can open, share, or diff it against a previous run.

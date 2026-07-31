# OBA Workspace

Resolves local checkout paths for the OBA companion repos (`wayfinder`, `js-sdk`, `onebusaway-ios`, `onebusaway-android`, `maglev.wiki`) and checks their state. `oba-api-review`, `oba-api-client-impact`, `oba-api-spec-check`, and `oba-api-verify` all resolve repos through this skill rather than calling `.claude/skills/lib/resolve-oba-repo.sh` directly, so resolution and anomaly handling live in one place instead of being repeated in each.

## Arguments

One of:
- One or more repo names, e.g. `maglev.wiki` or `wayfinder js-sdk onebusaway-ios onebusaway-android` — resolve these for an upcoming task.
- `status` — report on all five known repos without resolving for any specific task (see [Status mode](#status-mode)).

## Known repos

`wayfinder`, `js-sdk`, `onebusaway-ios`, `onebusaway-android`, `maglev.wiki`

## Steps (resolving specific repos)

### 1. Resolve each requested repo

For each repo name given, run:

```bash
.claude/skills/lib/resolve-oba-repo.sh <repo>
```

capturing stdout (the resolved path) and stderr separately.

### 2. Check each resolved path for anomalies

- **Staleness**: a `Warning: ... commit(s) behind ...` line on the stderr captured above. Only tiers 1-3 (explicit `OBA_WORKSPACE`, `/workspace`, sibling checkout) can produce this — the cache-dir tier is fetched and hard-reset on every run, so it's never stale.
- **Unknown freshness**: a `Note: could not fetch ...` line on the stderr captured above — the staleness check itself failed (offline, or no matching branch upstream), so freshness could not be confirmed either way. Treat this as an anomaly too rather than assuming the checkout is current.
- **Odd repo state**: run `git -C <path> rev-parse --abbrev-ref HEAD` and `git -C <path> status --porcelain` on the resolved path and flag any of:
  - Current branch is not `main` or `master` (or `HEAD`, i.e. detached)
  - Staged changes not yet committed
  - Unstaged modifications or untracked files

### 3. If any repo in this batch has anomalies

Present one consolidated summary covering every affected repo (list each anomaly found, per repo) — do not ask separately per repo. Then use the `AskUserQuestion` tool to ask how to proceed, with exactly these two options:

- **Continue anyway** — proceed with the checkouts as-is. Every anomaly listed must be folded into the calling skill's final report as a caveat.
- **Stop here** — halt the calling skill entirely without proceeding to its analysis steps, so the user can fix things (`git pull`, commit, or stash) and re-run.

### 4. Return to the caller

- No anomalies: just the resolved path(s).
- "Continue anyway": the resolved path(s) plus the full anomaly list, to be included in the caller's final report.
- "Stop here": tell the calling skill to stop immediately — do not proceed to its own analysis.

## Status mode

Invoked with `status` instead of specific repo names: resolve all five known repos (steps 1-2 above, for all of them — this clones any that aren't yet checked out anywhere) and present a table, no prompting:

| Repo | Path | Resolved via | Anomalies |
|------|------|--------------|-----------|

"Resolved via" — infer from the path: under `$OBA_WORKSPACE` if that env var is set, under `/workspace`, a sibling of the `maglev` checkout, or the cache dir (whichever prefix matches).

This mode is read/report-only — it never prompts, since it isn't a precondition for anything else proceeding.

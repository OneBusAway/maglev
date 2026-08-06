# OBA API review skills

Four [Claude Code](https://claude.com/claude-code) skills for reviewing changes to the maglev OBA REST API:

| Skill | Purpose |
|---|---|
| `oba-api-review` | Entry point. Figures out which of the other three apply and runs them. |
| `oba-api-verify` | Checks whether a change fully accomplishes a stated goal (a spec-gap issue, PR description, etc.), including test coverage. |
| `oba-api-client-impact` | Checks whether a change affects Wayfinder + the JS SDK, iOS, or Android. |
| `oba-api-spec-check` | Checks a change against the endpoint's `maglev.wiki` spec, and that any deliberate deviation from legacy behaviour is recorded. |

Usually you only need `oba-api-review` — it dispatches to the other three as appropriate. Call one of the others directly if you want to isolate a single concern (e.g. just the client-impact analysis).

**A note on spec changes:** `oba-api-spec-check` will sometimes flag that `maglev.wiki` needs updating — a missing Implementation Decisions entry, a gap in coverage, and so on. It only flags this; it doesn't edit the wiki. Treat any actual spec change as needing broader review than the PR it came up in — raise it in Slack first rather than updating the wiki unilaterally.

## Where these came from

These skills were built to support the [OBA API Spec Review project](https://github.com/orgs/OneBusAway/projects/11), which tracks reviewing all 27 OneBusAway API endpoints' behavioural specs against Maglev's implementation: each endpoint gets reviewed, gaps get filed as linked `spec-gap` issues, and PRs close them out.

That said, nothing here is specific to that project. Point `oba-api-review` at any PR, branch, or working tree that touches the maglev API — a bug fix, a new endpoint, an unrelated feature — and it'll do the same analysis: goal verification (if there's a stated goal), spec consistency, and client impact.

## Prerequisites

- [Claude Code](https://claude.com/claude-code), run from the root of a `maglev` checkout.
- `git`, and network access the first time you use a skill that needs one of the other OBA repos (see below) — later runs reuse what's already been fetched.
- [`gh`](https://cli.github.com/), authenticated against the `OneBusAway` org, for PR-number and linked-issue lookups.

## Using them

```
/oba-api-review 123                                   # PR #123 in OneBusAway/maglev
/oba-api-review fix/route-ids-paging                  # a branch, diffed against main
/oba-api-review                                        # current working tree
/oba-api-review "remove situationIds from stops-for-route"   # a proposed change, described in English
```

Add `--output` to also save the report as markdown, in addition to printing it inline:

```
/oba-api-review 123 --output                          # saves to tmp/oba-api-review/pr-123-<timestamp>.md
/oba-api-review fix/route-ids-paging --output notes/review.md   # saves to a specific path
```

Without `--output`, the report is only printed inline, as before.

## How cross-repo resolution works

`oba-api-client-impact` needs source from `wayfinder`, `js-sdk`, `onebusaway-ios`, and `onebusaway-android`; `oba-api-spec-check` and `oba-api-verify` need `maglev.wiki` — none of which need to be checked out ahead of time. They resolve each one through the `oba-workspace` skill, which in turn calls `.claude/skills/lib/resolve-oba-repo.sh <repo-name>`:

1. `$OBA_WORKSPACE/<repo>`, if you've set that env var to point at a directory containing your own checkout — cloned in if not already there.
2. `/workspace/<repo>`, if `$OBA_WORKSPACE` isn't set and `/workspace` exists — the conventional bind-mount point in sandboxed VMs, so a checkout here can be mounted back to the host. Same clone-if-missing behavior as (1).
3. A sibling directory of your `maglev` checkout (i.e. `../<repo>`), if you happen to have the other repos checked out that way already. Only used if it already exists — never cloned.
4. Otherwise, it's cloned into a local cache (`~/.cache/oba-api-review` by default, override with `OBA_SKILL_CACHE`) the first time it's needed, and kept up to date automatically on later runs.

Repos found via (1), (2) or (3) are places you might be editing directly (e.g. to push changes to `maglev.wiki` from the host), so this script never modifies an existing checkout there — each is only checked (read-only) against its upstream. Repos in (4) are fully owned by this script and kept fresh automatically instead.

`oba-workspace` sits in front of the script for all of this: it resolves repos on behalf of the other skills, and if any resolved checkout is stale, has unconfirmed freshness (the staleness check itself failed to fetch), or is in an odd state (not on `main`/`master`, staged changes, uncommitted or untracked files), it stops and asks you how to proceed before the calling skill continues. You can also invoke `oba-workspace status` directly at any time to see where all five repos currently resolve to and whether any look off.

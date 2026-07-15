# OBA API review skills

Four [Claude Code](https://claude.com/claude-code) skills for reviewing changes to the maglev OBA REST API:

| Skill | Purpose |
|---|---|
| `oba-api-review` | Entry point. Figures out which of the other three apply and runs them. |
| `oba-api-verify` | Checks whether a change fully accomplishes a stated goal (a spec-gap issue, PR description, etc.), including test coverage. |
| `oba-api-client-impact` | Checks whether a change affects Wayfinder + the JS SDK, iOS, or Android. |
| `oba-api-spec-check` | Checks a change against the endpoint's `maglev.wiki` spec, and that any deliberate deviation from legacy behaviour is recorded. |

Usually you only need `oba-api-review` — it dispatches to the other three as appropriate. Call one of the others directly if you want to isolate a single concern (e.g. just the client-impact analysis).

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

## How cross-repo resolution works

`oba-api-client-impact` and `oba-api-spec-check` need source from `wayfinder`, `js-sdk`, `onebusaway-ios`, `onebusaway-android`, and `maglev.wiki` — none of which need to be checked out ahead of time. `.claude/skills/lib/resolve-oba-repo.sh <repo-name>` finds each one automatically:

1. `$OBA_WORKSPACE/<repo>`, if you've set that env var to point at a directory containing your own checkout.
2. A sibling directory of your `maglev` checkout (i.e. `../<repo>`), if you happen to have the other repos checked out that way already.
3. Otherwise, it's cloned into a local cache (`~/.cache/oba-api-review` by default, override with `OBA_SKILL_CACHE`) the first time it's needed, and kept up to date automatically on later runs.

Repos found via (1) or (2) aren't managed by this script, so they're never modified — instead, each is checked (read-only) against its upstream, and a skill's output will flag it if the checkout looks stale enough that the analysis might be based on outdated code.

<!-- maglev-pr-template:v1 -->
<!-- ^ Keep the marker line above this comment.

     Draft pull requests may leave boxes unchecked. Before marking a pull request
     ready for review, check every box under Checklist.

     An explanation does not replace a required check. If a statement is not true
     yet, keep the pull request as Draft.

     Everything else, including these guidance comments, is yours to delete. -->

Fixes #
<!-- An issue a maintainer has triaged or assigned to you (see CONTRIBUTING.md). Use "Part of #N" if this doesn't fully close it. -->

## What it does

<!-- 1–3 sentences: the problem and the fix. Call out any behavior that differs from other handlers or from testdata/openapi.yml. -->

## Root cause

<!-- In your own words. For a bug fix: why it happened. For a feature: the gap it fills. -->

## Testing

<!-- What you actually ran. Paste real output, not descriptions. CI already runs fmt, vet and the tests. -->

- **Test failing on `main` before this change:** <!-- The failing assertion. Write "n/a" if this isn't a bug fix. -->
- **Manual check:** <!-- The request or command you ran, and what you saw. -->

## Response comparison (delete if no Java-served response changed)

<!-- The same request against Maglev and Java. JSON (wrap long blocks in <details>), screenshots or validator output are all fine. -->

- **Request URL(s):**
- **Java server, feeds and date checked:** <!-- If you read Java's source instead of running it, say so and link file:line. -->
- **Maglev before:**
- **Maglev after:**
- **Java:**
- **If Maglev intentionally differs from Java:** <!-- Why Java is wrong. Link the onebusaway-application-modules issue if one exists. -->

## Checklist

**Ready-for-review requirement:** Every box must be checked. If any statement is not true, keep the pull request
as Draft.

- [ ] This PR addresses one issue.
- [ ] It is under ~200 changed lines, or the description says why it can't be split.
- [ ] For a bug fix, the new or changed test fails on `main` and passes on this branch.
- [ ] Each commit is one logical change with a clear message.
- [ ] I did not hand-edit `gtfsdb/` (sqlc-generated) or `testdata/openapi.yml` (synced from upstream).
- [ ] Handlers pass `r.Context()` to DB/service calls, not `context.Background()`.
- [ ] The response comparison above is filled in, or this PR doesn't change a Java-served response.
- [ ] Any new endpoint matches `testdata/openapi.yml`, or an issue asking for it is open on OneBusAway/sdk-config.
- [ ] I read every line of this diff and can explain it if asked.

## Notes for reviewers (optional)

<!-- The riskiest part of this change, follow-ups, scope limits, spec/wiki discrepancies. -->

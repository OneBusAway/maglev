# Instructions for AI Agents Working in docs/

## Keep the wiki in sync with this folder

The [Maglev Wiki](https://github.com/OneBusAway/maglev/wiki) is the
canonical home for documentation and specs (see [README.md](README.md)),
and several wiki pages are mirrors of files in this folder. **Whenever you
change a spec here, push the updated content to its wiki page in the same
piece of work** — a spec change that lands in the repo but not on the wiki
leaves the canonical copy stale.

Current file ↔ page mapping:

| Repo file | Wiki page |
|---|---|
| `docs/horizontal-scalability-spec.md` | [PostgreSQL-Support](https://github.com/OneBusAway/maglev/wiki/PostgreSQL-Support) |
| `docs/multi-agency-static-feeds-spec.md` | [Multi-Agency-Static-Feeds-Stop-Consolidation](https://github.com/OneBusAway/maglev/wiki/Multi-Agency-Static-Feeds-Stop-Consolidation) |

Keep this table current: when a new spec in this folder gains a wiki page
(or a page is renamed), add or update its row.

## How to sync

The wiki is a plain git repository. Mirrored pages are verbatim copies of
their repo files — no reformatting, no wiki-only edits:

```bash
git clone https://github.com/OneBusAway/maglev.wiki.git
cp docs/horizontal-scalability-spec.md maglev.wiki/PostgreSQL-Support.md
cp docs/multi-agency-static-feeds-spec.md \
   maglev.wiki/Multi-Agency-Static-Feeds-Stop-Consolidation.md
git -C maglev.wiki add -A
git -C maglev.wiki commit -m "Sync specs from repo"
git -C maglev.wiki push
```

Before syncing, diff the wiki copy against the repo file. If the wiki side
has changes the repo file lacks, someone edited the wiki directly — merge
those edits back into the repo file first (the two must not diverge), then
push the merged result to both.

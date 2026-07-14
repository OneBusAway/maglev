#!/usr/bin/env bash
# resolve-oba-repo.sh <repo-name>
#
# Prints the local filesystem path to a checkout of one of the other OBA
# repos used by the oba-api-review skill family (oba-api-review,
# oba-api-client-impact, oba-api-spec-check, oba-api-verify). These skills
# ship inside maglev/ and must work with only maglev checked out, so this
# script resolves each dependent repo on demand instead of assuming a fixed
# multi-repo workspace layout.
#
# Resolution order:
#   1. $OBA_WORKSPACE/<repo>        - explicit override, if set
#   2. <maglev-root>/../<repo>      - sibling checkout, if this maglev
#                                      checkout lives in a multi-repo workspace
#   3. cache dir, cloning if absent - fallback for a maglev-only checkout
#
# Repos found via (1) or (2) were checked out independently of this script,
# so it never mutates them - it only does a read-only freshness check
# (fetch + compare, no reset/pull) and prints a warning to stderr if the
# checkout looks behind its own upstream. Repos in the cache dir (3) *are*
# managed by this script, so those are kept up to date automatically
# instead of just flagged.
#
# Only the resolved path is printed to stdout; all progress/diagnostic
# output goes to stderr, so this is safe to use as:
#   path="$(.claude/skills/lib/resolve-oba-repo.sh wayfinder)"

set -euo pipefail

usage() {
  echo "Usage: $(basename "$0") <repo-name>" >&2
  echo "Known repos: wayfinder, js-sdk, onebusaway-ios, onebusaway-android, maglev.wiki" >&2
}

REPO="${1:-}"
if [ -z "$REPO" ]; then
  usage
  exit 1
fi

case "$REPO" in
  wayfinder)          URL="https://github.com/onebusaway/wayfinder.git" ;;
  js-sdk)              URL="https://github.com/onebusaway/js-sdk.git" ;;
  onebusaway-ios)      URL="https://github.com/onebusaway/onebusaway-ios.git" ;;
  onebusaway-android)  URL="https://github.com/onebusaway/onebusaway-android.git" ;;
  maglev.wiki)         URL="https://github.com/onebusaway/maglev.wiki.git" ;;
  *)
    echo "Unknown repo: $REPO" >&2
    usage
    exit 1
    ;;
esac

# Read-only staleness check for a checkout this script does not manage
# (found via OBA_WORKSPACE or as a sibling). Fetches the current branch's
# upstream and compares - never resets/pulls/mutates the working tree.
warn_if_stale() {
  local path="$1"
  local branch upstream_ref local_rev remote_rev behind last_date

  branch="$(git -C "$path" symbolic-ref --short HEAD 2>/dev/null || echo "")"
  if [ -z "$branch" ]; then
    echo "Note: $REPO at $path is in a detached HEAD state; skipping staleness check." >&2
    return
  fi

  if ! git -C "$path" fetch --quiet origin "$branch" 2>/dev/null; then
    echo "Note: could not fetch to check staleness of $REPO at $path (offline, or no matching branch upstream)." >&2
    return
  fi

  upstream_ref="refs/remotes/origin/$branch"
  if ! git -C "$path" rev-parse --verify --quiet "$upstream_ref" >/dev/null; then
    return
  fi

  local_rev="$(git -C "$path" rev-parse HEAD)"
  remote_rev="$(git -C "$path" rev-parse "$upstream_ref")"
  if [ "$local_rev" != "$remote_rev" ]; then
    behind="$(git -C "$path" rev-list --count "HEAD..$upstream_ref" 2>/dev/null || echo "?")"
    last_date="$(git -C "$path" log -1 --format=%ad --date=short)"
    echo "Warning: $REPO at $path (branch $branch) is $behind commit(s) behind origin/$branch (last local commit: $last_date). This checkout was found independently, not cloned by this script, so it was not updated automatically - results below may reflect stale code. Run 'git -C $path pull' to update it, or unset OBA_WORKSPACE / move it aside to let this script manage a fresh copy in its cache instead." >&2
  fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 1. Explicit workspace override
if [ -n "${OBA_WORKSPACE:-}" ] && [ -d "$OBA_WORKSPACE/$REPO" ]; then
  warn_if_stale "$OBA_WORKSPACE/$REPO"
  echo "$OBA_WORKSPACE/$REPO"
  exit 0
fi

# 2. Sibling of the maglev checkout this script lives in (script is at
#    <maglev-root>/.claude/skills/lib/resolve-oba-repo.sh)
MAGLEV_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SIBLING="$(dirname "$MAGLEV_ROOT")/$REPO"
if [ -d "$SIBLING/.git" ]; then
  warn_if_stale "$SIBLING"
  echo "$SIBLING"
  exit 0
fi

# 3. Cache dir - clone on first use, best-effort refresh on later runs
CACHE_ROOT="${OBA_SKILL_CACHE:-$HOME/.cache/oba-api-review}/repos"
TARGET="$CACHE_ROOT/$REPO"

if [ -d "$TARGET/.git" ]; then
  BRANCH="$(git -C "$TARGET" symbolic-ref --short HEAD 2>/dev/null || echo "")"
  if [ -n "$BRANCH" ]; then
    echo "Updating cached checkout of $REPO..." >&2
    if git -C "$TARGET" fetch --depth 1 origin "$BRANCH" >&2; then
      git -C "$TARGET" reset --hard "origin/$BRANCH" >&2 || true
    else
      echo "Warning: fetch failed, using cached copy of $REPO as-is (last commit: $(git -C "$TARGET" log -1 --format=%ad --date=short))" >&2
    fi
  else
    echo "Note: cached checkout of $REPO at $TARGET is in a detached HEAD state; leaving as-is (last commit: $(git -C "$TARGET" log -1 --format=%ad --date=short))." >&2
  fi
else
  echo "Cloning $REPO into $TARGET (first use)..." >&2
  mkdir -p "$CACHE_ROOT"
  git clone --depth 1 "$URL" "$TARGET" >&2
fi

echo "$TARGET"

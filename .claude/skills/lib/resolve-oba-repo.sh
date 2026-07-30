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
#   2. /workspace/<repo>            - deterministic default, if $OBA_WORKSPACE
#                                      is unset and /workspace exists (the
#                                      conventional bind-mount point in
#                                      sandboxed VMs, so checkouts land
#                                      somewhere that can be mounted back to
#                                      the host)
#   3. <maglev-root>/../<repo>      - sibling checkout, if this maglev
#                                      checkout lives in a multi-repo workspace
#   4. cache dir, cloning if absent - final fallback for a maglev-only
#                                      checkout with no /workspace available
#
# Tiers 1-3 are places a human might be editing directly (that's the whole
# point of $OBA_WORKSPACE and /workspace - e.g. to push changes to
# maglev.wiki from the host), so this script only ever clones into them if
# the repo isn't there yet; once a checkout exists there, it's never
# mutated - no fetch+reset, no pull. The only thing done to an existing
# checkout in tiers 1-3 is a read-only freshness check (fetch + compare)
# that prints a warning to stderr if it looks behind its own upstream.
#
# Tier 4 (the cache dir) is fully owned by this script - nothing else is
# expected to edit it - so it's kept up to date automatically: cloned on
# first use, then fetched and hard-reset to match its upstream branch on
# every later run.
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
if [[ -z "$REPO" ]]; then
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

# Read-only staleness check for an existing checkout. Fetches the current
# branch's upstream and compares - never resets/pulls/mutates the working
# tree or local branch.
warn_if_stale() {
  local path="$1"
  local branch upstream_ref local_rev remote_rev behind last_date quoted_path

  branch="$(git -C "$path" symbolic-ref --short HEAD 2>/dev/null || echo "")"
  if [[ -z "$branch" ]]; then
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
  if [[ "$local_rev" != "$remote_rev" ]]; then
    behind="$(git -C "$path" rev-list --count "HEAD..$upstream_ref" 2>/dev/null || echo "?")"
    if [[ "$behind" != "0" ]]; then
      last_date="$(git -C "$path" log -1 --format=%ad --date=short)"
      quoted_path="$(printf '%q' "$path")"
      echo "Warning: $REPO at $path (branch $branch) is $behind commit(s) behind origin/$branch (last local commit: $last_date). This script never mutates an existing checkout, so it was not updated automatically - results below may reflect stale code. Run 'git -C $quoted_path pull' to update it yourself." >&2
    fi
  fi
}

# True if $1 is a checkout (regular or worktree - a worktree's top-level
# .git is a file, not a directory) whose origin matches $URL.
is_expected_checkout() {
  local target="$1" actual_url
  git -C "$target" rev-parse --show-toplevel >/dev/null 2>&1 || return 1
  actual_url="$(git -C "$target" config --get remote.origin.url 2>/dev/null || echo "")"
  [[ "$actual_url" == "$URL" ]]
}

# Clones $URL into $1 if it isn't already a git checkout there; otherwise
# verifies it's actually a checkout of $URL and runs the read-only staleness
# check. Never mutates an existing checkout.
resolve_target() {
  local target="$1"
  if [[ -e "$target/.git" ]]; then
    if ! is_expected_checkout "$target"; then
      echo "Error: $target exists but is not a git checkout of $URL. Refusing to use it - move it aside, or point OBA_WORKSPACE/your workspace layout elsewhere." >&2
      exit 1
    fi
    warn_if_stale "$target"
  else
    echo "Cloning $REPO into $target (first use)..." >&2
    mkdir -p "$(dirname "$target")"
    git clone --depth 1 "$URL" "$target" >&2
  fi
  echo "$target"
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 1. Explicit workspace override - clone into it if the repo isn't there yet,
#    otherwise read-only.
if [[ -n "${OBA_WORKSPACE:-}" ]]; then
  resolve_target "$OBA_WORKSPACE/$REPO"
  exit 0
fi

# 2. Deterministic default: /workspace, if present. Same clone-if-absent /
#    read-only-otherwise handling as (1).
if [[ -d /workspace ]]; then
  resolve_target "/workspace/$REPO"
  exit 0
fi

# 3. Sibling of the maglev checkout this script lives in (script is at
#    <maglev-root>/.claude/skills/lib/resolve-oba-repo.sh). Read-only only -
#    a sibling that doesn't exist isn't a sibling, so this tier never clones.
MAGLEV_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SIBLING="$(dirname "$MAGLEV_ROOT")/$REPO"
if [[ -d "$SIBLING/.git" ]]; then
  warn_if_stale "$SIBLING"
  echo "$SIBLING"
  exit 0
fi

# 4. Cache dir - final fallback, fully managed by this script: cloned on
#    first use, then kept up to date automatically (fetch + reset --hard)
#    on every later run, since nothing else is expected to edit it.
CACHE_ROOT="${OBA_SKILL_CACHE:-$HOME/.cache/oba-api-review}/repos"
TARGET="$CACHE_ROOT/$REPO"

if [[ -d "$TARGET/.git" ]]; then
  BRANCH="$(git -C "$TARGET" symbolic-ref --short HEAD 2>/dev/null || echo "")"
  if [[ -n "$BRANCH" ]]; then
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

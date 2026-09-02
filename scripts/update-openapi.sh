#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPSTREAM_URL="https://raw.githubusercontent.com/OneBusAway/sdk-config/main/stainless/openapi.yml"
DEST="$REPO_ROOT/testdata/openapi.yml"
TMP="$(mktemp /tmp/openapi.XXXXXX.yml)"

cleanup() { rm -f "$TMP"; }
trap cleanup EXIT

echo "Fetching upstream OpenAPI spec from sdk-config..."
curl -sSfL "$UPSTREAM_URL" -o "$TMP"

printf '# Source: https://github.com/OneBusAway/sdk-config/blob/main/stainless/openapi.yml\n# Fetched: %s\n' "$(date +%Y-%m-%d)" > "$DEST"
cat "$TMP" >> "$DEST"

echo "Updated $DEST"

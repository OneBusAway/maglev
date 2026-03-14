#!/usr/bin/env bash
set -euo pipefail

UPSTREAM_URL="https://raw.githubusercontent.com/OneBusAway/sdk-config/main/openapi.yml"
DEST="testdata/openapi.yml"
TMP="$(mktemp /tmp/openapi.XXXXXX.yml)"

cleanup() { rm -f "$TMP"; }
trap cleanup EXIT

echo "Fetching upstream OpenAPI spec from sdk-config..."
curl -sSfL "$UPSTREAM_URL" -o "$TMP"

printf '# Source: https://github.com/OneBusAway/sdk-config/blob/main/openapi.yml\n# Fetched: %s\n' "$(date +%Y-%m-%d)" > "$DEST"
cat "$TMP" >> "$DEST"

echo "Updated $DEST"

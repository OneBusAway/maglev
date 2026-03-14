#!/usr/bin/env bash
set -euo pipefail

UPSTREAM_URL="https://raw.githubusercontent.com/OneBusAway/sdk-config/main/openapi.yml"
LOCAL="testdata/openapi.yml"
TMP="$(mktemp /tmp/openapi.XXXXXX.yml)"

cleanup() { rm -f "$TMP"; }
trap cleanup EXIT

echo "Checking upstream OpenAPI spec for changes..."
curl -sSfL "$UPSTREAM_URL" -o "$TMP"

# Skip the 2-line header when comparing
if tail -n +3 "$LOCAL" | cmp -s "$TMP" -; then
    echo "openapi.yml is up to date with upstream"
else
    echo "WARNING: upstream openapi.yml has changed. Run 'make update-openapi' to update."
    echo "If you find issues in the upstream spec, open an issue at: https://github.com/OneBusAway/sdk-config/issues"
    exit 1
fi

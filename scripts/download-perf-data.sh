#!/usr/bin/env bash
# Download TriMet GTFS for performance benchmarking. Saves to testdata/perf/trimet.zip
# Run from repo root.

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$REPO_ROOT/testdata/perf"
ZIP_PATH="$OUT_DIR/trimet.zip"
URL="https://developer.trimet.org/schedule/gtfs.zip"

if [ -f "$ZIP_PATH" ]; then
  echo "Already exists: $ZIP_PATH (skip download)"
  exit 0
fi

mkdir -p "$OUT_DIR"
echo "Downloading TriMet GTFS to $ZIP_PATH ..."
if command -v curl &>/dev/null; then
  curl -sSL -o "$ZIP_PATH" "$URL"
elif command -v wget &>/dev/null; then
  wget -q -O "$ZIP_PATH" "$URL"
else
  echo "Error: need curl or wget"
  exit 1
fi
echo "Done: $ZIP_PATH"

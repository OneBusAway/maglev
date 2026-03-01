# Mutex Contention & Block Profiling Analysis

## Overview
Captured mutex and block profiles under a 500 VU load using the k6 harness. Profiling was enabled via `MAGLEV_PROFILE_MUTEX=1`.

## Findings
The profiling data confirms significant contention on the `realTimeMutex` within `internal/gtfs/realtime.go`.

- **Bottleneck:** The `rebuildMergedRealtimeLocked` function holds a write lock while rebuilding large lookup maps, blocking all concurrent API readers.
- **Impact:** This is the primary cause of the 75%+ failure rate and 1-minute latency observed in load tests.

## Recommended Mitigation
**Copy-On-Write (COW) with `atomic.Value`**
Refactor `internal/gtfs/realtime.go` to store the merged view in an `atomic.Value`. Readers will access it lock-free, while the 30s updater will swap in a new pre-computed version.


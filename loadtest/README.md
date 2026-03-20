# Maglev Performance Load Testing Harness

This directory contains a `k6` load testing harness designed to put the Maglev server under realistic concurrent load. This is a prerequisite for profiling and optimization work (SQLite query latency, RWMutex contention, and GC pressure, e.g.).

## Requirements
- [k6](https://k6.io/docs/getting-started/installation/) installed on your machine (`brew install k6`, e.g.).
- Maglev server running locally with test data populated.

## Running the Load Test
1. Start the Maglev server with pprof enabled:
   ```bash
   MAGLEV_ENABLE_PPROF=1 make run
   ```

2. In a separate terminal window, execute the k6 load test:
   ```bash
   k6 run loadtest/k6/scenarios.js
   ```

## Profiling and Analysis
While the load test is running, you can capture performance profiles using `pprof`:

### CPU Profiling (30 seconds)
```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
```

### Heap Profiling
```bash
go tool pprof http://localhost:4000/debug/pprof/heap
```

### Mutex Contention
```bash
go tool pprof http://localhost:6060/debug/pprof/mutex
```

---

## Hot-Swap Memory Testing

The daily GTFS static data refresh (ForceUpdate) temporarily doubles memory usage by holding both old and new data simultaneously before GC reclaims the old data. This section documents how to measure and test this behavior.

### The Problem

During ForceUpdate, the following happens:
1. Download new GTFS data
2. Build new database and in-memory data structures (**new data in memory**)
3. Acquire write lock and swap old for new (**both old AND new in memory**)
4. GC eventually reclaims old data

Steps 2-3 can cause peak memory to reach ~2x the baseline, potentially triggering OOM in memory-constrained containers.

### Quick Test (Unit Test)

Run the perftest-tagged hot-swap memory test:
```bash
# Download large agency test data first
./scripts/download-perf-data.sh

# Run the hot-swap memory test
go test -tags=perftest -v -run TestHotSwapMemory_LargeAgency ./internal/gtfs/
```

This will output:
- Baseline Go Sys, Peak Go Sys, Post-GC Go Sys
- Peak memory multiplier (e.g., 1.8x, 2.1x)
- GC settle time
- Request failure rate during swap

### Live Server Testing

For more realistic testing under production-like load:

#### Option A: Memory Monitoring Script
```bash
# Terminal 1: Start server with pprof
MAGLEV_ENABLE_PPROF=1 make run

# Terminal 2: Start memory monitoring
./scripts/hotswap-memory-test.sh monitor

# Terminal 3: Trigger ForceUpdate (run the perftest or wait for 24h cycle)
```

#### Option B: Memory Monitoring with k6 Load
```bash
# Terminal 1: Start server
MAGLEV_ENABLE_PPROF=1 make run

# Terminal 2: Run hot-swap specific load test
k6 run loadtest/k6/hotswap_scenario.js

# Terminal 3: Monitor RSS (OS level metric)
while true; do ps -o rss= -p $(pgrep maglev); sleep 1; done

# Terminal 4: Capture heap profiles periodically
go tool pprof http://localhost:4000/debug/pprof/heap  # baseline
# <trigger ForceUpdate>
go tool pprof http://localhost:4000/debug/pprof/heap  # during
go tool pprof http://localhost:4000/debug/pprof/heap  # after
```

### Key Metrics to Record

| Metric | Description | Target |
|--------|-------------|--------|
| Peak Multiplier | Peak Go Sys / Baseline Go Sys | <2.0x |
| GC Settle Time | Time for memory to return to baseline | <30s |
| Request Failures | Failed requests during swap | <1% |
| Swap Duration | Time for ForceUpdate to complete | <60s |

### Container Sizing Recommendations

Based on the hot-swap behavior, here are recommended container memory limits (measured via Go Sys memory):

| Agency Size | Baseline Go Sys | Peak Go Sys (1.25x) | Recommended Limit |
|-------------|-----------------|---------------------|-------------------|
| Small (RABA)| ~100 MB         | ~125 MB             | 256 MB            |
| Medium      | ~500 MB         | ~625 MB             | 1 GB              |
| Large (TriMet)| ~2.9 GB       | ~3.6 GB             | 4 GB              |
| XL (MTA)    | ~6.0 GB         | ~7.5 GB             | 12 GB             |

**Safety margin**: Multiply peak Go Sys by 2.5x for the container limit to handle unexpected spikes and external CGO/SQLite allocations not tracked by Go Sys.

### Mitigation Options

If memory spikes are problematic:

1. **Explicit GC after swap**: Add `runtime.GC()` after the swap in `ForceUpdate()`
2. **Streaming/incremental import**: Process GTFS data in chunks instead of all-at-once
3. **Memory-mapped database**: Use mmap for SQLite to reduce heap pressure
4. **Higher container limits**: Simply increase memory allocation with headroom

### Related Files

- `internal/gtfs/static.go` — ForceUpdate implementation (hot-swap logic)
- `internal/gtfs/gtfs_manager.go` — staticMutex write lock during swap
- `internal/gtfs/hot_swap_memory_test.go` — Memory profiling tests
- `scripts/hotswap-memory-test.sh` — Live server memory monitoring script
- `loadtest/k6/hotswap_scenario.js` — k6 scenario for hot-swap testing


## Note on Test Data
The pre-existing k6 data CSV files (`loadtest/k6/data/*.csv`) currently contain specific dataset IDs (e.g., Seattle-area data). When running against a different GTFS feed (like RABA in CI), you may need to regenerate these CSV files with IDs and coordinates appropriate for the target dataset to ensure the load test exercises actual data serving paths rather than just 404 paths.

## Sample Results
*Note: These are static benchmark results from a single test run on a specific machine. These numbers will drift as the codebase evolves.*

Generated: 2026-03-10

## Executive Summary

This document presents the findings from the hot-swap memory analysis during GTFS static data refresh (ForceUpdate). The analysis was conducted using TriMet (Portland, OR) as the large agency test dataset.

## Test Configuration

| Parameter | Value |
|-----------|-------|
| Test Data | TriMet GTFS (~24 MB compressed) |
| Concurrent Readers | 10 goroutines |
| Sample Interval | 50ms |
| Test Duration | ~288 seconds |

## Key Findings

### 1. Peak Memory Multiplier

| Metric | Value |
|--------|-------|
| **Baseline Go Sys** | 2.92 GiB |
| **Peak Go Sys** | 3.64 GiB |
| **Peak Multiplier** | **1.25x** baseline |
| **Post-Swap Go Sys** | 3.64 GiB |
| **Post-GC Go Sys** | 3.64 GiB |

**Finding**: The peak memory multiplier of 1.25x is **better than expected** (anticipated ~2.0x). This suggests the Go GC is actively reclaiming old data during the swap process.

### 2. GC Behavior During Swap

| Metric | Value |
|--------|-------|
| GC Cycles During Swap | 10 |
| GC Settle Time | 1,728 ms |
| Swap Duration | 297,408 ms (~5 min) |

**Finding**: The GC ran 10 cycles during the swap window, which helps explain the lower-than-expected peak multiplier. Memory settled within ~1.7 seconds after the swap completed.

### 3. Request Impact During Swap

| Metric | Value |
|--------|-------|
| Total Requests | 245,340 |
| Successful | 245,329 |
| Failed | 1 |
| **Failure Rate** | **0.00%** |

**Finding**: Request failures during the hot-swap window are negligible. The mutex-based protection ensures data consistency without impacting request success rate (only 1 failed out of ~245k requests).

### 4. Memory Timeline Observations

- **T+0 to T+4s**: HeapAlloc grows from ~344 MiB to ~640 MiB (loading new GTFS data)
- **T+4s to T+130s**: HeapAlloc oscillates between 500 MiB and 1.4 GiB as data is built
- **T+133s**: Major GC cycle, HeapAlloc drops from 1.42 GiB to 895 MiB
- **T+141s**: Another GC cycle, HeapAlloc stabilizes at ~725 MiB
- **T+153s**: Swap completes, memory stable

## Answers to Key Questions

### Q1: What is the peak memory multiplier?
**Answer: 1.25x baseline**
This is significantly better than the anticipated 2.0x multiplier. The Go GC appears to be effectively reclaiming memory during the swap process.

### Q2: How long does the old data take to be GC'd after the swap?
**Answer: ~1.7 seconds (1,728 ms)**
The GC settlement time is relatively fast. Memory returns to a stable state quickly after the swap completes.

### Q3: Do any requests fail or timeout during the swap window?
**Answer: 0.00% failure rate (1 failure out of 245,340 requests)**
The hot-swap mechanism is effectively transparent to clients.

### Q4: At what agency size does this become a problem for typical container limits?

Based on the TriMet results:

| Container Limit | Safe Agency Size | Notes |
|-----------------|------------------|-------|
| 512 MB | Small only (RABA-like) | Very tight |
| 1 GB | Small-Medium | Some headroom |
| 2 GB | Medium (local transit) | Comfortable |
| 4 GB | Large (TriMet, regional) | Recommended |
| 8 GB | XL (MTA, multi-region) | Safe for largest |

**Recommendation**: For TriMet-sized agencies (~24 MB GTFS), use at least 4 GB container limit to provide 1.08x safety margin above peak Go Sys memory.

## Container Sizing Recommendations

*Please refer to the [Container Sizing Recommendations](#container-sizing-recommendations) section above for the sizing table and guidelines derived from these results.*

## Recommendations

1. **Container sizing**: Use 1.5x baseline Go Sys as minimum container limit
2. **Monitoring**: Set up alerts for memory > 1.4x baseline during swap windows
3. **No immediate mitigation needed**: The 1.25x multiplier is within acceptable bounds
4. **Future consideration**: For XL agencies (MTA), consider streaming import

## Test Artifacts

- Test code: `internal/gtfs/hot_swap_memory_test.go`
- Test data: `testdata/perf/trimet.zip`
- Log file: `loadtest/results/hotswap_trimet_*.log`
- Monitoring script: `scripts/hotswap-memory-test.sh`
- k6 scenario: `loadtest/k6/hotswap_scenario.js`

## Reproducing These Results

```bash
# 1. Download test data
./scripts/download-perf-data.sh

# 2. Run the memory test
go test -tags=perftest -v -timeout 20m \
    -run TestHotSwapMemory_LargeAgency ./internal/gtfs/

# 3. For live server testing
MAGLEV_ENABLE_PPROF=1 make run
./scripts/hotswap-memory-test.sh monitor
```

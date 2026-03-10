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
go tool pprof http://localhost:4000/debug/pprof/profile?seconds=30
```

### Heap Profiling
```bash
go tool pprof http://localhost:4000/debug/pprof/heap
```

### Mutex Contention
```bash
go tool pprof http://localhost:4000/debug/pprof/mutex
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

Steps 2-3 can cause peak RSS to reach ~2x the baseline, potentially triggering OOM in memory-constrained containers.

### Quick Test (Unit Test)

Run the perftest-tagged hot-swap memory test:
```bash
# Download large agency test data first
./scripts/download-perf-data.sh

# Run the hot-swap memory test
go test -tags=perftest -v -run TestHotSwapMemory_LargeAgency ./internal/gtfs/
```

This will output:
- Baseline RSS, Peak RSS, Post-GC RSS
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

#### Option B: RSS Monitoring with k6 Load
```bash
# Terminal 1: Start server
MAGLEV_ENABLE_PPROF=1 make run

# Terminal 2: Run hot-swap specific load test
k6 run loadtest/k6/hotswap_scenario.js

# Terminal 3: Monitor RSS
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
| Peak RSS Multiplier | Peak RSS / Baseline RSS | <2.0x |
| GC Settle Time | Time for memory to return to baseline | <30s |
| Request Failures | Failed requests during swap | <1% |
| Swap Duration | Time for ForceUpdate to complete | <60s |

### Container Sizing Recommendations

Based on the hot-swap behavior, here are recommended container memory limits:

| Agency Size | Baseline RSS | Peak RSS (1.25x) | Recommended Limit |
|-------------|--------------|------------------|-------------------|
| Small (RABA) | ~100 MB | ~125 MB | 256 MB |
| Medium | ~500 MB | ~625 MB | 1 GB |
| Large (TriMet) | ~2.9 GB | ~3.6 GB | 4 GB |
| XL (MTA) | ~6.0 GB | ~7.5 GB | 12 GB |

**Safety margin**: Multiply peak RSS by 2.5x for the container limit to handle unexpected spikes.

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



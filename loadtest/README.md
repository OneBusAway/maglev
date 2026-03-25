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

### Mutex Contention
```bash
go tool pprof http://localhost:6060/debug/pprof/mutex
```

## Note on Test Data
The pre-existing k6 data CSV files (`loadtest/k6/data/*.csv`) currently contain specific dataset IDs (e.g., Seattle-area data). When running against a different GTFS feed (like RABA in CI), you may need to regenerate these CSV files with IDs and coordinates appropriate for the target dataset to ensure the load test exercises actual data serving paths rather than just 404 paths.

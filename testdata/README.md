# Test data

RABA (Redding Area Bus Authority) GTFS in `raba.zip` is used by the default test suite. It is committed to the repo.

## Performance testing (large dataset)

Some benchmarks use a larger GTFS feed to measure behavior under load. That data is not committed.

- **Why**: Performance tests need a big agency (many stops, routes, trips) to be meaningful.
- **How to get it**: From the repo root, run:
  ```bash
  ./scripts/download-perf-data.sh
  ```
- **Where it goes**: `testdata/perf/trimet.zip` (TriMet). The `testdata/perf/` directory is gitignored.
- **Run benchmarks**: Use the `perftest` build tag so that the large-dataset benchmarks are compiled and run:
  ```bash
  go test -tags=perftest -bench=. ./internal/restapi/ -benchmem
  ```
  Without the tag, those benchmarks are not built and the rest of the tests are unchanged.

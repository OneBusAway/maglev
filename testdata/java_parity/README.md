# Java parity fixture

`TestJavaParity_SyntheticTwoAgencyFeed` (`internal/restapi/java_parity_test.go`)
replays a set of requests against a small synthetic feed. It checks that the stop,
route and service IDs in each response match what Java OneBusAway returned.

- `feed/`: the GTFS feed, written by `make_feed.py`. Two agencies; `40` is listed
  first, so it is the feed's default agency. The feed includes a stop shared by
  both agencies, a stop with no routes and a station.
- `java_ids.json`: Java's answers, written by `capture.py`.
- Requests that still differ are listed, with reasons, in `knownJavaDifferences`
  in the test.

## Recapturing Java's answers

Do this after changing the feed or the request list in `capture.py`.

1. Regenerate the feed: `python3 make_feed.py feed`
2. Zip it and serve it over HTTP:
   `(cd feed && zip ../synthetic.zip *.txt) && python3 -m http.server 8765`
3. Start Java OneBusAway 2.7.1 on it with the Docker stack in
   maglev-workspace's `oba-api` skill. Set
   `GTFS_URL=http://host.docker.internal:8765/synthetic.zip`, and set
   `GTFS_TIDY_ARGS=-help` in a compose override. Without that, the stack's gtfstidy
   step deletes stops with no routes, which production Java keeps.
4. Capture: `python3 capture.py http://localhost:8080 > java_ids.json`

The requests use 2026-10-24 at 10:01 America/Los_Angeles. The test pins its
clock to the same moment.

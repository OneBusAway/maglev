"""Writes the synthetic two-agency GTFS feed used for Java parity checks.

usage: python3 make_feed.py OUT_DIR

agency.txt lists "40" before "1", so the feed's default agency is not the
lowest ID. Stops, all within about 100 m of each other:
  A    served by both agencies' routes
  B    served only by agency 1
  C    served only by agency 40
  U    served by nothing (stop_code 999)
  STA  station whose child B2 is served only by agency 1
Trips leave every 30 minutes from 06:00 to 14:00, two trips per block.
"""
import csv
import os
import sys

out_dir = sys.argv[1]
os.makedirs(out_dir, exist_ok=True)


def table(name, header, rows):
    with open(os.path.join(out_dir, name), "w", newline="") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(header)
        w.writerows(rows)


tz = "America/Los_Angeles"
table("agency.txt", ["agency_id", "agency_name", "agency_url", "agency_timezone"], [
    ["40", "Forty Transit", "https://example.com/40", tz],
    ["1", "One Metro", "https://example.com/1", tz],
])


def at(offset):
    return f"{47.6000 + offset * 0.0002:.6f}", "-122.330000"


stops = [
    # id, code, name, offset, location_type, parent
    ("C", "300", "Stop C", 0, "", ""),
    ("A", "100", "Stop A", 1, "", ""),
    ("B", "200", "Stop B", 2, "", ""),
    ("STA", "", "Station", 3, "1", ""),
    ("B2", "250", "Stop B2", 3, "", "STA"),
    ("U", "999", "Stop U", 4, "", ""),
]
table("stops.txt", ["stop_id", "stop_code", "stop_name", "stop_lat", "stop_lon", "location_type", "parent_station"],
      [[s, c, n, *at(o), lt, p] for s, c, n, o, lt, p in stops])

table("routes.txt", ["route_id", "agency_id", "route_short_name", "route_long_name", "route_type"], [
    ["R40", "40", "40X", "Forty Express", "3"],
    ["R1", "1", "1X", "One Local", "3"],
])

table("calendar.txt", ["service_id", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
                       "start_date", "end_date"],
      [["ALL", "1", "1", "1", "1", "1", "1", "1", "20260101", "20271231"]])

patterns = {
    ("R40", "0"): ["C", "A"],
    ("R40", "1"): ["A", "C"],
    ("R1", "0"): ["A", "B", "B2"],
    ("R1", "1"): ["B2", "B", "A"],
}

trips, stop_times = [], []
for (route, direction), seq in patterns.items():
    for n, start in enumerate(range(6 * 60, 14 * 60 + 1, 30)):
        trip = f"{route}-{direction}-{n:02d}"
        trips.append([route, "ALL", trip, direction, f"{route}-{direction}-BLK{n // 2:02d}", f"To {seq[-1]}"])
        for i, stop in enumerate(seq):
            minutes = start + 3 * i
            t = f"{minutes // 60:02d}:{minutes % 60:02d}:00"
            stop_times.append([trip, t, t, stop, str(i + 1)])

table("trips.txt", ["route_id", "service_id", "trip_id", "direction_id", "block_id", "trip_headsign"], trips)
table("stop_times.txt", ["trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence"], stop_times)
